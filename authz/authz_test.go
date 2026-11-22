package authz_test

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"github.com/gatework/netops/authz"
	"github.com/gatework/netops/device"
)

const policy = `
roles:
  - {name: noc, permissions: [view, collect, backup]}
  - {name: dc1-change, permissions: [plan, deploy, rollback], scope: ["site=dc1"]}
  - {name: cab, permissions: [approve]}
  - {name: root, permissions: [admin]}
bindings:
  alice: [noc, dc1-change]
  bob: [cab]
  carol: [root]
`

func TestPolicy_Authorize(t *testing.T) {
	t.Parallel()
	p, err := authz.Decode([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	dc1 := device.Device{ID: "leaf1", Mgmt: netip.MustParseAddr("192.0.2.1"), Site: "dc1"}
	dc2 := device.Device{ID: "leaf9", Mgmt: netip.MustParseAddr("192.0.2.9"), Site: "dc2"}
	tests := []struct {
		actor  string
		perm   authz.Permission
		d      device.Device
		reason string
	}{
		{"alice", authz.PermDeploy, dc1, ""},
		{"alice", authz.PermDeploy, dc2, "out_of_scope"},
		{"alice", authz.PermApprove, dc1, "no_permission"},
		{"bob", authz.PermApprove, dc2, ""},
		{"carol", authz.PermRollback, dc2, ""},
		{"mallory", authz.PermView, dc1, "no_role"},
	}
	for _, tc := range tests {
		err := p.Authorize(tc.actor, tc.perm, tc.d)
		var d *authz.DeniedError
		switch {
		case tc.reason == "" && err != nil:
			t.Errorf("%s %s %s: %v", tc.actor, tc.perm, tc.d.ID, err)
		case tc.reason != "" && (!errors.As(err, &d) || d.Reason != tc.reason || !errors.Is(err, authz.ErrDenied)):
			t.Errorf("%s %s %s: %v, want %s", tc.actor, tc.perm, tc.d.ID, err, tc.reason)
		}
	}
	allowed, denied := p.Filter("alice", authz.PermDeploy, []device.Device{dc1, dc2})
	if len(allowed) != 1 || len(denied) != 1 {
		t.Error("filter")
	}
	for _, bad := range []string{"roles: [{name: x, permissions: [fly]}]", "bindings: {a: [ghost]}", "roles: [{name: x}, {name: x}]"} {
		if _, err := authz.Decode([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if (authz.Policy{}).Permits("anyone", authz.PermView) {
		t.Error("empty policy permits")
	}
}

func ExamplePolicy_Authorize() {
	p, _ := authz.Decode([]byte("roles: [{name: ops, permissions: [deploy], scope: ['site=dc1']}]\nbindings: {alice: [ops]}\n"))
	d := device.Device{ID: "leaf9", Mgmt: netip.MustParseAddr("192.0.2.9"), Site: "dc2"}
	fmt.Println(p.Authorize("alice", authz.PermDeploy, d))
	// Output: authz: alice may not deploy leaf9 (out_of_scope)
}
