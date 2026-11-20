package assess

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"
)

// RulePolicy adjusts one rule.
type RulePolicy struct {
	Enabled  *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Severity Severity `yaml:"severity,omitempty" json:"severity,omitempty"`
	// Params are raw decoded overrides; the Engine converts them to the
	// declared kinds and rejects mismatches.
	Params map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
}

// Policy enables, disables and tunes rules. With OnlyEnabled, rules run
// only when the policy enables them explicitly.
type Policy struct {
	OnlyEnabled bool                  `yaml:"only_enabled,omitempty" json:"only_enabled,omitempty"`
	Rules       map[RuleID]RulePolicy `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// DecodePolicy decodes a YAML policy strictly: unknown fields are errors.
// An empty document is the empty policy.
func DecodePolicy(body []byte) (Policy, error) {
	var p Policy
	if len(bytes.TrimSpace(body)) == 0 {
		return p, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("%w: policy: %w", ErrInvalid, err)
	}
	return p, nil
}

// Check reports policy entries for rules that none of the known IDs name.
// Pass the IDs of every catalog the policy is meant for, so that one
// policy can drive several engines.
func (p Policy) Check(known ...[]RuleID) error {
	all := map[RuleID]bool{}
	for _, ids := range known {
		for _, id := range ids {
			all[id] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(p.Rules)) {
		if !all[id] {
			return fmt.Errorf("%w: policy names unknown rule %s", ErrInvalid, id)
		}
		if s := p.Rules[id].Severity; s != SeverityUnset && !s.Valid() {
			return fmt.Errorf("%w: policy severity for %s", ErrInvalid, id)
		}
	}
	return nil
}

func (p Policy) enabled(id RuleID) bool {
	rp, ok := p.Rules[id]
	if ok && rp.Enabled != nil {
		return *rp.Enabled
	}
	return !p.OnlyEnabled
}
