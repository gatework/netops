package parse

import (
	"errors"
	"fmt"
	"iter"
	"math/bits"
	"strings"
)

// ErrCheckpoint reports a checkpoint taken from another cursor.
var ErrCheckpoint = errors.New("parse: foreign checkpoint")

// Cursor walks a Content and records which lines were parsed. Position is
// the number of lines consumed: Next returns line Position()+1. A Cursor is
// not safe for concurrent use and must not be copied.
type Cursor struct {
	_        noCopy
	content  *Content
	parsed   []uint64 // bit i%64 of word i/64 flags line index i; padding bits are set
	unparsed int
	position int
	id       *byte // identity token for checkpoints
}

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

func newCursor(c *Content) *Cursor {
	n := c.Len()
	parsed := make([]uint64, (n+63)/64)
	if pad := n % 64; pad != 0 {
		parsed[len(parsed)-1] = ^uint64(0) << pad
	}
	return &Cursor{content: c, parsed: parsed, unparsed: n, id: new(byte)}
}

// Content returns the content walked by the cursor.
func (c *Cursor) Content() *Content { return c.content }

// Position returns the number of lines consumed.
func (c *Cursor) Position() int { return c.position }

// Next consumes and returns the next line.
func (c *Cursor) Next() (Line, bool) {
	l, ok := c.Peek()
	if ok {
		c.position++
	}
	return l, ok
}

// Peek returns the next line without consuming it.
func (c *Cursor) Peek() (Line, bool) { return c.content.Line(c.position + 1) }

// NextUnparsed skips parsed lines and consumes the next unparsed one.
func (c *Cursor) NextUnparsed() (Line, bool) {
	i := c.nextUnparsed(c.position)
	if i == c.content.Len() {
		c.position = i
		return Line{}, false
	}
	c.position = i + 1
	return c.content.Line(i + 1)
}

// Seek sets the position; 0 rewinds.
func (c *Cursor) Seek(position int) error {
	if position < 0 || position > c.content.Len() {
		return fmt.Errorf("%w: position %d", ErrLineRange, position)
	}
	c.position = position
	return nil
}

// Checkpoint captures the position for Restore.
type Checkpoint struct {
	id       *byte
	position int
}

// Checkpoint returns the current position.
func (c *Cursor) Checkpoint() Checkpoint { return Checkpoint{id: c.id, position: c.position} }

// Restore returns to a checkpoint taken from this cursor.
func (c *Cursor) Restore(cp Checkpoint) error {
	if cp.id != c.id {
		return ErrCheckpoint
	}
	c.position = cp.position
	return nil
}

// Mark flags lines first through last inclusive as parsed.
func (c *Cursor) Mark(first, last int) error {
	if first < 1 || last < first || last > c.content.Len() {
		return fmt.Errorf("%w: [%d, %d]", ErrLineRange, first, last)
	}
	c.set(first-1, last, true)
	return nil
}

// Unmark flags lines first through last inclusive as unparsed.
func (c *Cursor) Unmark(first, last int) error {
	if first < 1 || last < first || last > c.content.Len() {
		return fmt.Errorf("%w: [%d, %d]", ErrLineRange, first, last)
	}
	c.set(first-1, last, false)
	return nil
}

// Parsed reports whether line n was marked.
func (c *Cursor) Parsed(n int) bool {
	if n < 1 || n > c.content.Len() {
		return false
	}
	i := n - 1
	return c.parsed[i/64]&(1<<(i%64)) != 0
}

// UnparsedCount returns the number of unmarked lines.
func (c *Cursor) UnparsedCount() int { return c.unparsed }

// Unparsed yields the unmarked lines in order.
func (c *Cursor) Unparsed() iter.Seq[Line] {
	return func(yield func(Line) bool) {
		for i := c.nextUnparsed(0); i < c.content.Len(); i = c.nextUnparsed(i + 1) {
			l, _ := c.content.Line(i + 1)
			if !yield(l) {
				return
			}
		}
	}
}

// UnparsedText returns the raw text of the unmarked lines.
func (c *Cursor) UnparsedText() string {
	var b strings.Builder
	for l := range c.Unparsed() {
		b.WriteString(l.Raw)
	}
	return b.String()
}

func (c *Cursor) nextUnparsed(from int) int {
	n := c.content.Len()
	for from < n {
		w := from / 64
		if free := ^c.parsed[w] >> (from % 64); free != 0 {
			return min(from+bits.TrailingZeros64(free), n)
		}
		from = (w + 1) * 64
	}
	return n
}

func (c *Cursor) set(lo, hi int, parsed bool) {
	for lo < hi {
		w, shift := lo/64, lo%64
		n := min(64-shift, hi-lo)
		mask := ^uint64(0) >> (64 - n) << shift
		old := c.parsed[w]
		if parsed {
			c.parsed[w] = old | mask
			c.unparsed -= bits.OnesCount64(^old & mask)
		} else {
			c.parsed[w] = old &^ mask
			c.unparsed += bits.OnesCount64(old & mask)
		}
		lo += n
	}
}
