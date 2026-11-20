package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gatework/netops/store/disk"

	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/governance"
	"github.com/gatework/netops/store"
)

const lockWait = 30 * time.Second

// ErrNotFound reports a missing plan, approval set or execution.
var ErrNotFound = errors.New("journal: not found")

// Event is one audit line.
type Event struct {
	At     time.Time       `json:"at"`
	Kind   string          `json:"kind"` // "plan", "approve", "execute"
	Plan   string          `json:"plan"`
	Digest evidence.Digest `json:"digest,omitempty"`
	Device string          `json:"device,omitempty"`
	Actor  string          `json:"actor,omitempty"`
	Status string          `json:"status,omitempty"`
	Code   string          `json:"code,omitempty"`
}

// Journal is a change journal root. It is safe for concurrent use within a
// process; plan and approval writers also lock across processes.
type Journal struct {
	st  *store.Store
	now func() time.Time

	mu sync.Mutex // serializes audit appends
}

// Open opens or creates a journal root. now may be nil for time.Now.
func Open(root string, now func() time.Time) (*Journal, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Journal{st: st, now: now}, nil
}

// SavePlan stores an envelope and audits it. A plan ID can be stored once;
// saving the same envelope again is a no-op, a different one is an error.
func (j *Journal) SavePlan(ctx context.Context, e change.Envelope, actor string) error {
	rel := "plans/" + store.Name(e.Meta.ID) + ".json"
	lock, err := j.st.Lock(ctx, rel, lockWait)
	if err != nil {
		return err
	}
	defer lock.Close()
	var old change.Envelope
	switch err := j.st.ReadJSON(ctx, rel, 0, &old); {
	case err == nil && old.Digest == e.Digest:
		return nil
	case err == nil:
		return fmt.Errorf("journal: plan %s already exists with another digest", e.Meta.ID)
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	if _, err := j.st.WriteJSON(ctx, rel, e); err != nil {
		return err
	}
	return j.audit(Event{Kind: "plan", Plan: e.Meta.ID, Digest: e.Digest, Actor: actor})
}

// LoadPlan returns the stored envelope bytes of a plan, for Codec.Decode.
func (j *Journal) LoadPlan(ctx context.Context, planID string) ([]byte, error) {
	body, err := j.st.Read(ctx, "plans/"+store.Name(planID)+".json", 0)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%w: plan %s", ErrNotFound, planID)
	}
	return body, err
}

// Plans returns the stored plan file names, sorted.
func (j *Journal) Plans() ([]string, error) {
	names, err := j.st.List("plans")
	if err != nil {
		return nil, err
	}
	out := names[:0]
	for _, n := range names {
		if base, ok := strings.CutSuffix(n, ".json"); ok {
			out = append(out, base)
		}
	}
	return out, nil
}

// Approve records an approval of the plan. Approving the same plan twice by
// the same approver keeps the first approval.
func (j *Journal) Approve(ctx context.Context, planID string, a governance.Approval) error {
	rel := "approvals/" + store.Name(planID) + ".json"
	lock, err := j.st.Lock(ctx, rel, lockWait)
	if err != nil {
		return err
	}
	defer lock.Close()
	list, err := j.Approvals(ctx, planID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if slices.ContainsFunc(list, func(x governance.Approval) bool { return x.By == a.By && x.Plan == a.Plan }) {
		return nil
	}
	list = append(list, a)
	if _, err := j.st.WriteJSON(ctx, rel, list); err != nil {
		return err
	}
	return j.audit(Event{Kind: "approve", Plan: planID, Digest: a.Plan, Actor: a.By})
}

// Approvals returns the approvals of a plan.
func (j *Journal) Approvals(ctx context.Context, planID string) ([]governance.Approval, error) {
	var list []governance.Approval
	err := j.st.ReadJSON(ctx, "approvals/"+store.Name(planID)+".json", 0, &list)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%w: approvals of %s", ErrNotFound, planID)
	}
	return list, err
}

// Execution returns the apply.Journal of one plan on one device.
func (j *Journal) Execution(planID, deviceID string) apply.Journal {
	return execution{j: j, plan: planID, device: deviceID}
}

type execution struct {
	j            *Journal
	plan, device string
}

func (e execution) Write(ctx context.Context, r apply.Record) error {
	if _, err := e.j.st.WriteJSON(ctx, executionPath(e.plan, e.device), r); err != nil {
		return err
	}
	if !r.Status.Final() {
		return nil
	}
	return e.j.audit(Event{
		Kind: "execute", Plan: e.plan, Digest: r.Identity.PlanDigest, Device: e.device,
		Status: string(r.Status), Code: string(r.Failure),
	})
}

// Record returns the last persisted record of a plan on a device.
func (j *Journal) Record(ctx context.Context, planID, deviceID string) (apply.Record, error) {
	var r apply.Record
	err := j.st.ReadJSON(ctx, executionPath(planID, deviceID), 0, &r)
	if errors.Is(err, store.ErrNotFound) {
		return r, fmt.Errorf("%w: execution of %s on %s", ErrNotFound, planID, deviceID)
	}
	return r, err
}

// Records returns every device record of a plan.
func (j *Journal) Records(ctx context.Context, planID string) ([]apply.Record, error) {
	dir := "executions/" + store.Name(planID)
	names, err := j.st.List(dir)
	if err != nil {
		return nil, err
	}
	var out []apply.Record
	for _, n := range names {
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		var r apply.Record
		if err := j.st.ReadJSON(ctx, dir+"/"+n, 0, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func executionPath(planID, deviceID string) string {
	return "executions/" + store.Name(planID) + "/" + store.Name(deviceID) + ".json"
}

// Audit returns the audit events, oldest first, bounded to 64 MiB.
func (j *Journal) Audit(ctx context.Context) ([]Event, error) {
	body, err := j.st.Read(ctx, "audit.jsonl", 0)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Event
	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return out, fmt.Errorf("journal: audit line %d is corrupt", len(out)+1)
		}
		out = append(out, e)
	}
	return out, nil
}

// audit appends one event line and syncs it.
func (j *Journal) audit(e Event) error {
	e.At = j.now().UTC()
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("journal: encode audit: %w", err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := disk.OpenAppend(filepath.Join(j.st.Root(), "audit.jsonl"))
	if err != nil {
		return fmt.Errorf("journal: open audit: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("journal: append audit: %w", err)
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return fmt.Errorf("journal: sync audit: %w", err)
	}
	return nil
}
