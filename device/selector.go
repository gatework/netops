package device

import (
	"fmt"
	"net/netip"
	"path"
	"slices"
	"strings"
)

// Selector picks devices. It is a disjunction of clauses; a clause matches
// when, for every key it mentions, at least one of its terms for that key
// matches. The zero Selector matches every device.
//
// Clause grammar (terms separated by commas or spaces):
//
//	192.0.2.1          management address
//	192.0.2.0/24       management prefix
//	core-*             name glob (path.Match syntax, case-insensitive)
//	core               name substring (case-insensitive)
//	id=sw1             exact ID
//	role=leaf          exact role; likewise site=, platform=, vendor=, model=
//	label:rack=r12     exact label value
//
// For example "role=leaf,role=border site=dc1" selects leaves and borders of
// site dc1. A Selector is immutable and safe for concurrent use.
type Selector struct {
	clauses []clause
}

type clause []term

type term struct {
	key   string // "addr", "prefix", "name", "id", "role", ..., "label:<k>"
	value string
	addr  netip.Addr
	pfx   netip.Prefix
}

var attributeKeys = []string{"id", "role", "site", "platform", "vendor", "model"}

// ParseSelector parses one clause per expression. Empty expressions are
// ignored; no expressions yield the match-all selector.
func ParseSelector(exprs ...string) (Selector, error) {
	var s Selector
	for _, expr := range exprs {
		c, err := parseClause(expr)
		if err != nil {
			return Selector{}, err
		}
		if len(c) > 0 {
			s.clauses = append(s.clauses, c)
		}
	}
	return s, nil
}

func parseClause(expr string) (clause, error) {
	fields := strings.FieldsFunc(expr, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	c := make(clause, 0, len(fields))
	for _, f := range fields {
		t, err := parseTerm(f)
		if err != nil {
			return nil, err
		}
		c = append(c, t)
	}
	return c, nil
}

func parseTerm(f string) (term, error) {
	if a, err := netip.ParseAddr(f); err == nil {
		return term{key: "addr", value: f, addr: a.Unmap()}, nil
	}
	if p, err := netip.ParsePrefix(f); err == nil {
		return term{key: "prefix", value: f, pfx: p.Masked()}, nil
	}
	if k, v, ok := strings.Cut(f, "="); ok {
		if v == "" {
			return term{}, fmt.Errorf("device: selector %q has an empty value", f)
		}
		if lk, isLabel := strings.CutPrefix(k, "label:"); isLabel {
			if !labelPattern.MatchString(lk) {
				return term{}, fmt.Errorf("device: selector %q has an invalid label key", f)
			}
			return term{key: "label:" + lk, value: v}, nil
		}
		if !slices.Contains(attributeKeys, k) {
			return term{}, fmt.Errorf("device: selector %q has an unknown key", f)
		}
		return term{key: k, value: v}, nil
	}
	name := strings.ToLower(f)
	if _, err := path.Match(name, ""); err != nil {
		return term{}, fmt.Errorf("device: selector %q is not a valid glob", f)
	}
	return term{key: "name", value: name}, nil
}

// Empty reports whether s matches every device.
func (s Selector) Empty() bool { return len(s.clauses) == 0 }

// Match reports whether s selects d.
func (s Selector) Match(d Device) bool {
	if len(s.clauses) == 0 {
		return true
	}
	return slices.ContainsFunc(s.clauses, func(c clause) bool { return c.match(d) })
}

// String returns the clauses in parse form, separated by " | ".
func (s Selector) String() string {
	parts := make([]string, len(s.clauses))
	for i, c := range s.clauses {
		terms := make([]string, len(c))
		for j, t := range c {
			terms[j] = t.String()
		}
		parts[i] = strings.Join(terms, ",")
	}
	return strings.Join(parts, " | ")
}

func (c clause) match(d Device) bool {
	// Group by key: every key needs one matching term. Addresses and
	// prefixes form one "address" group.
	groups := map[string]bool{}
	for _, t := range c {
		k := t.key
		if k == "prefix" {
			k = "addr"
		}
		groups[k] = groups[k] || t.match(d)
	}
	for _, ok := range groups {
		if !ok {
			return false
		}
	}
	return true
}

func (t term) String() string {
	switch t.key {
	case "addr", "prefix", "name":
		return t.value
	}
	return t.key + "=" + t.value
}

func (t term) match(d Device) bool {
	switch t.key {
	case "addr":
		return d.Mgmt == t.addr
	case "prefix":
		return t.pfx.Contains(d.Mgmt)
	case "name":
		name := strings.ToLower(d.Name)
		if strings.ContainsAny(t.value, "*?[") {
			ok, _ := path.Match(t.value, name)
			return ok
		}
		return strings.Contains(name, t.value)
	case "id":
		return d.ID == t.value
	case "role":
		return d.Role == t.value
	case "site":
		return d.Site == t.value
	case "platform":
		return d.Platform == t.value
	case "vendor":
		return strings.EqualFold(d.Vendor, t.value)
	case "model":
		return strings.EqualFold(d.Model, t.value)
	}
	if k, ok := strings.CutPrefix(t.key, "label:"); ok {
		v, has := d.Labels[k]
		return has && v == t.value
	}
	return false
}
