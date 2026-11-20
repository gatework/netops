package governance_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/gatework/netops/governance"
)

func TestWindow_Contains(t *testing.T) {
	t.Parallel()
	w, err := governance.ParseWindow("Sat 23:00-02:00", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	sat := time.Date(2026, 11, 21, 23, 30, 0, 0, time.UTC) // Saturday
	cases := map[time.Time]bool{
		sat:                      true,
		sat.Add(2 * time.Hour):   true,  // Sunday 01:30, window started Saturday
		sat.Add(3 * time.Hour):   false, // Sunday 02:30
		sat.Add(-24 * time.Hour): false,
	}
	for at, want := range cases {
		if w.Contains(at) != want {
			t.Errorf("%s: %v", at, !want)
		}
	}
	for _, bad := range []string{"25:00-01:00", "Sat", "Xyz 01:00-02:00", "01:00-01:00"} {
		if _, err := governance.ParseWindow(bad, nil); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGate(t *testing.T) {
	t.Parallel()
	w, _ := governance.ParseWindow("01:00-05:00", time.UTC)
	noon := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	p := governance.Policy{Windows: []governance.Window{w}, RequireTicket: true, Approvals: map[governance.Class]int{governance.ClassNormal: 1}}
	r := governance.Request{Plan: "p", Class: governance.ClassNormal, Author: "alice", Now: noon,
		Approvals: []governance.Approval{{Plan: "p", By: "alice"}}}
	d := governance.Gate(p, r)
	want := []governance.Reason{governance.ReasonOutsideWindow, governance.ReasonNoTicket, governance.ReasonSelfApproval, governance.ReasonApprovals}
	if d.Allowed || !slices.Equal(d.Refusals, want) {
		t.Errorf("refusals %v", d.Refusals)
	}
	r.Class, r.Ticket, r.Justification = governance.ClassEmergency, "INC-1", "outage"
	r.Approvals = []governance.Approval{{Plan: "p", By: "bob"}}
	p.Approvals[governance.ClassEmergency] = 1
	if d := governance.Gate(p, r); !d.Allowed || !slices.Contains(d.Bypassed, governance.ReasonWindowBypassed) {
		t.Errorf("emergency %+v", d)
	}
}

func ExampleGate() {
	d := governance.Gate(governance.Policy{RequireTicket: true}, governance.Request{Class: governance.ClassNormal, Now: time.Now()})
	fmt.Println(d.Allowed, d.Refusals)
	// Output: false [no_ticket]
}
