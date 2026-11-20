package assess

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/gatework/netops/evidence"
)

// SchemaVersion is the version of the Assessment JSON document.
const SchemaVersion = 1

// RuleState is the outcome of one rule in one run.
type RuleState string

// Rule states.
const (
	RuleRan      RuleState = "ran"
	RuleSkipped  RuleState = "skipped"
	RuleDisabled RuleState = "disabled"
	RuleFailed   RuleState = "failed"
)

// Reason explains a skipped or failed rule.
type Reason string

// Reasons.
const (
	ReasonInsufficientEvidence Reason = "insufficient_evidence"
	ReasonCancelled            Reason = "cancelled"
	ReasonError                Reason = "rule_error"
	ReasonPanic                Reason = "rule_panic"
	ReasonInvalidFinding       Reason = "invalid_finding"
	ReasonUndeclaredParam      Reason = "undeclared_param"
)

// RuleOutcome records what happened to one rule.
type RuleOutcome struct {
	Rule     RuleID               `json:"rule"`
	State    RuleState            `json:"state"`
	Reason   Reason               `json:"reason,omitempty"`
	Missing  []evidence.Dimension `json:"missing,omitempty"`
	Findings int                  `json:"findings"`
}

// Assessment is the result of one engine run over one model.
type Assessment struct {
	SchemaVersion int              `json:"schema_version"`
	Findings      []Finding        `json:"findings"`
	Rules         []RuleOutcome    `json:"rules"`
	Counts        map[Severity]int `json:"counts"`
}

// Highest returns the most severe finding severity, or SeverityUnset.
func (a Assessment) Highest() Severity {
	h := SeverityUnset
	for _, f := range a.Findings {
		h = max(h, f.Severity)
	}
	return h
}

// Incomplete reports whether any enabled rule did not run.
func (a Assessment) Incomplete() bool {
	return slices.ContainsFunc(a.Rules, func(o RuleOutcome) bool {
		return o.State == RuleSkipped || o.State == RuleFailed
	})
}

// VerifyCommands resolves the commands that re-check a dimension.
type VerifyCommands interface {
	Commands(dim evidence.Dimension) []string
}

// Options configure an Engine.
type Options struct {
	// Verify resolves verification commands; nil leaves them out.
	Verify VerifyCommands
	// Workers bounds concurrent rule evaluation; 0 or 1 runs serially.
	Workers int
	// Logger receives rule failures with stacks; nil discards them.
	Logger *slog.Logger
	// Partial ignores policy entries for rules outside this catalog, for a
	// policy shared by several engines and checked with Policy.Check.
	Partial bool
}

type compiled[M any] struct {
	rule     Rule[M]
	meta     Meta
	enabled  bool
	override Severity // policy severity, overriding everything
	params   map[string]Param
	verify   []string
}

// Engine runs a catalog under a policy. It is immutable and safe for
// concurrent use.
type Engine[M any] struct {
	rules   []compiled[M]
	workers int
	log     *slog.Logger
}

// NewEngine validates the policy against the catalog, converts parameter
// overrides to their declared kinds and resolves verification commands.
func NewEngine[M any](catalog *Catalog[M], policy Policy, o Options) (*Engine[M], error) {
	if catalog == nil {
		return nil, fmt.Errorf("%w: nil catalog", ErrInvalid)
	}
	if !o.Partial {
		if err := policy.Check(catalog.IDs()); err != nil {
			return nil, err
		}
	}
	e := &Engine[M]{workers: max(o.Workers, 1), log: o.Logger}
	if e.log == nil {
		e.log = slog.New(slog.DiscardHandler)
	}
	for r := range catalog.Rules() {
		meta := r.Meta()
		rp := policy.Rules[meta.ID]
		params, err := effective(meta, rp.Params)
		if err != nil {
			return nil, err
		}
		c := compiled[M]{
			rule: r, meta: meta, enabled: policy.enabled(meta.ID),
			override: rp.Severity, params: params,
		}
		if o.Verify != nil {
			for _, d := range meta.Verify {
				for _, cmd := range o.Verify.Commands(d) {
					if !slices.Contains(c.verify, cmd) {
						c.verify = append(c.verify, cmd)
					}
				}
			}
		}
		e.rules = append(e.rules, c)
	}
	return e, nil
}

