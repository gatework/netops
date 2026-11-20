package evidence

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Coverage is the status of every observed dimension of one model. It is an
// immutable value: With returns a new Coverage. Dimensions never recorded
// report StatusNotRequested.
type Coverage struct {
	m map[Dimension]Status
}

// NewCoverage records the given statuses. A later entry for the same
// dimension replaces an earlier one.
func NewCoverage(entries map[Dimension]Status) (Coverage, error) {
	c := Coverage{m: make(map[Dimension]Status, len(entries))}
	for d, s := range entries {
		if !d.Valid() || !s.Valid() {
			return Coverage{}, fmt.Errorf("%w: coverage %q=%q", ErrInvalid, d, s)
		}
		c.m[d] = s
	}
	return c, nil
}

// Cover returns the coverage of the given observations.
func Cover(observations ...interface{ Coverage() (Dimension, Status) }) Coverage {
	c := Coverage{m: make(map[Dimension]Status, len(observations))}
	for _, o := range observations {
		d, s := o.Coverage()
		c.m[d] = s
	}
	return c
}

// Coverage returns the dimension and status of o, so that observations can
// be passed to Cover.
func (o Observation[T]) Coverage() (Dimension, Status) { return o.Dimension, o.Status }

// With returns a copy of c recording status for dim. An invalid dimension
// or status is recorded as failed so it can never satisfy a rule.
func (c Coverage) With(dim Dimension, status Status) Coverage {
	m := maps.Clone(c.m)
	if m == nil {
		m = map[Dimension]Status{}
	}
	if !status.Valid() {
		status = StatusFailed
	}
	m[dim] = status
	return Coverage{m: m}
}

// Status returns the status of dim.
func (c Coverage) Status(dim Dimension) Status {
	if s, ok := c.m[dim]; ok {
		return s
	}
	return StatusNotRequested
}

// Dimensions returns the recorded dimensions in sorted order.
func (c Coverage) Dimensions() []Dimension { return slices.Sorted(maps.Keys(c.m)) }

// Missing returns the required dimensions that are not complete, in the
// order given.
func (c Coverage) Missing(required ...Dimension) []Dimension {
	var out []Dimension
	for _, d := range required {
		if !c.Status(d).Complete() {
			out = append(out, d)
		}
	}
	return out
}

// Complete reports whether every required dimension is complete.
func (c Coverage) Complete(required ...Dimension) bool { return len(c.Missing(required...)) == 0 }

// Overall returns the worst recorded status, or StatusNotRequested when
// nothing was recorded.
func (c Coverage) Overall() Status {
	if len(c.m) == 0 {
		return StatusNotRequested
	}
	out := StatusComplete
	for _, s := range c.m {
		out = Worse(out, s)
	}
	return out
}

// MarshalJSON encodes the coverage as an object keyed by dimension.
func (c Coverage) MarshalJSON() ([]byte, error) {
	m := c.m
	if m == nil {
		m = map[Dimension]Status{}
	}
	return json.Marshal(m)
}

// UnmarshalJSON decodes and validates an object keyed by dimension.
func (c *Coverage) UnmarshalJSON(b []byte) error {
	var m map[Dimension]Status
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("evidence: decode coverage: %w", err)
	}
	v, err := NewCoverage(m)
	if err != nil {
		return err
	}
	*c = v
	return nil
}
