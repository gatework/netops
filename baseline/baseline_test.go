package baseline_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/gatework/netops/baseline"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/drift"
	"github.com/gatework/netops/parse"
)

const profile = `
default:
  settings:
    ntp.servers: {values: [192.0.2.1]}
  rules:
    - id: no-telnet
      title: telnet disabled
      forbid: ['^telnet server enable']
    - id: iface-desc
      title: interfaces described
      section: ['^interface ']
      require: ['^description ']
roles:
  leaf:
    settings:
      ntp.servers: {values: [192.0.2.9], exclusive: true}
`

func TestProfile(t *testing.T) {
	t.Parallel()
	p, err := baseline.Decode([]byte(profile))
	if err != nil {
		t.Fatal(err)
	}
	leaf := device.Device{ID: "l1", Mgmt: netip.MustParseAddr("192.0.2.10"), Role: "leaf"}
	r := p.Resolve(leaf)
	if got := r.Settings["ntp.servers"]; !got.Exclusive || got.Values[0] != "192.0.2.9" {
		t.Errorf("role layer not applied: %+v", got)
	}
	doc, _ := config.Parse(t.Context(), "telnet server enable\ninterface Eth1\n shutdown\ninterface Eth2\n description up\n", config.Cisco(), parse.Limits{})
	vs := r.CheckConfig(doc)
	if len(vs) != 2 || vs[0].Rule != "iface-desc" || vs[0].Kind != drift.KindMissing || vs[1].Kind != drift.KindUnexpected {
		t.Errorf("violations %+v", vs)
	}
	if _, err := baseline.Decode([]byte("default:\n  rules:\n    - id: x\n      forbid: ['(']\n")); err == nil {
		t.Error("bad pattern accepted")
	}
}

func ExampleDerive() {
	l := baseline.Derive(map[string]map[string][]string{
		"sw1": {"ntp.servers": {"192.0.2.1"}},
		"sw2": {"ntp.servers": {"192.0.2.1"}},
		"sw3": {"ntp.servers": {"198.51.100.7"}},
	}, 0.6)
	fmt.Println(l.Settings["ntp.servers"].Values)
	// Output: [192.0.2.1]
}
