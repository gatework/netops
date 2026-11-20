package apply

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gatework/netops/change"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/secret"
)

// SchemaVersion is the version of the Record document.
const SchemaVersion = 1

// ErrProtocol reports a request that breaks the contract.
var ErrProtocol = errors.New("apply: invalid request")

// Status is the state of an execution.
type Status string

// Execution statuses. Verified, Satisfied and DryRun are successes; Failed
// means nothing was sent; Uncertain means something may have been sent.
const (
	StatusPlanned   Status = "planned"
	StatusExecuting Status = "executing"
	StatusVerified  Status = "verified"
	StatusSatisfied Status = "satisfied"
	StatusDryRun    Status = "dry_run"
	StatusFailed    Status = "failed"
	StatusUncertain Status = "uncertain"
)

// Success reports whether s ends an execution successfully.
func (s Status) Success() bool {
	return s == StatusVerified || s == StatusSatisfied || s == StatusDryRun
}

// Final reports whether s ends an execution.
func (s Status) Final() bool {
	return s.Success() || s == StatusFailed || s == StatusUncertain
}

// Stage is where an execution failed.
type Stage string

// Execution stages.
const (
	StageJournal  Stage = "journal"
	StagePrecheck Stage = "precheck"
	StageWrite    Stage = "write"
	StageVerify   Stage = "verify"
	StageClose    Stage = "close"
)

// Mode selects execution or a dry run.
type Mode string

// Modes.
const (
	ModeExecute Mode = "execute"
	ModeDryRun  Mode = "dry_run"
)

// Identity names what is being executed.
type Identity struct {
	Device     device.Device   `json:"device"`
	PlanID     string          `json:"plan_id"`
	PlanDigest evidence.Digest `json:"plan_digest"`
	Baseline   evidence.Digest `json:"baseline"`
}

// Diagnostic is one classified failure cause.
type Diagnostic struct {
	Code  failure.Code `json:"code"`
	Stage Stage        `json:"stage"`
}

