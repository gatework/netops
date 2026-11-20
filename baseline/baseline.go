package baseline

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/drift"
	"github.com/gatework/netops/parse"
)

// Setting is the expected value set of one keyed setting, such as
// "ntp.servers". A single-valued setting has one value. With Exclusive,
// observed values outside the set are deviations too.
type Setting struct {
	Values    []string `yaml:"values" json:"values"`
	Exclusive bool     `yaml:"exclusive,omitempty" json:"exclusive,omitempty"`
}

// Rule is a text compliance requirement on a configuration. Within every
// block whose path matches Section (one regular expression per level,
// empty for the top level), each Require pattern must match at least one
// command and no Forbid pattern may match any command.
type Rule struct {
	ID       string   `yaml:"id" json:"id"`
	Title    string   `yaml:"title" json:"title"`
	Section  []string `yaml:"section,omitempty" json:"section,omitempty"`
	Require  []string `yaml:"require,omitempty" json:"require,omitempty"`
	Forbid   []string `yaml:"forbid,omitempty" json:"forbid,omitempty"`
	Severity string   `yaml:"severity,omitempty" json:"severity,omitempty"`

	section []*regexp.Regexp
	require []*regexp.Regexp
	forbid  []*regexp.Regexp
}

// Layer is one level of a profile.
type Layer struct {
	Settings map[string]Setting `yaml:"settings,omitempty" json:"settings,omitempty"`
	Rules    []Rule             `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// Profile is a layered baseline. Layers apply from general to specific:
// Default, then the device's role, its site, and finally "site/role". A
// more specific layer replaces settings and rules with the same key or ID.
type Profile struct {
	Default   Layer            `yaml:"default" json:"default"`
	Roles     map[string]Layer `yaml:"roles,omitempty" json:"roles,omitempty"`
	Sites     map[string]Layer `yaml:"sites,omitempty" json:"sites,omitempty"`
	SiteRoles map[string]Layer `yaml:"site_roles,omitempty" json:"site_roles,omitempty"`
}

// Decode parses a YAML profile strictly and compiles its patterns.
func Decode(body []byte) (Profile, error) {
	var p Profile
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("baseline: decode: %w", err)
	}
	if err := compileProfile(&p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func (l *Layer) compile() error {
	seen := map[string]bool{}
	for i := range l.Rules {
		r := &l.Rules[i]
		if r.ID == "" || seen[r.ID] {
			return fmt.Errorf("rule %d: missing or duplicate id %q", i, r.ID)
		}
		seen[r.ID] = true
		if r.Severity != "" {
			if _, err := assess.ParseSeverity(r.Severity); err != nil {
				return fmt.Errorf("rule %s: %w", r.ID, err)
			}
		}
		var err error
		if r.section, err = compileAll(r.Section); err != nil {
			return fmt.Errorf("rule %s: %w", r.ID, err)
		}
		if r.require, err = compileAll(r.Require); err != nil {
			return fmt.Errorf("rule %s: %w", r.ID, err)
		}
		if r.forbid, err = compileAll(r.Forbid); err != nil {
			return fmt.Errorf("rule %s: %w", r.ID, err)
		}
	}
	return nil
}

func compileAll(exprs []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, len(exprs))
	for i, e := range exprs {
		re, err := regexp.Compile(e)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", e, err)
		}
		out[i] = re
	}
	return out, nil
}

// Resolved is the effective baseline for one device.
type Resolved struct {
	Settings map[string]Setting
	Rules    []Rule // ordered by ID
}

// Resolve applies the layers that match d.
func (p Profile) Resolve(d device.Device) Resolved {
	r := Resolved{Settings: map[string]Setting{}}
	rules := map[string]Rule{}
	apply := func(l Layer) {
		maps.Copy(r.Settings, l.Settings)
		for _, rule := range l.Rules {
			rules[rule.ID] = rule
		}
	}
	apply(p.Default)
	if l, ok := p.Roles[d.Role]; ok && d.Role != "" {
		apply(l)
	}
	if l, ok := p.Sites[d.Site]; ok && d.Site != "" {
		apply(l)
	}
	if l, ok := p.SiteRoles[d.Site+"/"+d.Role]; ok && d.Site != "" && d.Role != "" {
		apply(l)
	}
	for _, id := range slices.Sorted(maps.Keys(rules)) {
		r.Rules = append(r.Rules, rules[id])
	}
	return r
}

func compileProfile(p *Profile) error {
	layers := []*Layer{&p.Default}
	for _, m := range []map[string]Layer{p.Roles, p.Sites, p.SiteRoles} {
		for _, k := range slices.Sorted(maps.Keys(m)) {
			l := m[k]
			if err := l.compile(); err != nil {
				return fmt.Errorf("baseline: layer %s: %w", k, err)
			}
			m[k] = l
		}
	}
	for _, l := range layers {
		if err := l.compile(); err != nil {
			return fmt.Errorf("baseline: default layer: %w", err)
		}
	}
	return nil
}

// CheckSettings compares observed setting values with the baseline.
// Settings missing from observed are reported as missing; render masks
// values for display.
func (r Resolved) CheckSettings(subject assess.Subject, observed map[string][]string, render func(key, value string) string) []drift.Deviation {
	var out []drift.Deviation
	for _, key := range slices.Sorted(maps.Keys(r.Settings)) {
		s := r.Settings[key]
		out = append(out, drift.Keys(drift.SourceBaseline, subject, key, s.Values, observed[key], s.Exclusive,
			func(v string) string { return render(key, v) })...)
	}
	return out
}

// Violation is one failed compliance rule.
type Violation struct {
	Rule    string
	Title   string
	Block   []string // path of the offending block, empty for the top level
	Kind    drift.Kind
	Pattern string
	Line    int // offending line for a forbidden match; the block line otherwise
	Text    string
}

// CheckConfig applies the compliance rules to a parsed configuration.
// Forbidden commands are reported with their masked text.
func (r Resolved) CheckConfig(doc *config.Document) []Violation {
	var out []Violation
	for _, rule := range r.Rules {
		for _, blk := range blocks(doc.Root(), rule.section) {
			out = append(out, rule.check(doc, blk)...)
		}
	}
	return out
}

func (rule Rule) check(doc *config.Document, blk *parse.Node) []Violation {
	var out []Violation
	lines := blk.Children
	if len(rule.section) == 0 {
		lines = slices.Collect(blk.Walk())
	}
	path := blk.Path()
	for i, re := range rule.require {
		if !slices.ContainsFunc(lines, func(n *parse.Node) bool { return re.MatchString(n.Text) }) {
			out = append(out, Violation{Rule: rule.ID, Title: rule.Title, Block: path, Kind: drift.KindMissing,
				Pattern: rule.Require[i], Line: blk.Line.Number})
		}
	}
	for i, re := range rule.forbid {
		for _, n := range lines {
			if re.MatchString(n.Text) {
				out = append(out, Violation{Rule: rule.ID, Title: rule.Title, Block: path, Kind: drift.KindUnexpected,
					Pattern: rule.Forbid[i], Line: n.Line.Number, Text: doc.Dialect().Mask(n.Text)})
			}
		}
	}
	return out
}

// blocks returns the nodes whose path matches section level by level; an
// empty section yields the root.
func blocks(root *parse.Node, section []*regexp.Regexp) []*parse.Node {
	cur := []*parse.Node{root}
	for _, re := range section {
		var next []*parse.Node
		for _, n := range cur {
			for _, c := range n.Children {
				if re.MatchString(c.Text) {
					next = append(next, c)
				}
			}
		}
		cur = next
	}
	return cur
}

// Derive proposes a single-layer baseline from peers: each setting whose
// values agree on at least quorum of the devices becomes expected.
// observed maps device ID to its settings. The result is deterministic.
func Derive(observed map[string]map[string][]string, quorum float64) Layer {
	keys := map[string]bool{}
	for _, s := range observed {
		for k := range s {
			keys[k] = true
		}
	}
	l := Layer{Settings: map[string]Setting{}}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		members := make([]drift.Member[[]string], 0, len(observed))
		for _, id := range slices.Sorted(maps.Keys(observed)) {
			v := slices.Clone(observed[id][k])
			slices.Sort(v)
			members = append(members, drift.Member[[]string]{ID: id, Value: v})
		}
		a := drift.Consensus(members, func(v []string) string { return strings.Join(v, "\x00") }, quorum)
		if a.Reached && len(a.Value) > 0 {
			l.Settings[k] = Setting{Values: a.Value}
		}
	}
	return l
}
