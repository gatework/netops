package secret

import (
	"cmp"
	"log/slog"
	"slices"
	"strings"
)

// Mask replaces secret values in every rendering.
const Mask = "[REDACTED]"

// Secret is a sensitive string. The zero value is the empty secret.
// Secret is a comparable value type and is safe for concurrent use.
type Secret struct {
	value string
}

// New wraps value as a Secret.
func New(value string) Secret { return Secret{value: value} }

// Reveal returns the plain value. Call it only at the point of use, such as
// building a transport configuration.
func (s Secret) Reveal() string { return s.value }

// Empty reports whether the secret has no value.
func (s Secret) Empty() bool { return s.value == "" }

// String returns Mask, or "" for the empty secret.
func (s Secret) String() string {
	if s.value == "" {
		return ""
	}
	return Mask
}

// GoString returns the same masked form as String.
func (s Secret) GoString() string { return "secret.Secret(" + s.String() + ")" }

// LogValue masks the secret in structured logs.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalText masks the secret in every text encoding, including JSON and
// YAML. There is no UnmarshalText: secrets enter through New at a trusted
// boundary.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Redactor removes known secret values from text. The zero value redacts
// nothing. A Redactor is immutable and safe for concurrent use.
type Redactor struct {
	values []string // longest first, so a secret that contains another wins
}

// NewRedactor returns a Redactor for the non-empty secrets. Values shorter
// than minLength characters are ignored to avoid masking ordinary words;
// pass 0 to redact every non-empty value.
func NewRedactor(minLength int, secrets ...Secret) Redactor {
	values := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if len(s.value) > 0 && len(s.value) >= minLength && !slices.Contains(values, s.value) {
			values = append(values, s.value)
		}
	}
	slices.SortFunc(values, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b))
	})
	return Redactor{values: values}
}

// Redact returns text with every known secret replaced by Mask.
func (r Redactor) Redact(text string) string {
	for _, v := range r.values {
		text = strings.ReplaceAll(text, v, Mask)
	}
	return text
}

// Empty reports whether the redactor knows no secrets.
func (r Redactor) Empty() bool { return len(r.values) == 0 }
