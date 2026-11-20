package describe

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/iface"
	"github.com/gatework/netops/neighbor"
	"github.com/gatework/netops/parse"
)

var (
	// ErrUnsupported reports a platform without description support.
	ErrUnsupported = errors.New("describe: unsupported platform")
	// ErrParse reports configuration output that cannot be read.
	ErrParse = errors.New("describe: unrecognized configuration")
)

// Port is one configured interface.
type Port struct {
	Name        string `json:"name"` // as configured, used in commands
	Description string `json:"description,omitempty"`
}

// Model is what the change works on: the neighbors and the configured
// ports of one device, ports keyed by iface.Key.
type Model struct {
	Platform  string              `json:"platform"`
	Neighbors []neighbor.Neighbor `json:"neighbors"`
	Ports     map[string]Port     `json:"ports"`
}

// Fingerprint digests the configured descriptions, everything a
// description change depends on. It is the change.Fingerprint of Model.
func Fingerprint(m Model) evidence.Digest {
	var b strings.Builder
	b.WriteString(m.Platform)
	for _, k := range slices.Sorted(maps.Keys(m.Ports)) {
		p := m.Ports[k]
		fmt.Fprintf(&b, "\n%s\x00%s\x00%s", k, p.Name, p.Description)
	}
	return evidence.DigestString(b.String())
}

// Syntax is a platform's configuration grammar for descriptions.
type Syntax struct {
	Enter, Leave string // enter and leave configuration mode
	Exit         string // leave interface view
	Set          string // prefix of "description <text>"
	Clear        string // removes the description
}

func (s Syntax) block(name string, lines ...string) []string {
	return append(append([]string{"interface " + name}, lines...), s.Exit)
}

// Profile is the description support of one platform.
type Profile struct {
	Platform  string
	Neighbors neighbor.Probe
	// Config is the read command of the running configuration.
	Config string
	ports  func(ctx context.Context, text string) (map[string]Port, error)
	// syntax is nil on platforms whose descriptions are read-only here.
	syntax *Syntax
}

// Writable reports whether the profile can render description changes.
func (p Profile) Writable() bool { return p.syntax != nil }

// Commands returns the read commands a Model needs, in order.
func (p Profile) Commands() []string { return []string{p.Neighbors.Command, p.Config} }

// Read builds the model from the response bodies of Commands.
func (p Profile) Read(ctx context.Context, neighbors, configuration string) (Model, error) {
	ns, err := p.Neighbors.Parse(neighbors)
	if err != nil {
		return Model{}, err
	}
	ports, err := p.ports(ctx, configuration)
	if err != nil {
		return Model{}, err
	}
	return Model{Platform: p.Platform, Neighbors: ns, Ports: ports}, nil
}

// FromCapture builds the model from a capture of Commands. A failed
// command is an error.
func (p Profile) FromCapture(ctx context.Context, c capture.Capture) (Model, error) {
	bodies := make([]string, 0, 2)
	for _, cmd := range p.Commands() {
		o, ok := c.Answer(cmd)
		if !ok {
			return Model{}, fmt.Errorf("describe: %s: no successful output for %q", p.Platform, cmd)
		}
		lines, err := o.Body(ctx, nil)
		if err != nil {
			return Model{}, err
		}
		texts := make([]string, len(lines))
		for i, l := range lines {
			texts[i] = l.Text
		}
		bodies = append(bodies, strings.Join(texts, "\n"))
	}
	return p.Read(ctx, bodies[0], bodies[1])
}

var (
	comware = Syntax{Enter: "system-view", Leave: "return", Exit: "quit", Set: "description ", Clear: "undo description"}
	cisco   = Syntax{Enter: "configure terminal", Leave: "end", Exit: "exit", Set: "description ", Clear: "no description"}
	hstone  = Syntax{Enter: "configure", Leave: "exit", Exit: "exit", Set: "description ", Clear: "no description"}
)

func profile(platform, cfg string, ports func(context.Context, string) (map[string]Port, error), s *Syntax) Profile {
	n, ok := neighbor.Lookup(platform)
	if !ok {
		panic("describe: no neighbor probe for " + platform)
	}
	return Profile{Platform: platform, Neighbors: n, Config: cfg, ports: ports, syntax: s}
}

// Write support is declared only where the dialogue has test evidence
// (the retired net-connector topology suite); Huawei and Junos are read
// only until it exists.
var builtin = map[string]Profile{
	"h3c":          profile("h3c", "display current-configuration", tree(config.Comware()), &comware),
	"h3c_wireless": profile("h3c_wireless", "display current-configuration", tree(config.Comware()), &comware),
	"huawei":       profile("huawei", "display current-configuration", tree(config.Comware()), nil),
	"cisco_ios":    profile("cisco_ios", "show running-config", tree(config.Cisco()), &cisco),
	"cisco_nxos":   profile("cisco_nxos", "show running-config", tree(config.Cisco()), &cisco),
	"hillstone":    profile("hillstone", "show configuration running", tree(config.Cisco()), &hstone),
	"juniper":      profile("juniper", "show configuration | display set | no-more", junosSet, nil),
}

// Lookup returns the profile of a platform.
func Lookup(platform string) (Profile, error) {
	p, ok := builtin[platform]
	if !ok {
		return Profile{}, fmt.Errorf("%w: %s", ErrUnsupported, platform)
	}
	return p, nil
}

// Platforms lists the platforms with a profile, sorted.
func Platforms() []string { return slices.Sorted(maps.Keys(builtin)) }

// tree reads "interface X" sections and their "description" lines.
func tree(d config.Dialect) func(context.Context, string) (map[string]Port, error) {
	return func(ctx context.Context, text string) (map[string]Port, error) {
		doc, err := config.Parse(ctx, text, d, parse.Limits{})
		if err != nil {
			return nil, err
		}
		ports := map[string]Port{}
		for _, sec := range doc.Sections("interface") {
			name := strings.TrimSpace(strings.TrimPrefix(sec.Text, "interface"))
			if name == "" {
				continue
			}
			p := Port{Name: name}
			for _, c := range sec.ChildrenWithPrefix("description") {
				desc := strings.TrimSpace(strings.TrimPrefix(c.Text, "description"))
				if desc == "" {
					return nil, fmt.Errorf("%w: %s has an empty description", ErrParse, name)
				}
				p.Description = desc
			}
			k := iface.Key(name)
			if old, dup := ports[k]; dup && old.Description != "" && p.Description != "" && old.Description != p.Description {
				return nil, fmt.Errorf("%w: %s is configured twice", ErrParse, name)
			}
			if old, dup := ports[k]; dup && p.Description == "" {
				p.Description = old.Description
			}
			ports[k] = p
		}
		return ports, nil
	}
}

var junosLine = regexp.MustCompile(`^set interfaces (\S+)(?: unit (\d+))?(?: (description) (.*)|\s.*)?$`)

// junosSet reads "display set" output. A unit is its own port "ge-0/0/0.0".
func junosSet(_ context.Context, text string) (map[string]Port, error) {
	ports := map[string]Port{}
	for line := range strings.SplitSeq(text, "\n") {
		m := junosLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		name := m[1]
		if m[2] != "" {
			name += "." + m[2]
		}
		p := ports[iface.Key(name)]
		p.Name = name
		if m[3] != "" {
			d, err := unquote(m[4])
			if err != nil || d == "" {
				return nil, fmt.Errorf("%w: %s description", ErrParse, name)
			}
			p.Description = d
		}
		ports[iface.Key(name)] = p
	}
	return ports, nil
}

func unquote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, `"`) {
		return s, nil
	}
	return strconv.Unquote(s)
}
