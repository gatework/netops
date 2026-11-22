package ops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/batch"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/governance"
	"github.com/gatework/netops/health"
	"github.com/gatework/netops/lease"
	"github.com/gatework/netops/rollout"
	"github.com/gatework/netops/secret"
)

// Governance is the change-control check of a deployment.
type Governance struct {
	Policy  governance.Policy
	Request governance.Request // Plan is filled with the change-set digest
}

// DeployRequest executes plans across a fleet.
type DeployRequest[C any] struct {
	Plans  []*change.Plan[C]
	Dialer Dialer
	// Read reads the model a plan is checked against, in the open session.
	Read func(ctx context.Context, s Session, p *change.Plan[C]) (C, error)
	// Journal returns the execution journal of a plan on its device.
	Journal func(p *change.Plan[C]) apply.Journal
	// Health, when set, snapshots operational state before and after the
	// change in the same session; Policy gates on it.
	Health       func(ctx context.Context, s Session, d device.Device) (health.Snapshot, error)
	HealthPolicy health.Policy
	// Governance, when set, must allow the change set before anything runs.
	Governance *Governance
	Strategy   rollout.Strategy
	// Waves, when set, is a reviewed schedule used instead of scheduling
	// with Strategy (whose failure budget still applies). It must name every
	// plan's device exactly once.
	Waves []rollout.Wave
	Mode  apply.Mode
	// Checkpoint takes a platform checkpoint before writing, when the
	// platform supports it, so a failed health gate can restore it.
	Checkpoint bool
	// Rollback undoes a verified change whose health gate failed: through
	// the checkpoint when one was taken, otherwise through the plan's
	// inverse.
	Rollback bool
	// Save persists the configuration after a verified, healthy change.
	Save   bool
	Redact secret.Redactor
	Events batch.Events[Outcome]
	// Authorize, when set, must allow every planned device before anything
	// runs; one refusal refuses the whole change set.
	Authorize func(device.Device) error
	// Leases, when set, leases each wave's devices before any session opens
	// and releases them after the wave, so other tools cannot change them
	// meanwhile. A conflict fails the wave's devices with session_busy.
	Leases *lease.Manager
	// Holder identifies this deployment in leases; Purpose defaults to the
	// change-set digest.
	Holder lease.Holder
	// LeaseTTL bounds a lease if the process dies; zero means one hour.
	LeaseTTL time.Duration
	// Notify publishes change.executed, change.failed, change.uncertain,
	// change.rolled_back and lease.conflict.
	Notify Notify
	Now    func() time.Time
}

// Outcome is the result of one device.
type Outcome struct {
	Device     device.Device    `json:"device"`
	Plan       string           `json:"plan"`
	Record     apply.Record     `json:"record"`
	Health     *health.Decision `json:"health,omitempty"`
	Checkpoint string           `json:"checkpoint,omitempty"`
	RolledBack bool             `json:"rolled_back,omitempty"`
	Saved      bool             `json:"saved,omitempty"`
	// Codes classify failures outside apply: health, checkpoint, rollback,
	// save.
	Codes []failure.Code `json:"codes,omitempty"`
}

// Succeeded reports whether the device ended healthy in the intended
// state (or, in a dry run, ready).
func (o Outcome) Succeeded() bool {
	return o.Record.Status.Success() && !o.RolledBack && len(o.Codes) == 0 && (o.Health == nil || o.Health.Pass)
}

// WaveReport is the outcomes of one wave.
type WaveReport struct {
	Wave     rollout.Wave `json:"wave"`
	Outcomes []Outcome    `json:"outcomes"`
}

// DeployReport is the result of a deployment.
type DeployReport struct {
	ChangeSet evidence.Digest      `json:"change_set"`
	Gate      *governance.Decision `json:"gate,omitempty"`
	Waves     []WaveReport         `json:"waves"`
	// Pending lists devices not attempted because the rollout stopped.
	Pending []device.Device `json:"pending,omitempty"`
	// Denied lists the authorization refusals that stopped the change set.
	Denied      []string `json:"denied,omitempty"`
	Stopped     bool     `json:"stopped"`
	Interrupted bool     `json:"interrupted"`
}

