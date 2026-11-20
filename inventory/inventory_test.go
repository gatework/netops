package inventory_test

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/inventory"
)

const file = `
defaults: {platform: h3c_wireless, site: hq, labels: {tier: prod}}
devices:
  - {id: ac1, name: AC1, mgmt: 192.0.2.1, role: ac}
  - {name: AC2, mgmt: 192.0.2.2, site: branch, labels: {rack: r2}}
  - {name: AC3, mgmt: 192.0.2.1}
`

func TestLoad(t *testing.T) {
	t.Parallel()
	devices, err := inventory.DecodeFile([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	extra := inventory.Static{Label: "override", Records: []device.Device{{ID: "ac1", Name: "AC1-new", Mgmt: netip.MustParseAddr("192.0.2.1")}}}
	inv, reports, _, err := inventory.Load(t.Context(), inventory.Static{Label: "file", Records: devices}, extra)
	if err != nil {
		t.Fatal(err)
	}
	if reports[0].Dropped["duplicate_address"] != 1 {
		t.Errorf("duplicate not reported: %+v", reports[0])
	}
	if d, _ := inv.Lookup("ac1"); d.Name != "AC1-new" {
		t.Errorf("later source did not win: %+v", d)
	}
	if d, _ := inv.ByAddr(netip.MustParseAddr("192.0.2.2")); d.Site != "branch" || d.Platform != "h3c_wireless" || d.Labels["tier"] != "prod" || d.Labels["rack"] != "r2" {
		t.Errorf("defaults not applied: %+v", d)
	}
	if _, err := inventory.DecodeFile([]byte("devices: [{label: x}]")); !errors.Is(err, inventory.ErrInvalid) {
		t.Errorf("unknown key: %v", err)
	}
}

func TestNetdiscoConfig_Validate(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]inventory.NetdiscoConfig{
		"plain http":  {URL: "http://nd.example"},
		"user in url": {URL: "https://u:p@nd.example"},
		"no query":    {Source: inventory.NetdiscoPostgres},
		"bad cache":   {URL: "https://nd.example", CacheMode: "sometimes"},
		"bad rule":    {URL: "https://nd.example", DeviceRules: []inventory.DeviceRule{{Vendor: "h3c"}}},
	} {
		if err := c.Validate(); !errors.Is(err, inventory.ErrInvalid) {
			t.Errorf("%s accepted", name)
		}
	}
	if err := (inventory.NetdiscoConfig{URL: "https://nd.example"}).Validate(); err != nil {
		t.Error(err)
	}
}

func ExampleDecodeFile() {
	ds, _ := inventory.DecodeFile([]byte("defaults: {platform: cisco_nxos}\ndevices:\n  - {name: leaf1, mgmt: 192.0.2.11, role: leaf}\n"))
	fmt.Println(ds[0].Name, ds[0].Mgmt, ds[0].Platform, ds[0].Role)
	// Output: leaf1 192.0.2.11 cisco_nxos leaf
}
