package drift

import (
	"cmp"
	"maps"
	"slices"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"
)

// Kind is the shape of a deviation.
type Kind string

// Deviation kinds.
const (
	KindMissing    Kind = "missing"    // expected but absent
	KindUnexpected Kind = "unexpected" // present but not expected
	KindMismatch   Kind = "mismatch"   // present with another value
)

// Source is what the observation was compared with.
type Source string

// Deviation sources.
const (
	SourceBaseline  Source = "baseline"
	SourceConsensus Source = "consensus"
	SourcePrevious  Source = "previous"
	SourceIntent    Source = "intent"
)

// Deviation is one difference between expectation and observation.
// Expected and Actual are display strings, already masked of secrets.
type Deviation struct {
	Subject  assess.Subject       `json:"subject"`
	Path     string               `json:"path"`
	Kind     Kind                 `json:"kind"`
	Source   Source               `json:"source"`
	Expected string               `json:"expected,omitempty"`
	Actual   string               `json:"actual,omitempty"`
	Refs     []evidence.SourceRef `json:"refs,omitempty"`
}

// Finding converts the deviation into a finding of the drift category. The
// rule, severity and title are completed by the engine.
func (d Deviation) Finding(detail string) assess.Finding {
	return assess.Finding{
		Category: assess.CategoryDrift,
		Subject:  d.Subject,
		Detail:   detail,
		Expected: d.Expected,
		Actual:   d.Actual,
		Fields: []evidence.Field{
			{Key: "path", Value: d.Path},
			{Key: "kind", Value: string(d.Kind)},
			{Key: "source", Value: string(d.Source)},
		},
		Evidence: slices.Clone(d.Refs),
	}
}

// Expect compares one expected value with an observation. present reports
// whether the observation exists at all; render formats values for display
// and must mask secrets. It returns false when they agree.
func Expect[V comparable](src Source, subject assess.Subject, path string, expected V, actual V, present bool, render func(V) string) (Deviation, bool) {
	var zero V
	d := Deviation{Subject: subject, Path: path, Source: src}
	switch {
	case !present && expected != zero:
		d.Kind, d.Expected = KindMissing, render(expected)
	case !present:
		return Deviation{}, false
	case expected == zero && actual != zero:
		d.Kind, d.Actual = KindUnexpected, render(actual)
	case expected != actual:
		d.Kind, d.Expected, d.Actual = KindMismatch, render(expected), render(actual)
	default:
		return Deviation{}, false
	}
	return d, true
}

// Keys compares the expected and observed key sets of a collection, such
// as NTP servers, producing a missing deviation for each absent key and an
// unexpected one for each extra key when exclusive is set.
func Keys[K cmp.Ordered](src Source, subject assess.Subject, path string, expected, actual []K, exclusive bool, render func(K) string) []Deviation {
	have := map[K]bool{}
	for _, k := range actual {
		have[k] = true
	}
	want := map[K]bool{}
	var out []Deviation
	for _, k := range slices.Sorted(slices.Values(expected)) {
		if want[k] {
			continue
		}
		want[k] = true
		if !have[k] {
			out = append(out, Deviation{Subject: subject, Path: path, Kind: KindMissing, Source: src, Expected: render(k)})
		}
	}
	if exclusive {
		for _, k := range slices.Sorted(maps.Keys(have)) {
			if !want[k] {
				out = append(out, Deviation{Subject: subject, Path: path, Kind: KindUnexpected, Source: src, Actual: render(k)})
			}
		}
	}
	return out
}

// Member is one peer's value in a consensus.
type Member[V any] struct {
	ID    string
	Value V
}

// Agreement is the outcome of a consensus.
type Agreement[V any] struct {
	// Value is the majority value; valid only when Reached.
	Value V
	// Share is the majority's fraction of members.
	Share float64
	// Reached reports whether Share met the quorum.
	Reached bool
	// Count is the number of members holding the majority value.
	Count int
	// Tied reports that another value has as many members as the
	// majority, which was then chosen by key order only.
	Tied bool
	// Outliers are the members whose key differs from the majority, ordered
	// by ID.
	Outliers []Member[V]
}

// Consensus finds the majority among members, comparing values by key.
// Ties go to the smallest key; the majority's representative is the member
// with the smallest ID, so the result does not depend on input order.
// quorum is the minimum majority share in (0, 1].
func Consensus[V any, K cmp.Ordered](members []Member[V], key func(V) K, quorum float64) Agreement[V] {
	var a Agreement[V]
	if len(members) == 0 {
		return a
	}
	sorted := slices.Clone(members)
	slices.SortStableFunc(sorted, func(x, y Member[V]) int { return cmp.Compare(x.ID, y.ID) })
	counts := map[K]int{}
	for _, m := range sorted {
		counts[key(m.Value)]++
	}
	var best K
	bestN := -1
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		switch n := counts[k]; {
		case n > bestN:
			best, bestN, a.Tied = k, n, false
		case n == bestN:
			a.Tied = true
		}
	}
	a.Count = bestN
	a.Share = float64(bestN) / float64(len(sorted))
	a.Reached = a.Share >= quorum
	for _, m := range sorted {
		if key(m.Value) == best {
			a.Value = m.Value
			break
		}
	}
	for _, m := range sorted {
		if key(m.Value) != best {
			a.Outliers = append(a.Outliers, m)
		}
	}
	return a
}