// Succeeded reports whether every device succeeded.
func (r DeployReport) Succeeded() bool {
	if r.Stopped || r.Interrupted || len(r.Pending) > 0 || (r.Gate != nil && !r.Gate.Allowed) {
		return false
	}
	for _, w := range r.Waves {
		if slices.ContainsFunc(w.Outcomes, func(o Outcome) bool { return !o.Succeeded() }) {
			return false
		}
	}
	return true
}

// ErrDenied reports a change set refused by governance.
var ErrDenied = errors.New("ops: change denied by governance")

// ErrUnauthorized reports a change set touching a device the actor may
// not change.
var ErrUnauthorized = errors.New("ops: change not authorized")

// ChangeSet returns the digest identifying a set of plans, the subject of
// approvals: the digest of the sorted plan digests.
func ChangeSet[C any](plans []*change.Plan[C]) (evidence.Digest, error) {
	digests := make([]string, len(plans))
	for i, p := range plans {
		d, err := p.Digest()
		if err != nil {
			return "", err
		}
		digests[i] = string(d)
	}
	return evidence.NewHasher("netops/change-set/v1").Set("plans", digests).Sum(), nil
}

// Deploy checks governance, schedules the plans into waves and executes
// each wave, stopping by the strategy's failure budget. Each device is
// handled in a single session: health before, checkpoint, apply, health
// after and gate, rollback or save.
func Deploy[C any](ctx context.Context, r DeployRequest[C]) (DeployReport, error) {
	if r.Dialer == nil || r.Read == nil || r.Journal == nil {
		return DeployReport{}, fmt.Errorf("ops: deploy: missing dependency")
	}
	byID := map[string]*change.Plan[C]{}
	targets := make([]device.Device, 0, len(r.Plans))
	for _, p := range r.Plans {
		t := p.Target()
		if byID[t.ID] != nil {
			return DeployReport{}, fmt.Errorf("ops: deploy: two plans for %s", t.ID)
		}
		byID[t.ID] = p
		targets = append(targets, t)
	}
	set, err := ChangeSet(r.Plans)
	if err != nil {
		return DeployReport{}, fmt.Errorf("ops: deploy: %w", err)
	}
	rep := DeployReport{ChangeSet: set}
	if r.Authorize != nil {
		for _, t := range targets {
			if err := r.Authorize(t); err != nil {
				rep.Denied = append(rep.Denied, err.Error())
			}
		}
		if len(rep.Denied) > 0 {
			return rep, ErrUnauthorized
		}
	}
	if r.Holder.Purpose == "" {
		r.Holder.Purpose = set.Short()
	}
	if r.Governance != nil {
		req := r.Governance.Request
		req.Plan, req.Devices = set, len(r.Plans)
		if req.Now.IsZero() {
			req.Now = clock(r.Now)()
		}
		d := governance.Gate(r.Governance.Policy, req)
		rep.Gate = &d
		if !d.Allowed {
			return rep, ErrDenied
		}
	}
	waves := r.Waves
	if len(waves) == 0 {
		if waves, err = rollout.Schedule(targets, r.Strategy); err != nil {
			return rep, fmt.Errorf("ops: deploy: %w", err)
		}
	} else if err := scheduled(waves, byID); err != nil {
		return rep, err
	}
	return r.run(ctx, rep, waves, byID)
}

