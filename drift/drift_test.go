package drift_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/drift"
)

func id(s string) string { return s }

func TestExpect(t *testing.T) {
	t.Parallel()
	subj := assess.NewSubject("device", "sw1")
	if _, ok := drift.Expect(drift.SourceBaseline, subj, "mtu", "9216", "9216", true, id); ok {
		t.Error("equal values deviate")
	}
	d, ok := drift.Expect(drift.SourceBaseline, subj, "mtu", "9216", "", false, id)
	if !ok || d.Kind != drift.KindMissing {
		t.Errorf("missing = %+v", d)
	}
	f := d.Finding("mtu missing")
	if f.Category != assess.CategoryDrift || len(f.Fields) != 3 {
		t.Errorf("finding lost the path: %+v", f)
	}
}

func TestConsensus_orderIndependent(t *testing.T) {
	t.Parallel()
	ms := []drift.Member[string]{{"c", "x"}, {"a", "y"}, {"b", "x"}, {"d", "y"}}
	first := drift.Consensus(ms, id, 0.5)
	slices.Reverse(ms)
	second := drift.Consensus(ms, id, 0.5)
	if first.Value != "x" || second.Value != "x" || !first.Reached {
		t.Errorf("tie not broken by key: %+v %+v", first, second)
	}
	if len(first.Outliers) != 2 || first.Outliers[0].ID != "a" {
		t.Errorf("outliers %+v", first.Outliers)
	}
}

func ExampleKeys() {
	ds := drift.Keys(drift.SourceBaseline, assess.NewSubject("device", "sw1"), "ntp.servers",
		[]string{"192.0.2.1", "192.0.2.2"}, []string{"192.0.2.1", "203.0.113.9"}, true, id)
	for _, d := range ds {
		fmt.Println(d.Kind, d.Expected+d.Actual)
	}
	// Output:
	// missing 192.0.2.2
	// unexpected 203.0.113.9
}
