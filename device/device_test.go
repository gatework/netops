package device_test

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gatework/netops/device"
)

func dev(id, ip, name, role, site string) device.Device {
	return device.Device{ID: id, Mgmt: netip.MustParseAddr(ip), Name: name, Role: role, Site: site, Platform: "cisco_nxos"}
}

func TestNew_reportsRejections(t *testing.T) {
	t.Parallel()
	inv, rep := device.New([]device.Device{
		dev("", "192.0.2.2", "b", "", ""),
		dev("sw1", "192.0.2.1", "a", "", ""),
		dev("sw1", "192.0.2.9", "dup-id", "", ""),
		dev("sw3", "192.0.2.1", "dup-addr", "", ""),
		{ID: "x"},
	})
	if inv.Len() != 2 || rep.Accepted != 2 || rep.Offered != 5 {
		t.Fatalf("len %d report %+v", inv.Len(), rep)
	}
	reasons := []device.Reason{}
	for _, r := range rep.Rejected {
		reasons = append(reasons, r.Reason)
	}
	want := []device.Reason{device.ReasonDuplicateID, device.ReasonDuplicateAddress, device.ReasonInvalid}
	if diff := cmp.Diff(want, reasons); diff != "" {
		t.Errorf("reasons (-want +got):\n%s", diff)
	}
	if d, ok := inv.Lookup("192.0.2.2"); !ok || d.Name != "b" {
		t.Error("ID was not defaulted to the address")
	}
	all := inv.All()
	if all[0].ID != "sw1" {
		t.Errorf("not ordered by address: %v", all)
	}
}

func TestInventory_Merge(t *testing.T) {
	t.Parallel()
	a, _ := device.New([]device.Device{dev("sw1", "192.0.2.1", "old", "", ""), dev("sw2", "192.0.2.2", "keep", "", "")})
	b, _ := device.New([]device.Device{dev("sw1", "192.0.2.1", "new", "", "")})
	m := a.Merge(b)
	if d, _ := m.Lookup("sw1"); d.Name != "new" || m.Len() != 2 {
		t.Errorf("merge = %v", m.All())
	}
}

func TestSelector_Match(t *testing.T) {
	t.Parallel()
	inv, _ := device.New([]device.Device{
		dev("l1", "192.0.2.1", "dc1-leaf-01", "leaf", "dc1"),
		dev("l2", "192.0.2.2", "dc2-leaf-01", "leaf", "dc2"),
		dev("b1", "198.51.100.1", "dc1-border", "border", "dc1"),
	})
	tests := []struct {
		exprs []string
		want  []string
	}{
		{nil, []string{"l1", "l2", "b1"}},
		{[]string{"192.0.2.0/24"}, []string{"l1", "l2"}},
		{[]string{"role=leaf site=dc1"}, []string{"l1"}},
		{[]string{"role=leaf,role=border site=dc1"}, []string{"l1", "b1"}},
		{[]string{"DC1-*"}, []string{"l1", "b1"}},
		{[]string{"border", "192.0.2.2"}, []string{"l2", "b1"}},
	}
	for _, tc := range tests {
		s, err := device.ParseSelector(tc.exprs...)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range inv.Select(s) {
			got = append(got, d.ID)
		}
		slices.Sort(got)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%q selected %v, want %v", tc.exprs, got, want)
		}
	}
	for _, bad := range []string{"role=", "colour=red", "label:Bad=x", "[a"} {
		if _, err := device.ParseSelector(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzParseSelector(f *testing.F) {
	for _, s := range []string{"192.0.2.1", "role=leaf site=dc1", "label:rack=r1", "core-*"} {
		f.Add(s)
	}
	d := dev("x", "192.0.2.1", "core-1", "leaf", "dc1")
	f.Fuzz(func(t *testing.T, expr string) {
		s, err := device.ParseSelector(expr)
		if err != nil {
			return
		}
		_ = s.Match(d)
		again, err := device.ParseSelector(s.String())
		if err != nil {
			t.Fatalf("String() %q does not parse: %v", s.String(), err)
		}
		if again.Match(d) != s.Match(d) {
			t.Fatalf("String() changed the meaning of %q", expr)
		}
	})
}

func ExampleParseSelector() {
	s, _ := device.ParseSelector("role=leaf site=dc1")
	d := device.Device{ID: "l1", Mgmt: netip.MustParseAddr("192.0.2.1"), Role: "leaf", Site: "dc1"}
	fmt.Println(s.Match(d), s)
	// Output: true role=leaf,site=dc1
}