// Record is the journaled state of one execution.
type Record struct {
	Schema      int             `json:"schema"`
	Identity    Identity        `json:"identity"`
	Mode        Mode            `json:"mode"`
	Status      Status          `json:"status"`
	Commands    []string        `json:"commands"` // redacted
	Pending     *int            `json:"pending,omitempty"`
	Acked       int             `json:"acked"`
	Observed    evidence.Digest `json:"observed,omitempty"`
	Failure     failure.Code    `json:"failure,omitempty"`
	Stage       Stage           `json:"stage,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
	Started     time.Time       `json:"started"`
	Finished    time.Time       `json:"finished,omitzero"`
	// Unconfirmed is set when the final record could not be persisted; the
	// journal may show an earlier state.
	Unconfirmed bool `json:"unconfirmed,omitempty"`
}

// Sent reports whether any command may have reached the device.
func (r Record) Sent() bool { return r.Pending != nil || r.Acked > 0 }

func (r Record) clone() Record {
	r.Commands = slices.Clone(r.Commands)
	r.Diagnostics = slices.Clone(r.Diagnostics)
	if r.Pending != nil {
		p := *r.Pending
		r.Pending = &p
	}
	r.Identity.Device = r.Identity.Device.Clone()
	return r
}

// Plan is what Run executes. change.Plan satisfies it.
type Plan[C any] interface {
	Meta() change.Meta
	Target() device.Device
	Baseline() evidence.Digest
	Digest() (evidence.Digest, error)
	Commands() []change.Command
	Precheck(before C) (change.Readiness, error)
	Verify(before, after C) (evidence.Digest, error)
}

// Session is an open device session: it reads the model the plan is
// checked against and writes one command, returning once the device has
// accepted it.
type Session[C any] interface {
	Read(ctx context.Context) (C, error)
	Write(ctx context.Context, cmd change.Command) error
}

// Journal persists execution records. Write must be durable when it
// returns nil.
type Journal interface {
	Write(ctx context.Context, r Record) error
}

// DefaultPersistTimeout bounds journal writes after cancellation.
const DefaultPersistTimeout = 10 * time.Second

// Request is one execution.
type Request[C any] struct {
	Plan    Plan[C]
	Session Session[C]
	Journal Journal
	Mode    Mode
	// Redact removes secrets from journaled commands in addition to
	// masking sensitive ones.
	Redact secret.Redactor
	// Classify maps errors to codes; nil uses failure.Classify.
	Classify func(error) []failure.Code
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// PersistTimeout bounds journal writes that must survive cancellation;
	// zero uses DefaultPersistTimeout.
	PersistTimeout time.Duration
}

// Run executes the plan once. The returned record is the final state; the
// error is non-nil for every outcome but success, and wraps the cause.
func Run[C any](ctx context.Context, req Request[C]) (Record, error) {
	if req.Plan == nil || req.Session == nil || req.Journal == nil {
		return Record{}, ErrProtocol
	}
	r, err := newRun(req)
	if err != nil {
		return Record{}, err
	}
	return r.execute(ctx)
}

type run[C any] struct {
	req    Request[C]
	rec    Record
	stage  Stage
	before C
}

func newRun[C any](req Request[C]) (*run[C], error) {
	if req.Mode == "" {
		req.Mode = ModeExecute
	}
	if req.Mode != ModeExecute && req.Mode != ModeDryRun {
		return nil, fmt.Errorf("%w: mode %q", ErrProtocol, req.Mode)
	}
	if req.Classify == nil {
		req.Classify = failure.Classify
	}
	if req.Now == nil {
		req.Now = time.Now
	}
	if req.PersistTimeout <= 0 {
		req.PersistTimeout = DefaultPersistTimeout
	}
	digest, err := req.Plan.Digest()
	if err != nil {
		return nil, fmt.Errorf("%w: plan digest: %w", ErrProtocol, err)
	}
	cmds := req.Plan.Commands()
	texts := make([]string, len(cmds))
	for i, c := range cmds {
		texts[i] = req.Redact.Redact(c.Display())
	}
	return &run[C]{req: req, rec: Record{
		Schema: SchemaVersion,
		Identity: Identity{
			Device: req.Plan.Target(), PlanID: req.Plan.Meta().ID,
			PlanDigest: digest, Baseline: req.Plan.Baseline(),
		},
		Mode: req.Mode, Status: StatusPlanned, Commands: texts, Started: req.Now().UTC(),
	}}, nil
}

func (r *run[C]) execute(ctx context.Context) (Record, error) {
	r.stage = StageJournal
	if err := r.persist(ctx); err != nil {
		return r.fail(ctx, err)
	}
	r.stage = StagePrecheck
	before, err := r.req.Session.Read(ctx)
	if err != nil {
		return r.fail(ctx, err)
	}
	r.before = before
	readiness, err := r.req.Plan.Precheck(before)
	switch {
	case err != nil:
		return r.fail(ctx, err)
	case readiness == change.Satisfied:
		return r.finish(ctx, StatusSatisfied)
	case r.req.Mode == ModeDryRun:
		return r.finish(ctx, StatusDryRun)
	}
	for i, cmd := range r.req.Plan.Commands() {
		if err := r.write(ctx, i, cmd); err != nil {
			return r.fail(ctx, err)
		}
	}
	r.stage = StageVerify
	after, err := r.req.Session.Read(ctx)
	if err != nil {
		return r.fail(ctx, err)
	}
	observed, err := r.req.Plan.Verify(r.before, after)
	if err != nil {
		return r.fail(ctx, err)
	}
	r.rec.Observed = observed
	return r.finish(ctx, StatusVerified)
}

// write persists the intent, sends the command and persists the ack.
func (r *run[C]) write(ctx context.Context, i int, cmd change.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.stage = StageJournal
	r.rec.Status = StatusExecuting
	r.rec.Pending = &i
	if err := r.persist(ctx); err != nil {
		// The command was not sent. Earlier acknowledged commands, if any,
		// still make the outcome uncertain.
		r.rec.Pending = nil
		return err
	}
	r.stage = StageWrite
	if err := r.req.Session.Write(ctx, cmd); err != nil {
		return err
	}
	r.rec.Pending = nil
	r.rec.Acked = i + 1
	r.stage = StageJournal
	return r.persistDetached(ctx)
}

func (r *run[C]) finish(ctx context.Context, status Status) (Record, error) {
	r.rec.Status = status
	r.rec.Finished = r.req.Now().UTC()
	r.stage = StageJournal
	if err := r.persistDetached(ctx); err != nil {
		r.rec.Unconfirmed = true
		return r.rec.clone(), fmt.Errorf("apply: persist final record: %w", err)
	}
	return r.rec.clone(), nil
}

// fail records the failure. A failure after any intent was persisted is
// uncertain.
func (r *run[C]) fail(ctx context.Context, cause error) (Record, error) {
	r.rec.Status = StatusFailed
	if r.rec.Sent() {
		r.rec.Status = StatusUncertain
	}
	r.rec.Stage = r.stage
	codes := r.req.Classify(cause)
	if errors.Is(cause, change.ErrDrifted) {
		codes = append([]failure.Code{failure.CodeDrifted}, codes...)
	}
	for _, c := range codes {
		d := Diagnostic{Code: c, Stage: r.stage}
		if !slices.Contains(r.rec.Diagnostics, d) {
			r.rec.Diagnostics = append(r.rec.Diagnostics, d)
		}
	}
	if len(codes) > 0 {
		r.rec.Failure = codes[0]
	}
	r.rec.Finished = r.req.Now().UTC()
	if err := r.persistDetached(ctx); err != nil {
		r.rec.Unconfirmed = true
		return r.rec.clone(), errors.Join(fmt.Errorf("apply: %s at %s: %w", r.rec.Status, r.rec.Stage, cause), err)
	}
	return r.rec.clone(), fmt.Errorf("apply: %s at %s: %w", r.rec.Status, r.rec.Stage, cause)
}

func (r *run[C]) persist(ctx context.Context) error {
	if err := r.req.Journal.Write(ctx, r.rec.clone()); err != nil {
		return failure.Wrap(failure.CodeStorage, "journal", err)
	}
	return nil
}

// persistDetached persists even after cancellation, within a bounded time:
// a record of what was sent must not be lost because the operator pressed
// Ctrl-C.
func (r *run[C]) persistDetached(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.req.PersistTimeout)
	defer cancel()
	return r.persist(pctx)
}

// Settle folds a session close failure into a finished record: a close
// failure after anything was sent makes success uncertain. It returns the
// record to persist again, and whether it changed.
func Settle(rec Record, closeErr error) (Record, bool) {
	if closeErr == nil {
		return rec, false
	}
	rec = rec.clone()
	rec.Diagnostics = append(rec.Diagnostics, Diagnostic{Code: failure.Primary(closeErr), Stage: StageClose})
	if rec.Status == StatusVerified && rec.Sent() {
		rec.Status = StatusUncertain
		rec.Stage = StageClose
		rec.Failure = failure.Primary(closeErr)
	}
	return rec, true
}