func (r DeployRequest[C]) run(ctx context.Context, rep DeployReport, waves []rollout.Wave, byID map[string]*change.Plan[C]) (DeployReport, error) {
	tracker := rollout.NewTracker(r.Strategy)
	for i, w := range waves {
		if ctx.Err() != nil || tracker.Stopped() {
			for _, rest := range waves[i:] {
				rep.Pending = append(rep.Pending, rest.Devices...)
			}
			break
		}
		leased, held := r.lease(ctx, w, byID)
		if held != nil {
			rep.Waves = append(rep.Waves, *held)
			tracker.Finish(len(held.Outcomes))
			continue
		}
		res, err := batch.Run(ctx, w.Devices, batch.Options[Outcome]{
			Workers: len(w.Devices), Events: r.Events, Now: r.Now,
			Task: func(ctx context.Context, d device.Device) (Outcome, error) {
				o := r.device(ctx, byID[d.ID])
				if !o.Succeeded() {
					return o, failure.New(primary(o), "ops: deploy")
				}
				return o, nil
			},
		})
		wr := WaveReport{Wave: w}
		failed := 0
		for _, x := range res.Results {
			if x.State == batch.StateSkipped {
				rep.Pending = append(rep.Pending, x.Device)
				continue
			}
			wr.Outcomes = append(wr.Outcomes, x.Value)
			if !x.Value.Succeeded() {
				failed++
			}
		}
		if r.Leases != nil {
			if rerr := r.Leases.Release(context.WithoutCancel(ctx), leased); rerr != nil && r.Notify.OnError != nil {
				r.Notify.OnError(rerr)
			}
		}
		r.announce(ctx, wr.Outcomes)
		rep.Waves = append(rep.Waves, wr)
		tracker.Finish(failed)
		if err != nil && ctx.Err() == nil {
			return rep, fmt.Errorf("ops: deploy: %w", err)
		}
	}
	rep.Stopped = tracker.Stopped()
	rep.Interrupted = ctx.Err() != nil
	if rep.Interrupted {
		return rep, ctx.Err()
	}
	return rep, nil
}

func primary(o Outcome) failure.Code {
	switch {
	case o.Record.Failure != failure.CodeUnset:
		return o.Record.Failure
	case len(o.Codes) > 0:
		return o.Codes[0]
	case o.Health != nil && !o.Health.Pass:
		return failure.CodeUnconfirmed
	}
	return failure.CodeInternal
}

// device runs the per-device pipeline in one session.
func (r DeployRequest[C]) device(ctx context.Context, p *change.Plan[C]) Outcome {
	d := p.Target()
	o := Outcome{Device: d, Plan: p.Meta().ID}
	journal := r.Journal(p)
	opErr, closeErr := r.Dialer.Open(ctx, d, func(ctx context.Context, s Session) error {
		return r.session(ctx, s, p, journal, &o)
	})
	if o.Record.Status == "" {
		// The session never reached apply: record the failure durably.
		o.Record = apply.Record{
			Schema: apply.SchemaVersion, Identity: apply.Identity{Device: d, PlanID: p.Meta().ID, Baseline: p.Baseline()},
			Mode: r.mode(), Status: apply.StatusFailed, Failure: failure.Primary(opErr), Stage: apply.StageJournal,
			Started: clock(r.Now)().UTC(), Finished: clock(r.Now)().UTC(),
		}
		if pd, err := p.Digest(); err == nil {
			o.Record.Identity.PlanDigest = pd
		}
		if opErr != nil || closeErr != nil {
			_ = journal.Write(context.WithoutCancel(ctx), o.Record)
		}
	} else if opErr != nil && o.Record.Status.Success() && len(o.Codes) == 0 {
		o.Codes = append(o.Codes, failure.Primary(opErr))
	}
	if rec, changed := apply.Settle(o.Record, closeErr); changed {
		o.Record = rec
		_ = journal.Write(context.WithoutCancel(ctx), rec)
	}
	return o
}

func (r DeployRequest[C]) mode() apply.Mode {
	if r.Mode == "" {
		return apply.ModeExecute
	}
	return r.Mode
}

func (r DeployRequest[C]) session(ctx context.Context, s Session, p *change.Plan[C], journal apply.Journal, o *Outcome) error {
	d := p.Target()
	var before health.Snapshot
	if r.Health != nil {
		var err error
		if before, err = r.Health(ctx, s, d); err != nil {
			o.Codes = append(o.Codes, failure.Primary(err))
			return err
		}
	}
	execute := r.mode() == apply.ModeExecute
	if execute && r.Checkpoint && s.CanCheckpoint() {
		name := checkpointName(p.Meta().ID)
		if err := s.Checkpoint(ctx, name); err != nil {
			o.Codes = append(o.Codes, failure.Primary(err))
			return err
		}
		o.Checkpoint = name
	}
	read := func(ctx context.Context) (C, error) { return r.Read(ctx, s, p) }
	rec, err := apply.Run(ctx, apply.Request[C]{
		Plan: p, Session: sessionApplier[C]{read: read, s: s}, Journal: journal,
		Mode: r.mode(), Redact: r.Redact, Now: r.Now,
	})
	o.Record = rec
	if err != nil || rec.Status != apply.StatusVerified {
		return nil //nolint:nilerr // the apply outcome is in the record; the session itself is fine
	}
	if r.Health != nil {
		after, err := r.Health(ctx, s, d)
		if err != nil {
			o.Codes = append(o.Codes, failure.Primary(err))
			return nil
		}
		gate := health.Gate(before, after, r.HealthPolicy)
		o.Health = &gate
		if !gate.Pass {
			if r.Rollback {
				r.rollback(ctx, s, p, rec, read, o)
			}
			return nil
		}
	}
	if r.Save {
		if err := s.Save(ctx); err != nil {
			o.Codes = append(o.Codes, failure.Primary(err))
			return nil
		}
		o.Saved = true
	}
	return nil
}

