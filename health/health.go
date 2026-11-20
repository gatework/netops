package health

import (
	"cmp"
	"maps"
	"slices"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"
)

// Fact is one keyed operational state.
type Fact struct {
	Dimension evidence.Dimension `json:"dimension"`
	Key       string             `json:"key"`
	State     string             `json:"state"`
}

// Snapshot is the operational state of one device at one time. It is an
// immutable value.
type Snapshot struct {
	coverage evidence.Coverage
	facts    map[evidence.Dimension]map[string]string
}

// NewSnapshot records facts under coverage. A later fact with the same
// dimension and key replaces an earlier one.
func NewSnapshot(coverage evidence.Coverage, facts []Fact) Snapshot {
	s := Snapshot{coverage: coverage, facts: map[evidence.Dimension]map[string]string{}}
	for _, f := range facts {
		m := s.facts[f.Dimension]
		if m == nil {
			m = map[string]string{}
			s.facts[f.Dimension] = m
		}
		m[f.Key] = f.State
	}
	return s
}

// Coverage returns the evidence coverage.
func (s Snapshot) Coverage() evidence.Coverage { return s.coverage }

// Facts returns the facts of dim ordered by key.
func (s Snapshot) Facts(dim evidence.Dimension) []Fact {
	m := s.facts[dim]
	out := make([]Fact, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, Fact{Dimension: dim, Key: k, State: m[k]})
	}
	return out
}

// Policy configures Gate.
type Policy struct {
	// Dimensions are gated; other dimensions are ignored.
	Dimensions []evidence.Dimension
	// Healthy lists the healthy states per dimension. A dimension without
	// an entry treats every state as healthy and any change as regression.
	Healthy map[evidence.Dimension][]string
	// Tolerance is the number of regressions accepted, for flapping links.
	Tolerance int
}

// Regression is one healthy fact that degraded. After is empty when the
// fact disappeared.
type Regression struct {
	Dimension evidence.Dimension `json:"dimension"`
	Key       string             `json:"key"`
	Before    string             `json:"before"`
	After     string             `json:"after,omitempty"`
}

// Decision is the outcome of a gate.
type Decision struct {
	Pass        bool                 `json:"pass"`
	Regressions []Regression         `json:"regressions,omitempty"`
	Unverified  []evidence.Dimension `json:"unverified,omitempty"`
}

// Gate compares before and after on the gated dimensions. It fails when a
// gated dimension is not complete on either side, or when regressions
// exceed the tolerance. Regressions are ordered by dimension and key.
func Gate(before, after Snapshot, p Policy) Decision {
	var d Decision
	for _, dim := range p.Dimensions {
		if !before.coverage.Status(dim).Complete() || !after.coverage.Status(dim).Complete() {
			d.Unverified = append(d.Unverified, dim)
			continue
		}
		healthy := func(state string) bool {
			states, ok := p.Healthy[dim]
			return !ok || slices.Contains(states, state)
		}
		b, a := before.facts[dim], after.facts[dim]
		for _, k := range slices.Sorted(maps.Keys(b)) {
			was := b[k]
			if !healthy(was) {
				continue
			}
			now, ok := a[k]
			_, explicit := p.Healthy[dim]
			switch {
			case !ok:
				d.Regressions = append(d.Regressions, Regression{Dimension: dim, Key: k, Before: was})
			case explicit && !healthy(now), !explicit && now != was:
				d.Regressions = append(d.Regressions, Regression{Dimension: dim, Key: k, Before: was, After: now})
			}
		}
	}
	slices.SortFunc(d.Regressions, func(x, y Regression) int {
		return cmp.Or(cmp.Compare(x.Dimension, y.Dimension), cmp.Compare(x.Key, y.Key))
	})
	d.Pass = len(d.Unverified) == 0 && len(d.Regressions) <= p.Tolerance
	return d
}

// Level is a coarse health rating.
type Level string

// Health levels.
const (
	LevelGood     Level = "good"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
	LevelUnknown  Level = "unknown" // evidence was insufficient to judge
)

// LevelOf rates an assessment: high or critical findings are critical,
// medium findings a warning; otherwise an incomplete assessment is unknown
// and a complete one good.
func LevelOf(a assess.Assessment) Level {
	switch h := a.Highest(); {
	case h >= assess.SeverityHigh:
		return LevelCritical
	case h == assess.SeverityMedium:
		return LevelWarning
	case a.Incomplete():
		return LevelUnknown
	default:
		return LevelGood
	}
}

// Ratio is a healthy-out-of-total count, such as APs running.
type Ratio struct {
	OK    int `json:"ok"`
	Total int `json:"total"`
}

// Percent returns the healthy percentage, or 100 for an empty total.
func (r Ratio) Percent() float64 {
	if r.Total == 0 {
		return 100
	}
	return 100 * float64(r.OK) / float64(r.Total)
}
