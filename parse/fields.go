package parse

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// SplitLabel splits "Label <sep> value" at the first separator and trims
// both sides. ok is false without a separator or with an empty label.
func SplitLabel(line, separator string) (label, value string, ok bool) {
	label, value, ok = strings.Cut(line, separator)
	label, value = strings.TrimSpace(label), strings.TrimSpace(value)
	return label, value, ok && label != ""
}

// NormalizeLabel lowercases a label and collapses internal whitespace, so
// that "AP  Name" and "ap name" are the same label.
func NormalizeLabel(label string) string {
	return strings.Join(strings.Fields(strings.ToLower(label)), " ")
}

// Labels maps a field name to the label spellings that denote it across
// software versions.
type Labels map[string][]string

// LabelTable resolves label spellings to field names. It is immutable and
// safe for concurrent use.
type LabelTable struct {
	index map[string]string
}

// DuplicateLabelError reports a spelling claimed by two fields.
type DuplicateLabelError struct {
	Label  string
	Fields [2]string
}

func (e *DuplicateLabelError) Error() string {
	return fmt.Sprintf("parse: label %q maps to both %s and %s", e.Label, e.Fields[0], e.Fields[1])
}

// Compile builds the table. Fields are visited in sorted order, so the
// reported duplicate is deterministic.
func (l Labels) Compile() (*LabelTable, error) {
	t := &LabelTable{index: map[string]string{}}
	for _, field := range slices.Sorted(maps.Keys(l)) {
		for _, label := range l[field] {
			key := NormalizeLabel(label)
			if prev, dup := t.index[key]; dup && prev != field {
				return nil, &DuplicateLabelError{Label: label, Fields: [2]string{prev, field}}
			}
			t.index[key] = field
		}
	}
	return t, nil
}

// MustCompile is Compile for package-level tables; it panics on a
// duplicate, which is a programming error.
func (l Labels) MustCompile() *LabelTable {
	t, err := l.Compile()
	if err != nil {
		panic(err)
	}
	return t
}

// Field returns the field denoted by label.
func (t *LabelTable) Field(label string) (string, bool) {
	if t == nil {
		return "", false
	}
	f, ok := t.index[NormalizeLabel(label)]
	return f, ok
}
