package evidence_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gatework/netops/evidence"
)

func TestNewObservation_invariants(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := evidence.NewObservation("ifaces", evidence.StatusFailed, at, []int{1}, nil, nil); err == nil {
		t.Error("failed observation with facts accepted")
	}
	if _, err := evidence.NewObservation[int]("Bad Dim", evidence.StatusComplete, at, nil, nil, nil); err == nil {
		t.Error("invalid dimension accepted")
	}
	o := evidence.Decoded("ifaces", at, []int{1}, []evidence.Issue{{Code: "unparsed_line"}})
	if o.Status != evidence.StatusPartial || o.Facts == nil || o.Sources == nil {
		t.Errorf("Decoded = %+v", o)
	}
}

func TestCoverage(t *testing.T) {
	t.Parallel()
	c := evidence.Cover(
		evidence.Decoded[int]("a", time.Time{}, nil, nil),
		evidence.Failed[int]("b", "timeout"),
	)
	c2 := c.With("c", evidence.StatusPartial)
	if c.Status("c") != evidence.StatusNotRequested || c2.Status("c") != evidence.StatusPartial {
		t.Error("With mutated the original")
	}
	if !c.Complete("a") || c.Complete("a", "b") || c.Overall() != evidence.StatusFailed {
		t.Errorf("coverage = %v", c.Dimensions())
	}
	body, err := json.Marshal(c2)
	if err != nil {
		t.Fatal(err)
	}
	var back evidence.Coverage
	if err := json.Unmarshal(body, &back); err != nil || back.Status("b") != evidence.StatusFailed {
		t.Errorf("round trip = %s %v", body, err)
	}
	if json.Unmarshal([]byte(`{"a":"maybe"}`), &back) == nil {
		t.Error("invalid status decoded")
	}
}

func TestHasher_fieldBoundaries(t *testing.T) {
	t.Parallel()
	a := evidence.NewHasher("t/v1").Strings("x", []string{"ab", "c"}).Sum()
	b := evidence.NewHasher("t/v1").Strings("x", []string{"a", "bc"}).Sum()
	c := evidence.NewHasher("t/v2").Strings("x", []string{"ab", "c"}).Sum()
	if a == b || a == c || !a.Valid() {
		t.Error("hasher collides across boundaries or domains")
	}
	s1 := evidence.NewHasher("t").Set("s", []string{"b", "a", "a"}).Sum()
	s2 := evidence.NewHasher("t").Set("s", []string{"a", "b"}).Sum()
	if s1 != s2 {
		t.Error("Set depends on order or duplicates")
	}
}

func FuzzHasher(f *testing.F) {
	f.Add("ab", "c", "a", "bc")
	f.Fuzz(func(t *testing.T, a1, a2, b1, b2 string) {
		ha := evidence.NewHasher("f").Strings("k", []string{a1, a2}).Sum()
		hb := evidence.NewHasher("f").Strings("k", []string{b1, b2}).Sum()
		if (a1 == b1 && a2 == b2) != (ha == hb) {
			t.Fatalf("hash equality disagrees with input equality: %q %q / %q %q", a1, a2, b1, b2)
		}
	})
}

func ExampleHasher() {
	d := evidence.NewHasher("acl/v1").String("name", "MGMT").Set("permits", []string{"10.0.0.0/8", "192.0.2.0/24"}).Sum()
	fmt.Println(d.Valid(), len(d.Short()))
	// Output: true 12
}
