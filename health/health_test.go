package health_test

import (
	"fmt"
	"testing"

	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/health"
)

func snap(status evidence.Status, facts ...health.Fact) health.Snapshot {
	c, _ := evidence.NewCoverage(map[evidence.Dimension]evidence.Status{"bgp": status})
	return health.NewSnapshot(c, facts)
}

func TestGate(t *testing.T) {
	t.Parallel()
	before := snap(evidence.StatusComplete,
		health.Fact{Dimension: "bgp", Key: "192.0.2.1", State: "established"},
		health.Fact{Dimension: "bgp", Key: "192.0.2.2", State: "idle"})
	p := health.Policy{Dimensions: []evidence.Dimension{"bgp"}, Healthy: map[evidence.Dimension][]string{"bgp": {"established"}}}
	ok := health.Gate(before, snap(evidence.StatusComplete, health.Fact{Dimension: "bgp", Key: "192.0.2.1", State: "established"}), p)
	if !ok.Pass {
		t.Errorf("unhealthy fact counted: %+v", ok)
	}
	bad := health.Gate(before, snap(evidence.StatusComplete), p)
	if bad.Pass || len(bad.Regressions) != 1 {
		t.Errorf("lost neighbor passed: %+v", bad)
	}
	blind := health.Gate(before, snap(evidence.StatusFailed), p)
	if blind.Pass || len(blind.Unverified) != 1 {
		t.Error("gate passed without evidence")
	}
}

func ExampleGate() {
	cov, _ := evidence.NewCoverage(map[evidence.Dimension]evidence.Status{"links": evidence.StatusComplete})
	before := health.NewSnapshot(cov, []health.Fact{{Dimension: "links", Key: "Eth1/1", State: "up"}})
	after := health.NewSnapshot(cov, []health.Fact{{Dimension: "links", Key: "Eth1/1", State: "down"}})
	d := health.Gate(before, after, health.Policy{Dimensions: []evidence.Dimension{"links"}})
	fmt.Println(d.Pass, d.Regressions[0].Key, d.Regressions[0].After)
	// Output: false Eth1/1 down
}
