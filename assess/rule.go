package assess

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"regexp"
	"slices"

	"github.com/gatework/netops/evidence"
)

// RuleID names a rule: dot-separated lowercase segments such as
// "wireless.ap_not_running".
type RuleID string

var ruleIDPattern = regexp.MustCompile(`\A[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*\z`)

// Meta is the declaration of a rule.
type Meta struct {
	ID       RuleID
	Title    string
	Severity Severity
	Category Category
	// Requires lists the dimensions that must be complete for the rule to
	// run.
	Requires []evidence.Dimension
	// Verify lists the dimensions whose commands re-check a finding.
	Verify     []evidence.Dimension
	References []string
	Params     []Param
}

func (m Meta) validate() error {
	switch {
	case !ruleIDPattern.MatchString(string(m.ID)):
		return fmt.Errorf("%w: rule id %q", ErrInvalid, m.ID)
	case m.Title == "":
		return fmt.Errorf("%w: rule %s has no title", ErrInvalid, m.ID)
	case !m.Severity.Valid():
		return fmt.Errorf("%w: rule %s default severity", ErrInvalid, m.ID)
	case !m.Category.Valid():
		return fmt.Errorf("%w: rule %s category %q", ErrInvalid, m.ID, m.Category)
	}
	seen := map[string]bool{}
	for _, p := range m.Params {
		if !paramName.MatchString(p.name) || seen[p.name] {
			return fmt.Errorf("%w: rule %s parameter %q", ErrInvalid, m.ID, p.name)
		}
		seen[p.name] = true
	}
	return nil
}

func (m Meta) params() map[string]Param {
	out := make(map[string]Param, len(m.Params))
	for _, p := range m.Params {
		out[p.name] = p
	}
	return out
}

// Input is what a rule evaluates.
type Input[M any] struct {
	Model  M
	Params Params
}

// Rule evaluates one model. Evaluate must not mutate the model and must be
// safe to call concurrently with other rules. An error marks the rule run
// as failed; its message is not reported, only that the rule failed.
type Rule[M any] interface {
	Meta() Meta
	Evaluate(ctx context.Context, in Input[M]) ([]Finding, error)
}

type funcRule[M any] struct {
	meta Meta
	eval func(context.Context, Input[M]) ([]Finding, error)
}

func (r funcRule[M]) Meta() Meta { return r.meta }

func (r funcRule[M]) Evaluate(ctx context.Context, in Input[M]) ([]Finding, error) {
	return r.eval(ctx, in)
}

// NewRule returns a rule from its declaration and evaluation function.
//
//nolint:iface // the interface is the extension point; funcRule is one of many implementations
func NewRule[M any](meta Meta, eval func(context.Context, Input[M]) ([]Finding, error)) Rule[M] {
	return funcRule[M]{meta: meta, eval: eval}
}

// Check returns a rule whose evaluation cannot fail.
func Check[M any](meta Meta, eval func(Input[M]) []Finding) Rule[M] {
	return NewRule(meta, func(_ context.Context, in Input[M]) ([]Finding, error) { return eval(in), nil })
}

// Catalog is an ordered, validated set of rules with unique IDs. It is
// immutable and safe for concurrent use.
type Catalog[M any] struct {
	rules []Rule[M]
	index map[RuleID]int
}

// NewCatalog validates the rules and their metadata.
func NewCatalog[M any](rules ...Rule[M]) (*Catalog[M], error) {
	c := &Catalog[M]{index: make(map[RuleID]int, len(rules))}
	for _, r := range rules {
		if r == nil {
			return nil, fmt.Errorf("%w: nil rule", ErrInvalid)
		}
		meta := r.Meta()
		if err := meta.validate(); err != nil {
			return nil, err
		}
		if _, dup := c.index[meta.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate rule %s", ErrInvalid, meta.ID)
		}
		c.index[meta.ID] = len(c.rules)
		c.rules = append(c.rules, r)
	}
	return c, nil
}

// Rules yields the rules in declaration order.
func (c *Catalog[M]) Rules() iter.Seq[Rule[M]] { return slices.Values(c.rules) }

// IDs returns the rule IDs in declaration order.
func (c *Catalog[M]) IDs() []RuleID {
	out := make([]RuleID, len(c.rules))
	for i, r := range c.rules {
		out[i] = r.Meta().ID
	}
	return out
}

// Lookup returns the rule with id.
func (c *Catalog[M]) Lookup(id RuleID) (Rule[M], bool) {
	i, ok := c.index[id]
	if !ok {
		return nil, false
	}
	return c.rules[i], true
}

// Len returns the number of rules.
func (c *Catalog[M]) Len() int { return len(c.rules) }

// Metas returns the declarations of every rule, for documentation and
// policy templates.
func (c *Catalog[M]) Metas() []Meta {
	out := make([]Meta, len(c.rules))
	for i, r := range c.rules {
		out[i] = r.Meta()
	}
	return out
}

// effective returns the declared parameters with overrides applied.
func effective(meta Meta, overrides map[string]any) (map[string]Param, error) {
	decl := meta.params()
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		p, ok := decl[name]
		if !ok {
			return nil, fmt.Errorf("%w: rule %s has no parameter %q", ErrInvalid, meta.ID, name)
		}
		v, err := p.override(overrides[name])
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", meta.ID, err)
		}
		p.value = v
		decl[name] = p
	}
	return decl, nil
}
