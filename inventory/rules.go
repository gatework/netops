package inventory

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"unicode"

	"github.com/gobwas/glob"
	"go4.org/netipx"

	"github.com/gatework/netops/device"
)

// Classifier maps an inventory identity to a platform key. It is the port
// through which a transport module (net-connector's driver.Registry)
// supplies its platform catalog without netops importing it.
type Classifier interface {
	// Platforms lists the known platform keys.
	Platforms() []string
	// Classify returns the platform of id, or false when none matches.
	Classify(id device.Identity) (platform string, ok bool)
}

// MatchMode selects how name filters compare.
type MatchMode string

// Name match modes.
const (
	MatchContains MatchMode = "contains"
	MatchPrefix   MatchMode = "prefix"
	MatchGlob     MatchMode = "glob"
)

// Row is one raw inventory record, as Netdisco reports it.
type Row struct {
	IP        string `json:"ip"`
	Name      string `json:"name"`
	DNS       string `json:"dns"`
	Vendor    string `json:"vendor"`
	OS        string `json:"os"`
	Model     string `json:"model"`
	OSVersion string `json:"os_ver"`
	Serial    string `json:"serial"`
}

func (r Row) size() int {
	return len(r.IP) + len(r.Name) + len(r.DNS) + len(r.Vendor) + len(r.OS) + len(r.Model) + len(r.OSVersion) + len(r.Serial)
}

// Drop reasons recorded in Report.Dropped by the rules.
const (
	DropInvalidAddress  = "invalid_address"
	DropFiltered        = "filtered"
	DropUnsupported     = "unsupported_vendor"
	DropPlatformNotKept = "platform_not_selected"
)

// rules selects rows and resolves their platform. Resolution order: host
// override, device rule (vendor + optional OS and model prefix), vendor
// override, then the Classifier.
type rules struct {
	includeHosts, excludeHosts *netipx.IPSet
	names                      textRules
	includePlatforms           []string
	excludePlatforms           []string
	hosts                      map[netip.Addr]string
	vendors                    map[string]string
	mappings                   []DeviceRule
	classifier                 Classifier
	known                      map[string]bool
}

func newRules(c NetdiscoConfig, cl Classifier) (*rules, error) {
	if cl == nil {
		return nil, fmt.Errorf("%w: netdisco needs a platform classifier", ErrInvalid)
	}
	r := &rules{classifier: cl, known: map[string]bool{}, hosts: map[netip.Addr]string{}, vendors: map[string]string{}}
	for _, p := range cl.Platforms() {
		r.known[p] = true
	}
	var err error
	if r.includeHosts, err = addressSet(c.IncludeHosts); err != nil {
		return nil, err
	}
	if r.excludeHosts, err = addressSet(c.ExcludeHosts); err != nil {
		return nil, err
	}
	if r.names, err = newTextRules(c.IncludeNames, c.ExcludeNames, c.NameMatch); err != nil {
		return nil, err
	}
	for _, p := range slices.Concat(c.IncludePlatforms, c.ExcludePlatforms, c.Platforms) {
		if !r.known[p] {
			return nil, fmt.Errorf("%w: unknown platform %q", ErrInvalid, p)
		}
	}
	r.includePlatforms, r.excludePlatforms = c.IncludePlatforms, c.ExcludePlatforms
	for k, v := range c.HostOverrides {
		a, err := netip.ParseAddr(k)
		if err != nil || a.Zone() != "" || !r.known[v] {
			return nil, fmt.Errorf("%w: host_overrides %q", ErrInvalid, k)
		}
		r.hosts[a.Unmap()] = v
	}
	for k, v := range c.PlatformOverrides {
		label := device.NormalizeLabel(k)
		if label == "" || !r.known[v] {
			return nil, fmt.Errorf("%w: platform_overrides %q", ErrInvalid, k)
		}
		r.vendors[label] = v
	}
	for _, m := range c.DeviceRules {
		if !r.known[m.Platform] {
			return nil, fmt.Errorf("%w: device_rules platform %q", ErrInvalid, m.Platform)
		}
		m.Vendor = device.NormalizeLabel(m.Vendor)
		if m.OS != nil {
			v := device.NormalizeLabel(*m.OS)
			m.OS = &v
		}
		if m.ModelPrefix != nil {
			v := device.NormalizeLabel(*m.ModelPrefix)
			m.ModelPrefix = &v
		}
		r.mappings = append(r.mappings, m)
	}
	return r, nil
}