// Run evaluates every enabled rule on model. Rules whose required
// dimensions are not complete in coverage are skipped. Findings are
// completed from metadata and sorted deterministically. The returned error
// is the context error when the run was cancelled; the partial assessment
// is still returned.
func (e *Engine[M]) Run(ctx context.Context, model M, coverage evidence.Coverage) (Assessment, error) {
	outcomes := make([]RuleOutcome, len(e.rules))
	results := make([][]Finding, len(e.rules))
	sem := make(chan struct{}, e.workers)
	var wg sync.WaitGroup
	for i, c := range e.rules {
		outcomes[i] = RuleOutcome{Rule: c.meta.ID}
		switch {
		case !c.enabled:
			outcomes[i].State = RuleDisabled
			continue
		case ctx.Err() != nil:
			outcomes[i].State, outcomes[i].Reason = RuleSkipped, ReasonCancelled
			continue
		}
		if missing := coverage.Missing(c.meta.Requires...); len(missing) > 0 {
			outcomes[i].State, outcomes[i].Reason, outcomes[i].Missing = RuleSkipped, ReasonInsufficientEvidence, missing
			continue
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			results[i], outcomes[i] = e.evaluate(ctx, c, model)
		})
	}
	wg.Wait()
	a := Assessment{SchemaVersion: SchemaVersion, Findings: []Finding{}, Rules: outcomes, Counts: map[Severity]int{}}
	for _, fs := range results {
		a.Findings = append(a.Findings, fs...)
	}
	slices.SortStableFunc(a.Findings, compareFindings)
	for _, f := range a.Findings {
		a.Counts[f.Severity]++
	}
	return a, ctx.Err()
}

func (e *Engine[M]) evaluate(ctx context.Context, c compiled[M], model M) (findings []Finding, out RuleOutcome) {
	out = RuleOutcome{Rule: c.meta.ID}
	defer func() {
		if r := recover(); r != nil {
			// A defective rule must not take down the whole inspection; it is
			// reported as failed and logged with its stack for the developer.
			e.log.ErrorContext(ctx, "assess rule panicked", slog.String("rule", string(c.meta.ID)),
				slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
			findings, out.State, out.Reason, out.Findings = nil, RuleFailed, ReasonPanic, 0
		}
	}()
	var misuse string
	raw, err := c.rule.Evaluate(ctx, Input[M]{Model: model, Params: Params{decl: c.params, misuse: &misuse}})
	switch {
	case misuse != "":
		e.log.ErrorContext(ctx, "assess rule read an undeclared parameter",
			slog.String("rule", string(c.meta.ID)), slog.String("param", misuse))
		out.State, out.Reason = RuleFailed, ReasonUndeclaredParam
		return nil, out
	case err != nil:
		e.log.WarnContext(ctx, "assess rule failed", slog.String("rule", string(c.meta.ID)), slog.Any("error", err))
		out.State, out.Reason = RuleFailed, ReasonError
		if ctx.Err() != nil {
			out.State, out.Reason = RuleSkipped, ReasonCancelled
		}
		return nil, out
	}
	findings = make([]Finding, 0, len(raw))
	for _, f := range raw {
		f.Rule = c.meta.ID
		f.Severity = cmp.Or(c.override, f.Severity, c.meta.Severity)
		f.Category = cmp.Or(f.Category, c.meta.Category)
		f.Title = cmp.Or(f.Title, c.meta.Title)
		if len(f.Verify) == 0 {
			f.Verify = slices.Clone(c.verify)
		}
		if len(f.References) == 0 {
			f.References = slices.Clone(c.meta.References)
		}
		if !f.Severity.Valid() || !f.Category.Valid() {
			out.State, out.Reason = RuleFailed, ReasonInvalidFinding
			return nil, out
		}
		findings = append(findings, f)
	}
	out.State, out.Findings = RuleRan, len(findings)
	return findings, out
}
