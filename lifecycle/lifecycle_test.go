package lifecycle_test

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/lifecycle"
)

func TestVersion_Compare(t *testing.T) {
	t.Parallel()
	less := [][2]string{
		{"9.3(9)", "9.3(10)"},
		{"17.9.4", "17.9.4a"},
		{"21.4R3", "21.4R3-S5"},
		{"7.1.070, Release 6728P05", "7.1.070, Release 6728P06"},
		{"7.1.064, Release 5474", "7.1.070, Release 5474"},
		{"15.2(4)E9", "15.2(7)E2"},
	}
	for _, p := range less {
		a, b := lifecycle.ParseVersion(p[0]), lifecycle.ParseVersion(p[1])
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("%s should be < %s", p[0], p[1])
		}
	}
	if !lifecycle.ParseVersion("9.3(09)").Equal(lifecycle.ParseVersion("9.3(9)")) {
		t.Error("leading zeros differ")
	}
}

const policy = `
support_warning: 2160h
rules:
  - name: nxos-leaf
    match: ["platform=cisco_nxos"]
    target: "10.3(4a)"
    minimum: "9.3(10)"
    blocked: ["10.2(3)"]
    advisory: "cisco-sa-example"
    end_of_support: "2027-01-31"
  - name: comware
    match: ["platform=h3c_wireless"]
    minimum: "7.1.064, Release 5474"
    end_of_sale: "2025-01-01"
`

func TestPolicy_Evaluate(t *testing.T) {
	t.Parallel()
	p, err := lifecycle.Decode([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	mk := func(platform, version string) device.Device {
		return device.Device{ID: version, Mgmt: netip.MustParseAddr("192.0.2.1"), Platform: platform, Version: version}
	}
	tests := []struct {
		d    device.Device
		want []lifecycle.Status
	}{
		{mk("cisco_nxos", "10.3(4a)"), []lifecycle.Status{lifecycle.StatusEndOfSupportSoon}},
		{mk("cisco_nxos", "9.3(8)"), []lifecycle.Status{lifecycle.StatusBelowMinimum, lifecycle.StatusEndOfSupportSoon, lifecycle.StatusNotTarget}},
		{mk("cisco_nxos", "10.2(3)"), []lifecycle.Status{lifecycle.StatusBlocked, lifecycle.StatusEndOfSupportSoon, lifecycle.StatusNotTarget}},
		{mk("h3c_wireless", "7.1.070, Release 5474"), []lifecycle.Status{lifecycle.StatusEndOfSale}},
		{mk("h3c_wireless", ""), []lifecycle.Status{lifecycle.StatusEndOfSale, lifecycle.StatusUnknown}},
		{mk("juniper_junos", "21.4R3"), nil},
	}
	for _, tc := range tests {
		v := p.Evaluate(tc.d, now)
		if !slices.Equal(v.Statuses, tc.want) {
			t.Errorf("%s %s: %v, want %v", tc.d.Platform, tc.d.Version, v.Statuses, tc.want)
		}
	}
	if f := p.Evaluate(mk("cisco_nxos", "10.2(3)"), now).Finding(); f.Rule != "lifecycle.blocked" || len(f.References) != 1 {
		t.Errorf("finding %+v", f)
	}
	if _, err := lifecycle.Decode([]byte("rules: [{name: x, target: '9.0', minimum: '9.3'}]")); err == nil {
		t.Error("target below minimum accepted")
	}
}

func ExampleParseVersion() {
	a := lifecycle.ParseVersion("21.4R3")
	b := lifecycle.ParseVersion("21.4R3-S5")
	fmt.Println(a.Compare(b))
	// Output: -1
}
