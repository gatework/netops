package parse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"slices"
	"strings"

	"github.com/gatework/netops/evidence"
)

const (
	// DefaultMaxBytes is the source size limit of zero-value Limits.
	DefaultMaxBytes = 64 << 20
	// DefaultMaxLines is the physical line limit of zero-value Limits.
	DefaultMaxLines = 1_000_000
	scanChunk       = 32 << 10
)

var (
	// ErrInvalidLimits reports a negative limit.
	ErrInvalidLimits = errors.New("parse: invalid limits")
	// ErrLimit reports a source that exceeds a limit.
	ErrLimit = errors.New("parse: source limit exceeded")
	// ErrLineRange reports a line number or position outside the content.
	ErrLineRange = errors.New("parse: line out of range")
)

// Limits bounds accepted sources. A zero field takes its default; negative
// values are invalid.
type Limits struct {
	MaxBytes int
	MaxLines int
}

func (l Limits) normalized() (Limits, error) {
	if l.MaxBytes < 0 || l.MaxLines < 0 {
		return Limits{}, ErrInvalidLimits
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxLines == 0 {
		l.MaxLines = DefaultMaxLines
	}
	return l, nil
}

// LimitError reports which limit a source exceeded. Observed may be a lower
// bound because indexing stops early.
type LimitError struct {
	Resource string // "bytes" or "lines"
	Limit    int
	Observed int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("parse: %s limit %d exceeded (observed %d)", e.Resource, e.Limit, e.Observed)
}

// Unwrap makes errors.Is(err, ErrLimit) true.
func (e *LimitError) Unwrap() error { return ErrLimit }

// Line is one physical line. Raw keeps the terminator; Text drops it.
type Line struct {
	Number    int
	StartByte int
	EndByte   int
	Raw       string
	Text      string
}

// Content is immutable, line-indexed text. LF, CRLF and a lone CR each end
// a line; a final unterminated line counts. Methods on a nil *Content
// behave like empty content. Content is safe for concurrent use.
type Content struct {
	text   string
	ends   []int // ends[i] is the byte offset just past line i+1
	digest evidence.Digest
}

// New indexes text under limits. It checks ctx between chunks so that a
// huge source can be abandoned.
func New(ctx context.Context, text string, limits Limits) (*Content, error) {
	limits, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	if len(text) > limits.MaxBytes {
		return nil, &LimitError{Resource: "bytes", Limit: limits.MaxBytes, Observed: len(text)}
	}
	ends := []int{}
	h := sha256.New()
	for start := 0; start < len(text); start += scanChunk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+scanChunk, len(text))
		_, _ = io.WriteString(h, text[start:end])
	}
	for i := 0; i < len(text); i++ {
		if i%scanChunk == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		switch text[i] {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
		case '\n':
		default:
			continue
		}
		if len(ends) == limits.MaxLines {
			return nil, &LimitError{Resource: "lines", Limit: limits.MaxLines, Observed: len(ends) + 1}
		}
		ends = append(ends, i+1)
	}
	if last := lastEnd(ends); last < len(text) {
		if len(ends) == limits.MaxLines {
			return nil, &LimitError{Resource: "lines", Limit: limits.MaxLines, Observed: len(ends) + 1}
		}
		ends = append(ends, len(text))
	}
	return &Content{text: text, ends: ends, digest: evidence.Digest(hex.EncodeToString(h.Sum(nil)))}, nil
}

func lastEnd(ends []int) int {
	if len(ends) == 0 {
		return 0
	}
	return ends[len(ends)-1]
}

// Text returns the complete source.
func (c *Content) Text() string {
	if c == nil {
		return ""
	}
	return c.text
}

// Len returns the number of lines.
func (c *Content) Len() int {
	if c == nil {
		return 0
	}
	return len(c.ends)
}

// Digest returns the SHA-256 of the source.
func (c *Content) Digest() evidence.Digest {
	if c == nil {
		return evidence.DigestOf(nil)
	}
	return c.digest
}

// Line returns line number n (1-based).
func (c *Content) Line(n int) (Line, bool) {
	if n < 1 || n > c.Len() {
		return Line{}, false
	}
	start, end := c.bounds(n - 1)
	raw := c.text[start:end]
	text := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
	return Line{Number: n, StartByte: start, EndByte: end, Raw: raw, Text: text}, true
}

// Span returns the raw text of lines first through last inclusive.
func (c *Content) Span(first, last int) (string, bool) {
	if first < 1 || last < first || last > c.Len() {
		return "", false
	}
	start, _ := c.bounds(first - 1)
	return c.text[start:c.ends[last-1]], true
}

// LineAt returns the line that contains byte offset.
func (c *Content) LineAt(offset int) (Line, bool) {
	if c == nil || offset < 0 || offset >= len(c.text) {
		return Line{}, false
	}
	i, _ := slices.BinarySearch(c.ends, offset+1)
	return c.Line(i + 1)
}

// All yields every line in order.
func (c *Content) All() iter.Seq[Line] {
	return func(yield func(Line) bool) {
		for n := 1; n <= c.Len(); n++ {
			l, _ := c.Line(n)
			if !yield(l) {
				return
			}
		}
	}
}

// Lines returns every line.
func (c *Content) Lines() []Line { return slices.Collect(c.All()) }

// Cursor returns a new cursor at the first line with no line parsed.
func (c *Content) Cursor() *Cursor { return newCursor(c) }

// Format prints a summary that never includes the text.
func (c *Content) Format(f fmt.State, _ rune) {
	if c == nil {
		_, _ = io.WriteString(f, "<nil>")
		return
	}
	_, _ = fmt.Fprintf(f, "parse.Content{lines: %d, bytes: %d, sha256: %s}", c.Len(), len(c.text), c.digest.Short())
}

// LogValue logs a summary that never includes the text.
func (c *Content) LogValue() slog.Value {
	if c == nil {
		return slog.StringValue("<nil>")
	}
	return slog.GroupValue(slog.Int("lines", c.Len()), slog.Int("bytes", len(c.text)), slog.String("sha256", c.digest.Short()))
}

func (c *Content) bounds(i int) (start, end int) {
	if i > 0 {
		start = c.ends[i-1]
	}
	return start, c.ends[i]
}

// Lines yields the lines of a short, already bounded text, such as one
// command response, with the same numbering and terminators as New. It
// applies no limits; index untrusted or large input with New.
func Lines(text string) iter.Seq[Line] {
	return func(yield func(Line) bool) {
		start, n := 0, 0
		emit := func(end int) bool {
			n++
			raw := text[start:end]
			return yield(Line{Number: n, StartByte: start, EndByte: end, Raw: raw, Text: strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")})
		}
		for i := 0; i < len(text); i++ {
			switch text[i] {
			case '\r':
				if i+1 < len(text) && text[i+1] == '\n' {
					i++
				}
			case '\n':
			default:
				continue
			}
			if !emit(i + 1) {
				return
			}
			start = i + 1
		}
		if start < len(text) {
			emit(len(text))
		}
	}
}
