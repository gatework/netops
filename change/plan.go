package change

import (
	"fmt"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
)

// Readiness is the outcome of re-checking a plan against the device just
// before execution.
type Readiness int

// Readiness values.
const (
	// Ready: the device is as planned; execute.
	Ready Readiness = iota + 1
	// Satisfied: the device already realizes the change; skip writing.
	Satisfied
)

// String returns "ready" or "satisfied".
func (r Readiness) String() string {
	switch r {
	case Ready:
		return "ready"
	case Satisfied:
		return "satisfied"
	}
	return "unknown"
}

// Plan binds one change to one device and the model state it was planned
// against. It is immutable and safe for concurrent use.
type Plan[M any] struct {
	meta     Meta
	target   device.Device
	change   Change[M]
	fp       Fingerprint[M]
	baseline evidence.Digest
	script   Script
	// restore is set on an inverse plan: the digest the device must return
	// to.
	restore evidence.Digest
}

// NewPlan validates the change, refuses one that model already satisfies
// (ErrSatisfied) or that is not eligible (ErrIneligible), and renders it.
func NewPlan[M any](meta Meta, target device.Device, model M, c Change[M], fp Fingerprint[M]) (*Plan[M], error) {
	if c == nil || fp == nil {
		return nil, fmt.Errorf("%w: nil change or fingerprint", ErrInvalid)
	}
	if err := meta.validate(); err != nil {
		return nil, err
	}
	target = target.Normalize()
	if err := target.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !c.Kind().Valid() {
		return nil, fmt.Errorf("%w: change kind %q", ErrInvalid, c.Kind())
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if c.Satisfied(model) {
		return nil, ErrSatisfied
	}
	if err := c.Eligible(model); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIneligible, err)
	}
	script, err := c.Render(model)
	if err != nil {
		return nil, fmt.Errorf("%w: render: %w", ErrInvalid, err)
	}
	if err := script.validate(); err != nil {
		return nil, err
	}
	return &Plan[M]{meta: meta, target: target, change: c, fp: fp, baseline: fp(model), script: script.clone()}, nil
}

// Meta returns the audit metadata.
func (p *Plan[M]) Meta() Meta { return p.meta }

// Target returns the device.
func (p *Plan[M]) Target() device.Device { return p.target.Clone() }

// Change returns the change.
func (p *Plan[M]) Change() Change[M] { return p.change }

// Baseline returns the fingerprint of the model the plan was made against.
func (p *Plan[M]) Baseline() evidence.Digest { return p.baseline }

// Script returns a copy of the commands and rollback.
func (p *Plan[M]) Script() Script { return p.script.clone() }

// Commands returns a copy of the commands to send.
func (p *Plan[M]) Commands() []Command { return p.script.clone().Commands }

// Precheck re-checks the device just before execution. It returns Ready
// when the model is unchanged since planning, Satisfied when the model
// already realizes the change, and ErrDrifted otherwise.
func (p *Plan[M]) Precheck(before M) (Readiness, error) {
	if p.satisfied(before) {
		return Satisfied, nil
	}
	if p.fp(before) != p.baseline {
		return 0, ErrDrifted
	}
	return Ready, nil
}

// Verify checks the model read after writing: the change must be
// satisfied and, for a Preserving change, nothing else may have changed.
// It returns the fingerprint of after.
func (p *Plan[M]) Verify(before, after M) (evidence.Digest, error) {
	done := p.satisfied(after)
	if eff, ok := p.change.(Effect[M]); ok && p.restore == "" {
		done = eff.Effective(before, after)
	}
	if !done {
		return "", ErrUnverified
	}
	if pres, ok := p.change.(Preserving[M]); ok && p.restore == "" {
		if err := pres.Preserved(before, after); err != nil {
			return "", fmt.Errorf("%w: collateral change: %w", ErrUnverified, err)
		}
	}
	return p.fp(after), nil
}

func (p *Plan[M]) satisfied(m M) bool {
	if p.restore != "" {
		return p.fp(m) == p.restore
	}
	return p.change.Satisfied(m)
}

// Inverse returns the plan that undoes p after it was executed and the
// device was observed at the given fingerprint. The inverse sends p's
// rollback commands, refuses to run if the device moved away from
// observed, and verifies that the device returns to p's baseline.
func (p *Plan[M]) Inverse(meta Meta, observed evidence.Digest) (*Plan[M], error) {
	if len(p.script.Rollback) == 0 {
		return nil, ErrNoRollback
	}
	if err := meta.validate(); err != nil {
		return nil, err
	}
	if !observed.Valid() {
		return nil, fmt.Errorf("%w: observed digest", ErrInvalid)
	}
	return &Plan[M]{
		meta: meta, target: p.target, change: p.change, fp: p.fp, baseline: observed,
		script:  Script{Commands: p.script.Rollback, Rollback: p.script.Commands},
		restore: p.baseline,
	}, nil
}

// Inverted reports whether p is the inverse of another plan.
func (p *Plan[M]) Inverted() bool { return p.restore != "" }