// addressSet parses addresses, prefixes and ranges ("a-b", or "a-n" for
// the last IPv4 octet).
func addressSet(values []string) (*netipx.IPSet, error) {
	var b netipx.IPSetBuilder
	for _, v := range values {
		switch {
		case strings.Contains(v, "/"):
			p, err := netip.ParsePrefix(v)
			if err != nil {
				return nil, fmt.Errorf("%w: address %q", ErrInvalid, v)
			}
			b.AddPrefix(p.Masked())
		case strings.Contains(v, "-"):
			lo, hi, _ := strings.Cut(v, "-")
			first, err := netip.ParseAddr(lo)
			if err != nil {
				return nil, fmt.Errorf("%w: address %q", ErrInvalid, v)
			}
			if first.Is4() && !strings.Contains(hi, ".") {
				o := first.As4()
				hi = fmt.Sprintf("%d.%d.%d.%s", o[0], o[1], o[2], hi)
			}
			last, err := netip.ParseAddr(hi)
			rng := netipx.IPRangeFrom(first, last)
			if err != nil || !rng.IsValid() {
				return nil, fmt.Errorf("%w: address %q", ErrInvalid, v)
			}
			b.AddRange(rng)
		default:
			a, err := netip.ParseAddr(v)
			if err != nil || a.Zone() != "" {
				return nil, fmt.Errorf("%w: address %q", ErrInvalid, v)
			}
			b.Add(a.Unmap())
		}
	}
	s, err := b.IPSet()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return s, nil
}

type textRules struct {
	mode             MatchMode
	include, exclude []string
	globs            map[string]glob.Glob
}

func newTextRules(include, exclude []string, mode MatchMode) (textRules, error) {
	if mode == "" {
		mode = MatchContains
	}
	if !slices.Contains([]MatchMode{MatchContains, MatchPrefix, MatchGlob}, mode) {
		return textRules{}, fmt.Errorf("%w: name_match %q", ErrInvalid, mode)
	}
	t := textRules{mode: mode, globs: map[string]glob.Glob{}}
	for i, list := range [][]string{include, exclude} {
		for _, v := range list {
			if strings.TrimSpace(v) == "" || strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return textRules{}, fmt.Errorf("%w: name filter %q", ErrInvalid, v)
			}
			v = strings.ToLower(v)
			if mode == MatchGlob {
				g, err := glob.Compile(v)
				if err != nil {
					return textRules{}, fmt.Errorf("%w: name glob %q", ErrInvalid, v)
				}
				t.globs[v] = g
			}
			if i == 0 {
				t.include = append(t.include, v)
			} else {
				t.exclude = append(t.exclude, v)
			}
		}
	}
	return t, nil
}

func (t textRules) any(value string, list []string) bool {
	value = strings.ToLower(value)
	return slices.ContainsFunc(list, func(p string) bool {
		switch t.mode {
		case MatchPrefix:
			return strings.HasPrefix(value, p)
		case MatchGlob:
			return t.globs[p].Match(value)
		case MatchContains:
		}
		return strings.Contains(value, p)
	})
}

func (t textRules) selects(value string) bool {
	if t.any(value, t.exclude) {
		return false
	}
	return len(t.include) == 0 || t.any(value, t.include)
}

// platform resolves the platform key of a row.
func (r *rules) platform(row Row, addr netip.Addr) string {
	if p := r.hosts[addr]; p != "" {
		return p
	}
	id := device.Identity{Vendor: row.Vendor, OS: row.OS, Model: row.Model}
	n := id.Normalized()
	for _, m := range r.mappings {
		if m.Vendor == n.Vendor && (m.OS == nil || *m.OS == n.OS) && (m.ModelPrefix == nil || strings.HasPrefix(n.Model, *m.ModelPrefix)) {
			return m.Platform
		}
	}
	if p := r.vendors[n.Vendor]; p != "" {
		return p
	}
	if p, ok := r.classifier.Classify(id); ok && r.known[p] {
		return p
	}
	return ""
}

// device converts a row, or returns the drop reason.
func (r *rules) device(row Row) (device.Device, string) {
	addr, err := netip.ParseAddr(strings.TrimSpace(row.IP))
	if err != nil || addr.Zone() != "" {
		return device.Device{}, DropInvalidAddress
	}
	addr = addr.Unmap()
	name := cmpOr(strings.TrimSpace(row.Name), strings.TrimSpace(row.DNS))
	p := r.platform(row, addr)
	switch {
	case r.excludeHosts.Contains(addr),
		len(r.includeHosts.Prefixes()) > 0 && !r.includeHosts.Contains(addr),
		!r.names.selects(name),
		p != "" && slices.Contains(r.excludePlatforms, p),
		p != "" && len(r.includePlatforms) > 0 && !slices.Contains(r.includePlatforms, p):
		return device.Device{}, DropFiltered
	case p == "":
		return device.Device{}, DropUnsupported
	}
	return device.Device{
		Name: cmpOr(name, addr.String()), Mgmt: addr, Platform: p, Vendor: row.Vendor,
		Model: row.Model, Version: row.OSVersion,
	}, ""
}
