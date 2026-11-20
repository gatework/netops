package neighbor_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gatework/netops/neighbor"
)

// legacy-neighbors.json holds the synthetic one-neighbor outputs of the
// retired net-connector topology package; every probe must reproduce the
// same adjacency.
func TestLegacyFixtures(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/legacy-neighbors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]string
	if err := json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	want := map[string]neighbor.Neighbor{
		"h3c":          {LocalPort: "GE1/0/1", System: "peer", Port: "GE1/0/2", ChassisID: "000f-e25d-ee91", Protocol: neighbor.LLDP},
		"h3c_wireless": {LocalPort: "XGE1/0/1", System: "peer", Port: "Port 12", ChassisID: "000f-e25d-ee91", Protocol: neighbor.LLDP},
		"cisco_ios":    {LocalPort: "GigabitEthernet1/0/1", System: "peer", Port: "GigabitEthernet1/0/2", Protocol: neighbor.CDP},
		"cisco_nxos":   {LocalPort: "Ethernet1/1", System: "peer", Port: "Ethernet1/2", Protocol: neighbor.CDP},
		"hillstone":    {LocalPort: "ethernet0/1", System: "peer", Port: "ethernet0/2", ChassisID: "7425.8ae4.4f4c", Protocol: neighbor.LLDP},
		"huawei":       {LocalPort: "Gigabitethernet 0/1/0", System: "peer", Port: "Gigabitethernet 0/1/1", Protocol: neighbor.LLDP},
		"juniper":      {LocalPort: "ge-0/0/0", System: "peer", Port: "ge-0/0/1", ChassisID: "001122334455", Protocol: neighbor.LLDP},
		"palo_alto":    {LocalPort: "ethernet1/1", System: "peer", Port: "Ethernet1/2", ChassisID: "001122334455", Protocol: neighbor.LLDP},
	}
	for platform, w := range want {
		p, ok := neighbor.Lookup(platform)
		if !ok {
			t.Errorf("%s: no probe", platform)
			continue
		}
		got, err := p.Parse(fixtures[platform])
		if err != nil {
			t.Errorf("%s: %v", platform, err)
			continue
		}
		if diff := cmp.Diff([]neighbor.Neighbor{w}, got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", platform, diff)
		}
	}
}

func TestParseRejectsUnaccountedRecords(t *testing.T) {
	t.Parallel()
	p, _ := neighbor.Lookup("h3c")
	tests := map[string]string{
		"no header":      "GE1/0/1 000f-e25d-ee91 GE1/0/2 peer\n",
		"malformed port": "Local Interface Chassis ID Port ID System Name\nnot-a-port 000f GE1/0/2 peer\n",
		"silent":         "",
	}
	for name, text := range tests {
		if _, err := p.Parse(text); !errors.Is(err, neighbor.ErrParse) {
			t.Errorf("%s: err = %v, want ErrParse", name, err)
		}
	}
	empty := "Local Interface Chassis ID Port ID System Name\n<sw1>\n"
	if got, err := p.Parse(empty); err != nil || len(got) != 0 {
		t.Errorf("empty table: %v %v", got, err)
	}
	ios, _ := neighbor.Lookup("cisco_ios")
	if _, err := ios.Parse("Total cdp entries displayed : 0\n"); err != nil {
		t.Errorf("ios empty: %v", err)
	}
	if _, err := ios.Parse("Device ID: a\nDevice ID: b\nInterface: Gi1/0/1, Port ID (outgoing port): Gi1/0/2\n"); !errors.Is(err, neighbor.ErrParse) {
		t.Errorf("ios half record: %v", err)
	}
}

func TestIssue(t *testing.T) {
	t.Parallel()
	tests := map[neighbor.Neighbor]string{
		{System: "peer", Port: "Gi1/0/1"}: "",
		{System: "--", Port: "Gi1/0/1"}:   neighbor.IssueUnknownSystem,
		{System: "peer", Port: "N/A"}:     neighbor.IssueUnknownPort,
		{System: "peer", Port: ""}:        neighbor.IssueUnknownPort,
	}
	for n, want := range tests {
		if got := n.Issue(); got != want {
			t.Errorf("%+v: %q, want %q", n, got, want)
		}
	}
}

// A Probe is shared by every device session of a run.
func TestParseConcurrently(t *testing.T) {
	t.Parallel()
	p, _ := neighbor.Lookup("cisco_ios")
	text := "Device ID: peer\nInterface: Gi1/0/1, Port ID (outgoing port): Gi1/0/2\n"
	errs := make(chan error, 16)
	for range cap(errs) {
		go func() {
			_, err := p.Parse(text)
			errs <- err
		}()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
