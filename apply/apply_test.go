package apply_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/internal/fake"
	"github.com/gatework/netops/secret"
)

type journal struct {
	mu      sync.Mutex
	records []apply.Record
	failAt  int // 1-based write that fails; 0 never
}

func (j *journal) Write(_ context.Context, r apply.Record) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.records)+1 == j.failAt {
		j.failAt = 0
		return errors.New("disk full")
	}
	j.records = append(j.records, r)
	return nil
}

func (j *journal) statuses() []apply.Status {
	var out []apply.Status
	for _, r := range j.records {
		if len(out) == 0 || out[len(out)-1] != r.Status {
			out = append(out, r.Status)
		}
	}
	return out
}

type session struct {
	cfg      fake.Config
	failRead int
	reads    int
	failCmd  int
	writes   []string
}

func (s *session) Read(context.Context) (fake.Config, error) {
	s.reads++
	if s.reads == s.failRead {
		return nil, failure.New(failure.CodeTimeout, "read")
	}
	return slices.Clone(s.cfg), nil
}

func (s *session) Write(_ context.Context, c change.Command) error {
	s.writes = append(s.writes, c.Text)
	if len(s.writes) == s.failCmd {
		return failure.New(failure.CodeClosed, "write")
	}
	s.cfg = append(s.cfg, c.Text)
	return nil
}

var created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newPlan(t *testing.T, base fake.Config, line string) *change.Plan[fake.Config] {
	t.Helper()
	p, err := change.NewPlan(change.Meta{ID: "p1", Created: created},
		device.Device{ID: "sw1", Mgmt: netip.MustParseAddr("192.0.2.1")}, base,
		change.Change[fake.Config](fake.AddLine{Line: line}), fake.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun(t *testing.T) {
	t.Parallel()
	base := fake.Config{"hostname sw1"}
	tests := []struct {
		name     string
		cfg      fake.Config
		mode     apply.Mode
		failRead int
		failCmd  int
		failJrnl int
		status   apply.Status
		code     failure.Code
		writes   int
	}{
		{name: "verified", cfg: base, status: apply.StatusVerified, writes: 1},
		{name: "already satisfied", cfg: fake.Config{"hostname sw1", "ntp 1"}, status: apply.StatusSatisfied},
		{name: "dry run writes nothing", cfg: base, mode: apply.ModeDryRun, status: apply.StatusDryRun},
		{name: "drift refused", cfg: fake.Config{"hostname other"}, status: apply.StatusFailed, code: failure.CodeDrifted},
		{name: "read failure before writing", cfg: base, failRead: 1, status: apply.StatusFailed, code: failure.CodeTimeout},
		{name: "write failure is uncertain", cfg: base, failCmd: 1, status: apply.StatusUncertain, code: failure.CodeClosed, writes: 1},
		{name: "verify read failure is uncertain", cfg: base, failRead: 2, status: apply.StatusUncertain, code: failure.CodeTimeout, writes: 1},
		{name: "planned record not persisted", cfg: base, failJrnl: 1, status: apply.StatusFailed, code: failure.CodeStorage},
		{name: "intent not persisted", cfg: base, failJrnl: 2, status: apply.StatusFailed, code: failure.CodeStorage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			j := &journal{failAt: tc.failJrnl}
			s := &session{cfg: tc.cfg, failRead: tc.failRead, failCmd: tc.failCmd}
			rec, err := apply.Run(t.Context(), apply.Request[fake.Config]{
				Plan: newPlan(t, base, "ntp 1"), Session: s, Journal: j, Mode: tc.mode,
				Now: func() time.Time { return created },
			})
			if rec.Status != tc.status || rec.Failure != tc.code || len(s.writes) != tc.writes {
				t.Errorf("status %s code %s writes %d, err %v", rec.Status, rec.Failure, len(s.writes), err)
			}
			if (err == nil) != rec.Status.Success() {
				t.Errorf("error %v disagrees with status %s", err, rec.Status)
			}
			last := j.records[len(j.records)-1]
			if last.Status != rec.Status {
				t.Errorf("journal ends at %s", last.Status)
			}
		})
	}
}

func TestRun_journalsIntentBeforeWriting(t *testing.T) {
	t.Parallel()
	j := &journal{}
	_, err := apply.Run(t.Context(), apply.Request[fake.Config]{
		Plan: newPlan(t, fake.Config{}, "ntp 1"), Session: &session{}, Journal: j,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []apply.Status{apply.StatusPlanned, apply.StatusExecuting, apply.StatusVerified}
	if got := j.statuses(); !slices.Equal(got, want) {
		t.Errorf("journal %v", got)
	}
	intent := j.records[1]
	if intent.Pending == nil || *intent.Pending != 0 || intent.Acked != 0 {
		t.Errorf("intent record %+v", intent)
	}
}

func TestRun_redactsCommands(t *testing.T) {
	t.Parallel()
	j := &journal{}
	_, _ = apply.Run(t.Context(), apply.Request[fake.Config]{
		Plan: newPlan(t, fake.Config{}, "snmp community s3cr3t-value"), Session: &session{}, Journal: j,
		Redact: secret.NewRedactor(0, secret.New("s3cr3t-value")),
	})
	for _, r := range j.records {
		if strings.Contains(strings.Join(r.Commands, " "), "s3cr3t") {
			t.Fatal("secret journaled")
		}
	}
}

func TestSettle(t *testing.T) {
	t.Parallel()
	rec := apply.Record{Status: apply.StatusVerified, Acked: 1}
	got, changed := apply.Settle(rec, failure.New(failure.CodeClosed, "close"))
	if !changed || got.Status != apply.StatusUncertain {
		t.Errorf("close failure after writes: %+v", got)
	}
}

func ExampleRun() {
	p, _ := change.NewPlan(change.Meta{ID: "p1", Created: created},
		device.Device{ID: "sw1", Mgmt: netip.MustParseAddr("192.0.2.1")}, fake.Config{},
		change.Change[fake.Config](fake.AddLine{Line: "ntp server 192.0.2.1"}), fake.Fingerprint)
	rec, err := apply.Run(context.Background(), apply.Request[fake.Config]{Plan: p, Session: &session{}, Journal: &journal{}})
	fmt.Println(rec.Status, rec.Acked, err)
	// Output: verified 1 <nil>
}
