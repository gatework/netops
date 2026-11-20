package evidence

import (
	"fmt"
	"regexp"
	"slices"
)

// Status is the completeness of one observed dimension. The zero value is
// StatusUnset and is never valid in an Observation.
type Status string

// Observation statuses.
const (
	StatusUnset        Status = ""
	StatusComplete     Status = "complete"      // every fact was decoded
	StatusPartial      Status = "partial"       // some facts are missing or undecoded
	StatusFailed       Status = "failed"        // collection or decoding failed
	StatusUnsupported  Status = "unsupported"   // the platform cannot provide it
	StatusNotRequested Status = "not_requested" // it was not collected
)

var statuses = []Status{StatusComplete, StatusPartial, StatusFailed, StatusUnsupported, StatusNotRequested}

// Valid reports whether s is a known, set status.
func (s Status) Valid() bool { return slices.Contains(statuses, s) }

// Complete reports whether s is StatusComplete.
func (s Status) Complete() bool { return s == StatusComplete }

// Usable reports whether facts with status s may be read at all: complete
// or partial evidence.
func (s Status) Usable() bool { return s == StatusComplete || s == StatusPartial }

// String returns the wire form.
func (s Status) String() string { return string(s) }

// MarshalText encodes a valid status.
func (s Status) MarshalText() ([]byte, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("evidence: invalid status %q", string(s))
	}
	return []byte(s), nil
}

// UnmarshalText decodes a known status and rejects any other value.
func (s *Status) UnmarshalText(b []byte) error {
	v := Status(b)
	if !v.Valid() {
		return fmt.Errorf("evidence: invalid status %q", string(b))
	}
	*s = v
	return nil
}

// rank orders statuses from best to worst for Worse.
func (s Status) rank() int {
	switch s {
	case StatusComplete:
		return 0
	case StatusNotRequested:
		return 1
	case StatusPartial:
		return 2
	case StatusUnsupported:
		return 3
	case StatusFailed:
		return 4
	case StatusUnset:
		return 5
	}
	return 5
}

// Worse returns the worse of two statuses, for aggregating a device or a
// batch: failed > unsupported > partial > not_requested > complete.
func Worse(a, b Status) Status {
	if b.rank() > a.rank() {
		return b
	}
	return a
}

// Dimension names one independently collected aspect of a device, such as
// "interfaces" or "clients". Dimensions are lowercase identifiers.
type Dimension string

var dimensionPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ParseDimension validates a dimension name.
func ParseDimension(s string) (Dimension, error) {
	if !dimensionPattern.MatchString(s) {
		return "", fmt.Errorf("evidence: invalid dimension %q", s)
	}
	return Dimension(s), nil
}

// Valid reports whether d is a well-formed dimension name.
func (d Dimension) Valid() bool { return dimensionPattern.MatchString(string(d)) }
