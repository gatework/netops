package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrInvalid reports an observation, digest or coverage that breaks its
// invariants.
var ErrInvalid = errors.New("evidence: invalid")

// Digest is the lowercase hex SHA-256 of some bytes.
type Digest string

// DigestOf returns the digest of b.
func DigestOf(b []byte) Digest {
	sum := sha256.Sum256(b)
	return Digest(hex.EncodeToString(sum[:]))
}

// DigestString returns the digest of s.
func DigestString(s string) Digest { return DigestOf([]byte(s)) }

// ParseDigest validates a hex SHA-256 digest.
func ParseDigest(s string) (Digest, error) {
	if !Digest(s).Valid() {
		return "", fmt.Errorf("%w: digest", ErrInvalid)
	}
	return Digest(s), nil
}

// Valid reports whether d is 64 lowercase hex digits.
func (d Digest) Valid() bool {
	if len(d) != 2*sha256.Size {
		return false
	}
	for _, r := range d {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Short returns the first 12 hex digits for display.
func (d Digest) Short() string {
	if len(d) < 12 {
		return string(d)
	}
	return string(d[:12])
}

// SourceRef points at the evidence behind a fact or a finding: a device, a
// command, the digest of the raw output and, optionally, a 1-based line.
type SourceRef struct {
	Device  string `json:"device,omitempty"`
	Command string `json:"command"`
	Digest  Digest `json:"digest,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// At returns a copy of r pointing at line.
func (r SourceRef) At(line int) SourceRef {
	r.Line = line
	return r
}

// Field is one structured, non-sensitive detail of an Issue.
type Field struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Issue is a problem met while decoding: an unrecognized line, a count that
// does not match a header, a command that failed. Issues carry codes and
// structured fields, never raw device text.
type Issue struct {
	Code   string     `json:"code"`
	Source *SourceRef `json:"source,omitempty"`
	Count  int        `json:"count,omitempty"`
	Fields []Field    `json:"fields,omitempty"`
}

// Observation is the typed result of observing one dimension.
//
// Construct it with NewObservation, NotRequested or Failed; the zero value
// is invalid. Facts are owned by the observation after construction.
type Observation[T any] struct {
	Dimension  Dimension   `json:"dimension"`
	Status     Status      `json:"status"`
	ObservedAt time.Time   `json:"observed_at,omitzero"`
	Facts      []T         `json:"facts"`
	Issues     []Issue     `json:"issues"`
	Sources    []SourceRef `json:"sources"`
}

// NewObservation validates and returns an observation. Failed,
// unsupported and not-requested observations must not carry facts. A
// complete observation may carry informational issues, such as a tolerated
// count drift; the decoder decides whether an issue degrades the status.
func NewObservation[T any](dim Dimension, status Status, at time.Time, facts []T, issues []Issue, sources []SourceRef) (Observation[T], error) {
	if !dim.Valid() || !status.Valid() {
		return Observation[T]{}, fmt.Errorf("%w: observation %q status %q", ErrInvalid, dim, status)
	}
	if !status.Usable() && len(facts) > 0 {
		return Observation[T]{}, fmt.Errorf("%w: %s observation %q carries facts", ErrInvalid, status, dim)
	}
	return Observation[T]{
		Dimension: dim, Status: status, ObservedAt: at,
		Facts:   nonNil(slices.Clone(facts)),
		Issues:  nonNil(slices.Clone(issues)),
		Sources: nonNil(slices.Clone(sources)),
	}, nil
}

// Decoded returns a complete observation when there are no issues and a
// partial one otherwise. It is the usual constructor for a decoder.
func Decoded[T any](dim Dimension, at time.Time, facts []T, issues []Issue, sources ...SourceRef) Observation[T] {
	status := StatusComplete
	if len(issues) > 0 {
		status = StatusPartial
	}
	o, err := NewObservation(dim, status, at, facts, issues, sources)
	if err != nil {
		return invalid[T](dim)
	}
	return o
}

// NotRequested returns the observation of a dimension that was not
// collected.
func NotRequested[T any](dim Dimension) Observation[T] {
	o, err := NewObservation[T](dim, StatusNotRequested, time.Time{}, nil, nil, nil)
	if err != nil {
		return invalid[T](dim)
	}
	return o
}

// Failed returns the observation of a dimension whose collection failed
// with code.
func Failed[T any](dim Dimension, code string, sources ...SourceRef) Observation[T] {
	issue := Issue{Code: code}
	if len(sources) > 0 {
		first := sources[0]
		issue.Source = &first
	}
	o, err := NewObservation[T](dim, StatusFailed, time.Time{}, nil, []Issue{issue}, sources)
	if err != nil {
		return invalid[T](dim)
	}
	return o
}

// invalid represents a dimension name that failed validation: the
// observation is failed so that rules never read it as evidence.
func invalid[T any](dim Dimension) Observation[T] {
	return Observation[T]{Dimension: dim, Status: StatusFailed, Facts: []T{}, Issues: []Issue{{Code: "invalid_dimension"}}, Sources: []SourceRef{}}
}

func nonNil[S ~[]E, E any](s S) S {
	if s == nil {
		return S{}
	}
	return s
}
