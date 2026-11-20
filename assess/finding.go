package assess

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gatework/netops/evidence"
)

// ErrInvalid reports invalid metadata, policy or findings.
var ErrInvalid = errors.New("assess: invalid")

// Severity ranks findings. The zero value is SeverityUnset.
type Severity int

// Severities from least to most severe.
const (
	SeverityUnset Severity = iota
	SeverityInfo
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

var severityNames = [...]string{"", "info", "low", "medium", "high", "critical"}

// Severities returns every valid severity, most severe first.
func Severities() []Severity {
	return []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo}
}

// Valid reports whether s is a set severity.
func (s Severity) Valid() bool { return s >= SeverityInfo && s <= SeverityCritical }

// String returns the lowercase name.
func (s Severity) String() string {
	if s < 0 || int(s) >= len(severityNames) {
		return fmt.Sprintf("Severity(%d)", int(s))
	}
	return severityNames[s]
}

// ParseSeverity parses a lowercase severity name.
func ParseSeverity(name string) (Severity, error) {
	for i, n := range severityNames {
		if n != "" && n == name {
			return Severity(i), nil
		}
	}
	return SeverityUnset, fmt.Errorf("%w: severity %q", ErrInvalid, name)
}

// MarshalText encodes a valid severity by name.
func (s Severity) MarshalText() ([]byte, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("%w: severity %d", ErrInvalid, int(s))
	}
	return []byte(s.String()), nil
}

// UnmarshalText decodes a severity name.
func (s *Severity) UnmarshalText(text []byte) error {
	v, err := ParseSeverity(string(text))
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// Category groups findings by concern.
type Category string

// Finding categories.
const (
	CategoryEvidence      Category = "evidence"
	CategoryConfiguration Category = "configuration"
	CategoryConsistency   Category = "consistency"
	CategoryDrift         Category = "drift"
	CategoryRuntime       Category = "runtime"
	CategorySecurity      Category = "security"
	CategoryCapacity      Category = "capacity"
	CategoryCompliance    Category = "compliance"
)

var categories = []Category{
	CategoryEvidence, CategoryConfiguration, CategoryConsistency, CategoryDrift,
	CategoryRuntime, CategorySecurity, CategoryCapacity, CategoryCompliance,
}

// Valid reports whether c is a known category.
func (c Category) Valid() bool { return slices.Contains(categories, c) }

// Subject is what a finding is about, as a path of kind/key pairs such as
// device:sw1/interface:Eth1/1. It is an immutable value.
type Subject struct {
	parts []SubjectPart
}

// SubjectPart is one level of a Subject.
type SubjectPart struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

// NewSubject returns a top-level subject.
func NewSubject(kind, key string) Subject { return Subject{parts: []SubjectPart{{kind, key}}} }

// Child returns a subject nested under s.
func (s Subject) Child(kind, key string) Subject {
	return Subject{parts: append(slices.Clone(s.parts), SubjectPart{kind, key})}
}

// Parts returns the levels, outermost first.
func (s Subject) Parts() []SubjectPart { return slices.Clone(s.parts) }

// Key returns the innermost key, or "" for the zero subject.
func (s Subject) Key() string {
	if len(s.parts) == 0 {
		return ""
	}
	return s.parts[len(s.parts)-1].Key
}

// String returns "kind:key/kind:key".
func (s Subject) String() string {
	parts := make([]string, len(s.parts))
	for i, p := range s.parts {
		parts[i] = p.Kind + ":" + p.Key
	}
	return strings.Join(parts, "/")
}

// MarshalText encodes the subject in String form.
func (s Subject) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText decodes the String form.
func (s *Subject) UnmarshalText(b []byte) error {
	var out Subject
	if len(b) > 0 {
		for part := range strings.SplitSeq(string(b), "/") {
			kind, key, ok := strings.Cut(part, ":")
			if !ok || kind == "" {
				return fmt.Errorf("%w: subject %q", ErrInvalid, string(b))
			}
			out.parts = append(out.parts, SubjectPart{kind, key})
		}
	}
	*s = out
	return nil
}

// Remediation suggests how to resolve a finding. Commands are
// illustrative; executable changes go through the change package.
type Remediation struct {
	Summary  string   `json:"summary"`
	Commands []string `json:"commands,omitempty"`
	Manual   bool     `json:"manual,omitempty"`
}

// Finding is one conclusion of a rule. Expected and Actual are display
// strings that the rule has already masked of secrets.
type Finding struct {
	Rule        RuleID               `json:"rule"`
	Severity    Severity             `json:"severity"`
	Category    Category             `json:"category"`
	Subject     Subject              `json:"subject"`
	Title       string               `json:"title"`
	Detail      string               `json:"detail,omitempty"`
	Expected    string               `json:"expected,omitempty"`
	Actual      string               `json:"actual,omitempty"`
	Fields      []evidence.Field     `json:"fields,omitempty"`
	Evidence    []evidence.SourceRef `json:"evidence,omitempty"`
	Verify      []string             `json:"verify,omitempty"`
	References  []string             `json:"references,omitempty"`
	Remediation *Remediation         `json:"remediation,omitempty"`
}

// compareFindings orders by severity (most severe first), category,
// subject, rule and detail, so reports are deterministic.
func compareFindings(a, b Finding) int {
	return cmp.Or(
		cmp.Compare(b.Severity, a.Severity),
		cmp.Compare(a.Category, b.Category),
		cmp.Compare(a.Subject.String(), b.Subject.String()),
		cmp.Compare(a.Rule, b.Rule),
		cmp.Compare(a.Detail, b.Detail),
	)
}
