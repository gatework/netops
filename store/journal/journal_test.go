package journal_test

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/governance"
	"github.com/gatework/netops/internal/fake"
	"github.com/gatework/netops/store/journal"
)

func TestJournal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	j, err := journal.Open(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	p, _ := change.NewPlan(change.Meta{ID: "chg-1", Created: now},
		device.Device{ID: "sw1", Mgmt: netip.MustParseAddr("192.0.2.1")}, fake.Config{},
		change.Change[fake.Config](fake.AddLine{Line: "x"}), fake.Fingerprint)
	e, _ := p.Envelope()
	if err := j.SavePlan(ctx, e, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := j.SavePlan(ctx, e, "alice"); err != nil {
		t.Error("idempotent save failed")
	}
	e2 := e
	e2.Digest = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := j.SavePlan(ctx, e2, "mallory"); err == nil {
		t.Error("plan replaced")
	}
	body, err := j.LoadPlan(ctx, "chg-1")
	codec, _ := change.NewCodec(fake.Fingerprint, change.DecodeAs[fake.Config, fake.AddLine]("fake.add"))
	if _, derr := codec.Decode(body); err != nil || derr != nil {
		t.Fatalf("load %v decode %v", err, derr)
	}
	a := governance.Approval{Plan: e.Digest, By: "bob", At: now}
	_ = j.Approve(ctx, "chg-1", a)
	_ = j.Approve(ctx, "chg-1", a)
	if list, _ := j.Approvals(ctx, "chg-1"); len(list) != 1 {
		t.Errorf("approvals %v", list)
	}
	w := j.Execution("chg-1", "sw1")
	_ = w.Write(ctx, apply.Record{Status: apply.StatusPlanned})
	_ = w.Write(ctx, apply.Record{Status: apply.StatusVerified})
	if r, _ := j.Record(ctx, "chg-1", "sw1"); r.Status != apply.StatusVerified {
		t.Errorf("record %+v", r)
	}
	if _, err := j.Record(ctx, "chg-2", "sw1"); !errors.Is(err, journal.ErrNotFound) {
		t.Errorf("missing record: %v", err)
	}
	events, _ := j.Audit(ctx)
	kinds := []string{}
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if len(kinds) != 3 || kinds[0] != "plan" || kinds[1] != "approve" || kinds[2] != "execute" {
		t.Errorf("audit %v", kinds)
	}
}