// rollback undoes a verified change through the checkpoint, or else the
// inverse plan executed with apply semantics.
func (r DeployRequest[C]) rollback(ctx context.Context, s Session, p *change.Plan[C], rec apply.Record, read func(context.Context) (C, error), o *Outcome) {
	if o.Checkpoint != "" {
		if err := s.Rollback(ctx, o.Checkpoint); err != nil {
			o.Codes = append(o.Codes, failure.Primary(err))
			return
		}
		o.RolledBack = true
		return
	}
	meta := p.Meta()
	meta.ID += "-rollback"
	meta.Created = clock(r.Now)().UTC()
	meta.Reason = "health gate failed"
	inverse, err := p.Inverse(meta, rec.Observed)
	if err != nil {
		o.Codes = append(o.Codes, failure.Primary(failure.Wrap(failure.CodeUnsupported, "ops: rollback", err)))
		return
	}
	irec, err := apply.Run(ctx, apply.Request[C]{
		Plan: inverse, Session: sessionApplier[C]{read: read, s: s}, Journal: r.Journal(inverse),
		Mode: apply.ModeExecute, Redact: r.Redact, Now: r.Now,
	})
	if err != nil || !irec.Status.Success() {
		o.Codes = append(o.Codes, failure.CodeUnconfirmed)
		return
	}
	o.RolledBack = true
}

func checkpointName(planID string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, planID)
	return "netops_" + name
}

type sessionApplier[C any] struct {
	read func(context.Context) (C, error)
	s    Session
}

func (a sessionApplier[C]) Read(ctx context.Context) (C, error) { return a.read(ctx) }

func (a sessionApplier[C]) Write(ctx context.Context, cmd change.Command) error {
	return a.s.Write(ctx, cmd)
}

// RollbackRequest undoes one executed plan with its inverse.
type RollbackRequest[C any] struct {
	Plan     *change.Plan[C]
	Observed evidence.Digest // the fingerprint recorded after execution
	Meta     change.Meta
	Dialer   Dialer
	Read     func(ctx context.Context, s Session, p *change.Plan[C]) (C, error)
	Journal  apply.Journal
	Mode     apply.Mode
	Redact   secret.Redactor
	Now      func() time.Time
}

// Rollback executes the inverse of an executed plan in one session.
func Rollback[C any](ctx context.Context, r RollbackRequest[C]) (apply.Record, error) {
	inverse, err := r.Plan.Inverse(r.Meta, r.Observed)
	if err != nil {
		return apply.Record{}, fmt.Errorf("ops: rollback: %w", err)
	}
	d := inverse.Target()
	var rec apply.Record
	var runErr error
	opErr, closeErr := r.Dialer.Open(ctx, d, func(ctx context.Context, s Session) error {
		rec, runErr = apply.Run(ctx, apply.Request[C]{
			Plan: inverse, Journal: r.Journal, Mode: r.Mode, Redact: r.Redact, Now: r.Now,
			Session: sessionApplier[C]{read: func(ctx context.Context) (C, error) { return r.Read(ctx, s, inverse) }, s: s},
		})
		return nil
	})
	if settled, changed := apply.Settle(rec, closeErr); changed && rec.Status != "" {
		rec = settled
		_ = r.Journal.Write(context.WithoutCancel(ctx), rec)
	}
	if err := errors.Join(runErr, opErr); err != nil {
		return rec, fmt.Errorf("ops: rollback: %w", err)
	}
	return rec, nil
}

