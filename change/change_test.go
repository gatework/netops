package change_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gatework/netops/change"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/internal/fake"
)

var (
	sw1  = device.Device{ID: "sw1", Mgmt: netip.MustParseAddr("192.0.2.1"), Platform: "test"}
	meta = change.Meta{ID: "chg-1", Author: "alice", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
)

func plan(t *testing.T, model fake.Config, line string) *change.Plan[fake.Config] {
	t.Helper()
	p, err := change.NewPlan(meta, sw1, model, change.Change[fake.Config](fake.AddLine{Line: line}), fake.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewPlan(t *testing.T) {
	t.Parallel()
	base := fake.Config{"hostname sw1"}
	if _, err := change.NewPlan(meta, sw1, base, change.Change[fake.Config](fake.AddLine{Line: "hostname sw1"}), fake.Fingerprint); !errors.Is(err, change.ErrSatisfied) {
		t.Errorf("satisfied: %v", err)
	}
	if _, err := change.NewPlan(meta, sw1, base, change.Change[fake.Config](fake.AddLine{Line: "forbidden x"}), fake.Fingerprint); !errors.Is(err, change.ErrIneligible) {
		t.Errorf("ineligible: %v", err)
	}
	if _, err := change.NewPlan(change.Meta{ID: "bad id"}, sw1, base, change.Change[fake.Config](fake.AddLine{Line: "x"}), fake.Fingerprint); !errors.Is(err, change.ErrInvalid) {
		t.Errorf("meta: %v", err)
	}
}

func TestPlan_lifecycle(t *testing.T) {
	t.Parallel()
	base := fake.Config{"hostname sw1"}
	p := plan(t, base, "ntp server 192.0.2.1")
	if r, err := p.Precheck(base); err != nil || r != change.Ready {
		t.Errorf("precheck = %v %v", r, err)
	}
	if r, _ := p.Precheck(fake.Config{"hostname sw1", "ntp server 192.0.2.1"}); r != change.Satisfied {
		t.Error("idempotent re-run not detected")
	}
	if _, err := p.Precheck(fake.Config{"hostname other"}); !errors.Is(err, change.ErrDrifted) {
		t.Errorf("drift: %v", err)
	}
	after := fake.Config{"hostname sw1", "ntp server 192.0.2.1"}
	obs, err := p.Verify(base, after)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Verify(base, fake.Config{"ntp server 192.0.2.1"}); !errors.Is(err, change.ErrUnverified) {
		t.Errorf("collateral change missed: %v", err)
	}
	inv, err := p.Inverse(change.Meta{ID: "chg-1-rb", Created: meta.Created}, obs)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Commands()[0].Text != "no ntp server 192.0.2.1" {
		t.Errorf("inverse commands %+v", inv.Commands())
	}
	if r, _ := inv.Precheck(after); r != change.Ready {
		t.Error("inverse not ready on the changed device")
	}
	if _, err := inv.Verify(after, base); err != nil {
		t.Errorf("inverse verify: %v", err)
	}
}

func TestCodec_roundTripAndTamper(t *testing.T) {
	t.Parallel()
	p := plan(t, fake.Config{}, "ntp server 192.0.2.1")
	e, err := p.Envelope()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(e)
	codec, err := change.NewCodec(fake.Fingerprint, change.DecodeAs[fake.Config, fake.AddLine]("fake.add"))
	if err != nil {
		t.Fatal(err)
	}
	back, err := codec.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	d1, _ := p.Digest()
	d2, _ := back.Digest()
	if d1 != d2 || back.Baseline() != p.Baseline() {
		t.Error("round trip changed the plan")
	}
	tampered := strings.Replace(string(body), "192.0.2.1", "192.0.2.66", 1)
	if _, err := codec.Decode([]byte(tampered)); !errors.Is(err, change.ErrInvalid) {
		t.Errorf("tampering not detected: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	p, _ := change.NewPlan(meta, sw1, fake.Config{}, change.Change[fake.Config](fake.AddLine{Line: "x"}), fake.Fingerprint)
	e, _ := p.Envelope()
	body, _ := json.Marshal(e)
	f.Add(body)
	codec, _ := change.NewCodec(fake.Fingerprint, change.DecodeAs[fake.Config, fake.AddLine]("fake.add"))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := codec.Decode(b)
		if err != nil {
			return
		}
		if _, err := p.Digest(); err != nil {
			t.Fatal(err)
		}
	})
}

func ExampleNewPlan() {
	running := fake.Config{"hostname sw1"}
	p, _ := change.NewPlan(meta, sw1, running, change.Change[fake.Config](fake.AddLine{Line: "ntp server 192.0.2.1"}), fake.Fingerprint)
	for _, c := range p.Script().Commands {
		fmt.Println("+", c.Text)
	}
	for _, c := range p.Script().Rollback {
		fmt.Println("rollback:", c.Text)
	}
	// Output:
	// + ntp server 192.0.2.1
	// rollback: no ntp server 192.0.2.1
}
