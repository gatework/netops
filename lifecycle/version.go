package lifecycle

import (
	"cmp"
	"strconv"
	"strings"
	"unicode"
)

// Version is a parsed software version. The zero Version is unknown.
type Version struct {
	raw    string
	tokens []token
}

type token struct {
	num   int
	word  string
	isNum bool
}

// ParseVersion splits s into numeric and alphabetic segments; every other
// character separates segments.
func ParseVersion(s string) Version {
	v := Version{raw: strings.TrimSpace(s)}
	var cur strings.Builder
	kind := 0 // 0 none, 1 digits, 2 letters
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		t := cur.String()
		if kind == 1 {
			n, err := strconv.Atoi(strings.TrimLeft(t, "0"))
			if err != nil {
				n = 0
			}
			v.tokens = append(v.tokens, token{num: n, isNum: true})
		} else {
			v.tokens = append(v.tokens, token{word: strings.ToLower(t)})
		}
		cur.Reset()
	}
	for _, r := range v.raw {
		next := 0
		switch {
		case r >= '0' && r <= '9':
			next = 1
		case unicode.IsLetter(r):
			next = 2
		}
		if next != kind {
			flush()
			kind = next
		}
		if next != 0 {
			cur.WriteRune(r)
		}
	}
	flush()
	return v
}

// Known reports whether the version has any segment.
func (v Version) Known() bool { return len(v.tokens) > 0 }

// String returns the version as given.
func (v Version) String() string { return v.raw }

// Compare orders versions segment by segment. A version that extends
// another is greater; at a position where one segment is a number and the
// other a word, the word sorts first.
func (v Version) Compare(o Version) int {
	for i := range min(len(v.tokens), len(o.tokens)) {
		a, b := v.tokens[i], o.tokens[i]
		switch {
		case a.isNum && b.isNum:
			if c := cmp.Compare(a.num, b.num); c != 0 {
				return c
			}
		case a.isNum != b.isNum:
			if a.isNum {
				return 1
			}
			return -1
		default:
			if c := strings.Compare(a.word, b.word); c != 0 {
				return c
			}
		}
	}
	return cmp.Compare(len(v.tokens), len(o.tokens))
}

// Equal reports whether the versions compare equal.
func (v Version) Equal(o Version) bool { return v.Compare(o) == 0 }