// scheduled checks that waves name every planned device exactly once.
func scheduled[C any](waves []rollout.Wave, byID map[string]*change.Plan[C]) error {
	seen := map[string]bool{}
	for _, w := range waves {
		for _, d := range w.Devices {
			if byID[d.ID] == nil || seen[d.ID] {
				return fmt.Errorf("ops: deploy: wave %d names %s without a plan or twice", w.Index, d.ID)
			}
			seen[d.ID] = true
		}
	}
	if len(seen) != len(byID) {
		return fmt.Errorf("ops: deploy: waves leave %d planned devices out", len(byID)-len(seen))
	}
	return nil
}

// lease takes the wave's leases. On a conflict it returns the wave with
// every device failed as busy, and nothing is opened.
func (r DeployRequest[C]) lease(ctx context.Context, w rollout.Wave, byID map[string]*change.Plan[C]) (lease.Set, *WaveReport) {
	if r.Leases == nil {
		return lease.Set{}, nil
	}
	ids := make([]string, len(w.Devices))
	for i, d := range w.Devices {
		ids[i] = d.ID
	}
	ttl := r.LeaseTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	set, err := r.Leases.Acquire(ctx, ids, r.Holder, ttl)
	if err == nil {
		return set, nil
	}
	now := clock(r.Now)().UTC()
	wr := &WaveReport{Wave: w}
	for _, d := range w.Devices {
		p := byID[d.ID]
		o := Outcome{Device: d, Plan: p.Meta().ID, Codes: []failure.Code{failure.CodeBusy}, Record: apply.Record{
			Schema: apply.SchemaVersion, Identity: apply.Identity{Device: d, PlanID: p.Meta().ID, Baseline: p.Baseline()},
			Mode: r.mode(), Status: apply.StatusFailed, Failure: failure.CodeBusy, Stage: apply.StageJournal,
			Started: now, Finished: now,
		}}
		wr.Outcomes = append(wr.Outcomes, o)
	}
	summary := "devices of wave " + fmt.Sprint(w.Index) + " are leased by another change"
	var fields []evidence.Field
	if held, ok := errors.AsType[*lease.HeldError](err); ok {
		summary = held.Lease.Device + " is leased by " + held.Lease.Holder.String()
		fields = event.Fields("holder", held.Lease.Holder.String(), "until", held.Lease.Expires.Format(time.RFC3339))
	}
	r.Notify.emit(ctx, event.KindLeaseConflict, assess.SeverityMedium, now, w.Devices[0], summary, fields...)
	return lease.Set{}, wr
}

// announce publishes the outcome of each device that wrote or tried to.
func (r DeployRequest[C]) announce(ctx context.Context, outcomes []Outcome) {
	for _, o := range outcomes {
		at := o.Record.Finished
		if at.IsZero() {
			at = clock(r.Now)()
		}
		fields := event.Fields("plan", o.Plan, "status", string(o.Record.Status))
		switch {
		case o.RolledBack:
			r.Notify.emit(ctx, event.KindChangeRolledBack, assess.SeverityHigh, at, o.Device, "change "+o.Plan+" was rolled back after a failed health gate", fields...)
		case o.Record.Status == apply.StatusUncertain:
			r.Notify.emit(ctx, event.KindChangeUncertain, assess.SeverityCritical, at, o.Device, "change "+o.Plan+" may be partly applied; verify by hand", fields...)
		case o.Record.Status == apply.StatusFailed:
			r.Notify.emit(ctx, event.KindChangeFailed, assess.SeverityHigh, at, o.Device, "change "+o.Plan+" failed before writing", append(fields, event.Fields("code", string(primary(o)))...)...)
		case o.Record.Status == apply.StatusVerified && o.Succeeded():
			r.Notify.emit(ctx, event.KindChangeExecuted, assess.SeverityInfo, at, o.Device, "change "+o.Plan+" verified", fields...)
		case o.Record.Status == apply.StatusVerified:
			r.Notify.emit(ctx, event.KindChangeFailed, assess.SeverityHigh, at, o.Device, "change "+o.Plan+" verified but failed afterwards", append(fields, event.Fields("code", string(primary(o)))...)...)
		}
	}
}
