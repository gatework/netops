package describe_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gatework/netops/change"
	"github.com/gatework/netops/describe"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/neighbor"
)

const comwareLLDP = `System Name Local Interface Chassis ID Port ID
core-1 GE1/0/1 000f-e25d-ee91 GigabitEthernet1/0/24
core-2 GE1/0/2 000f-e25d-ee92 GE1/0/24
ap-7 GE1/0/3 000f-e25d-ee93 GE1/0/1
ap-8 GE1/0/3 000f-e25d-ee94 GE1/0/1
-- GE1/0/4 000f-e25d-ee95 GE1/0/1
lab GE1/0/9 000f-e25d-ee99 GE1/0/1`

const comwareConfig = `#
 sysname access-1
#
interface GigabitEthernet1/0/1
 description To_core-1_GE1/0/24
 port link-mode bridge
#
interface GigabitEthernet1/0/2
 port link-mode bridge
#
interface GigabitEthernet1/0/3
 description uplink
#
interface GigabitEthernet1/0/4
#
interface GigabitEthernet1/0/5
 description printer
#
return`

func model(t *testing.T) describe.Model {
	t.Helper()
	p, err := describe.Lookup("h3c")
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Read(t.Context(), comwareLLDP, comwareConfig)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAudit(t *testing.T) {
	t.Parallel()
	a, err := describe.Policy{}.Audit(model(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []describe.Comparison{
		{Interface: "GigabitEthernet1/0/1", Neighbor: "core-1", Port: "GigabitEthernet1/0/24", Current: "To_core-1_GE1/0/24", Expected: "To_core-1_Gi1/0/24", Status: describe.StatusMatches},
		{Interface: "GigabitEthernet1/0/2", Neighbor: "core-2", Port: "GE1/0/24", Expected: "To_core-2_Gi1/0/24", Status: describe.StatusMissing},
		{Interface: "GigabitEthernet1/0/3", Neighbor: "ap-7", Port: "GE1/0/1", Current: "uplink", Status: describe.StatusUnverified, Reason: describe.ReasonAmbiguous},
		{Interface: "GigabitEthernet1/0/4", Neighbor: "--", Port: "GE1/0/1", Status: describe.StatusUnverified, Reason: neighbor.IssueUnknownSystem},
		{Interface: "GE1/0/9", Neighbor: "lab", Port: "GE1/0/1", Status: describe.StatusUnverified, Reason: describe.ReasonNoPort},
	}
	if diff := cmp.Diff(want, a.Comparisons); diff != "" {
		t.Errorf("audit (-want +got):\n%s", diff)
	}
	if len(a.Unverified()) != 3 {
		t.Errorf("unverified = %d", len(a.Unverified()))
	}
}

func TestChangePlanVerify(t *testing.T) {
	t.Parallel()
	before := model(t)
	a, _ := describe.Policy{}.Audit(before)
	c, ok := a.Change()
	if !ok {
		t.Fatal("no change")
	}
	meta := change.Meta{ID: "desc-1", Created: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)}
	dev := device.Device{ID: "access-1", Mgmt: netip.MustParseAddr("192.0.2.10"), Platform: "h3c"}
	p, err := change.NewPlan(meta, dev, before, change.Change[describe.Model](c), describe.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	cmds := func(cs []change.Command) string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Text
		}
		return strings.Join(out, "\n")
	}
	if got, want := cmds(p.Script().Commands), "system-view\ninterface GigabitEthernet1/0/2\ndescription To_core-2_Gi1/0/24\nquit\nreturn"; got != want {
		t.Errorf("commands:\n%s\nwant:\n%s", got, want)
	}
	if got, want := cmds(p.Script().Rollback), "system-view\ninterface GigabitEthernet1/0/2\nundo description\nquit\nreturn"; got != want {
		t.Errorf("rollback:\n%s\nwant:\n%s", got, want)
	}
	if r, err := p.Precheck(before); err != nil || r != change.Ready {
		t.Fatalf("precheck: %v %v", r, err)
	}

	after := model(t)
	after.Ports["gigabitethernet1/0/2"] = describe.Port{Name: "GigabitEthernet1/0/2", Description: "To_core-2_Gi1/0/24"}
	if _, err := p.Verify(before, after); err != nil {
		t.Fatalf("verify: %v", err)
	}
	after.Ports["gigabitethernet1/0/5"] = describe.Port{Name: "GigabitEthernet1/0/5", Description: "scanner"}
	if _, err := p.Verify(before, after); !errors.Is(err, change.ErrUnverified) {
		t.Fatalf("collateral change not caught: %v", err)
	}
}

func TestChangeValidate(t *testing.T) {
	t.Parallel()
	tests := map[string]describe.Change{
		"read-only platform": {Platform: "huawei", Items: []describe.Item{{Interface: "GE0/0/1", Description: "x"}}},
		"unknown platform":   {Platform: "acme", Items: []describe.Item{{Interface: "GE0/0/1", Description: "x"}}},
		"empty":              {Platform: "h3c"},
		"injection":          {Platform: "h3c", Items: []describe.Item{{Interface: "GE1/0/1", Description: "x\nreboot"}}},
		"unsafe interface":   {Platform: "h3c", Items: []describe.Item{{Interface: "GE1/0/1 ;", Description: "x"}}},
		"duplicate":          {Platform: "h3c", Items: []describe.Item{{Interface: "GE1/0/1", Description: "a"}, {Interface: "Gi1/0/1", Description: "b"}}},
	}
	for name, c := range tests {
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEquivalent(t *testing.T) {
	t.Parallel()
	if !describe.Equivalent("to_core_GE1/0/1", "To_core_Gi1/0/1") || describe.Equivalent("To_core_GE1/0/1", "To_core_GE1/0/2") {
		t.Error("Equivalent")
	}
}

func TestJunosRead(t *testing.T) {
	t.Parallel()
	p, _ := describe.Lookup("juniper")
	cfg := "set interfaces ge-0/0/0 description \"To core 1\"\nset interfaces ge-0/0/0 unit 0 family inet\nset interfaces ge-0/0/1 unit 0 description uplink"
	lldp := "Local Interface: ge-0/0/0\nChassis ID: 0011\nPort ID: et-0/0/1\nSystem name: core-1\n"
	m, err := p.Read(t.Context(), lldp, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m.Ports["ge-0/0/0"].Description != "To core 1" || m.Ports["ge-0/0/1.0"].Description != "uplink" || len(m.Neighbors) != 1 {
		t.Errorf("model = %+v", m)
	}
	if p.Writable() {
		t.Error("junos descriptions must stay read-only without dialogue evidence")
	}
}
