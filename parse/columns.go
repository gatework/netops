package parse

import (
	"strings"
)

// Columns slices fixed-width table rows by the start offsets of their
// header. Offsets are bytes, which suits the ASCII tables of device CLIs.
// The zero value has no columns.
type Columns struct {
	starts []int
}

// HeaderColumns locates each label in header, left to right. ok is false
// when a label is missing.
func HeaderColumns(header string, labels ...string) (Columns, bool) {
	c := Columns{starts: make([]int, len(labels))}
	from := 0
	for i, l := range labels {
		at := strings.Index(header[from:], l)
		if at < 0 {
			return Columns{}, false
		}
		c.starts[i] = from + at
		from += at + len(l)
	}
	return c, true
}

// RulerColumns derives columns from a ruler line such as "----- ---- ---":
// each run of the rune starts a column. ok is false when the line contains
// anything but the rune and spaces, or no run at all.
func RulerColumns(ruler string, r byte) (Columns, bool) {
	var c Columns
	prev := byte(' ')
	for i := range len(ruler) {
		switch ruler[i] {
		case r:
			if prev != r {
				c.starts = append(c.starts, i)
			}
		case ' ', '\t':
		default:
			return Columns{}, false
		}
		prev = ruler[i]
	}
	return c, len(c.starts) > 0
}

// Len returns the number of columns.
func (c Columns) Len() int { return len(c.starts) }

// Cells returns the trimmed cell of every column; cells past the end of a
// short row are empty. The last column takes the rest of the row.
func (c Columns) Cells(row string) []string {
	out := make([]string, len(c.starts))
	for i, start := range c.starts {
		if start >= len(row) {
			break
		}
		end := len(row)
		if i+1 < len(c.starts) && c.starts[i+1] < end {
			end = c.starts[i+1]
		}
		out[i] = strings.TrimSpace(row[start:end])
	}
	return out
}
