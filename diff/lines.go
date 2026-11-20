package diff

import (
	"fmt"
	"slices"
	"strings"
)

// Op is one edit operation.
type Op int8

// Edit operations.
const (
	OpEqual Op = iota
	OpDelete
	OpInsert
)

// String returns "equal", "delete" or "insert".
func (o Op) String() string {
	switch o {
	case OpEqual:
		return "equal"
	case OpDelete:
		return "delete"
	case OpInsert:
		return "insert"
	}
	return fmt.Sprintf("Op(%d)", int8(o))
}

// Edit is one line of an edit script. A and B are 0-based indexes into the
// old and new sides; the side an edit does not touch is -1.
type Edit struct {
	Op   Op
	A, B int
	Text string
}

// Script is the edit script from an old to a new sequence of lines.
type Script struct {
	Edits []Edit
	// Exact is false when a gap exceeded the budget and was reported as a
	// whole replacement instead of being aligned line by line.
	Exact bool
}

// Changed reports whether the sides differ.
func (s Script) Changed() bool {
	return slices.ContainsFunc(s.Edits, func(e Edit) bool { return e.Op != OpEqual })
}

// Stats returns the number of inserted and deleted lines.
func (s Script) Stats() (inserted, deleted int) {
	for _, e := range s.Edits {
		switch e.Op {
		case OpInsert:
			inserted++
		case OpDelete:
			deleted++
		case OpEqual:
		}
	}
	return inserted, deleted
}

// DefaultBudget is the largest gap, in compared line pairs, aligned
// exactly by Lines.
const DefaultBudget = 1 << 22

// Options tunes Lines. The zero value uses DefaultBudget.
type Options struct {
	Budget int
}

// Lines returns the edit script from a to b. Its time is linear in the
// input plus the aligned gaps, each bounded by the budget.
func Lines(a, b []string, o Options) Script {
	if o.Budget <= 0 {
		o.Budget = DefaultBudget
	}
	d := &differ{a: a, b: b, budget: o.Budget, exact: true}
	d.diff(0, len(a), 0, len(b))
	return Script{Edits: d.edits, Exact: d.exact}
}

type differ struct {
	a, b   []string
	budget int
	exact  bool
	edits  []Edit
}

func (d *differ) equal(i, j int) {
	d.edits = append(d.edits, Edit{Op: OpEqual, A: i, B: j, Text: d.b[j]})
}
func (d *differ) del(i int) { d.edits = append(d.edits, Edit{Op: OpDelete, A: i, B: -1, Text: d.a[i]}) }
func (d *differ) ins(j int) { d.edits = append(d.edits, Edit{Op: OpInsert, A: -1, B: j, Text: d.b[j]}) }

// diff appends the script for a[alo:ahi] against b[blo:bhi].
func (d *differ) diff(alo, ahi, blo, bhi int) {
	for alo < ahi && blo < bhi && d.a[alo] == d.b[blo] {
		d.equal(alo, blo)
		alo, blo = alo+1, blo+1
	}
	var suffix int
	for alo < ahi-suffix && blo < bhi-suffix && d.a[ahi-1-suffix] == d.b[bhi-1-suffix] {
		suffix++
	}
	ahi, bhi = ahi-suffix, bhi-suffix
	defer func() {
		for k := range suffix {
			d.equal(ahi+k, bhi+k)
		}
	}()
	switch {
	case alo == ahi:
		for j := blo; j < bhi; j++ {
			d.ins(j)
		}
		return
	case blo == bhi:
		for i := alo; i < ahi; i++ {
			d.del(i)
		}
		return
	}
	if anchors := d.anchors(alo, ahi, blo, bhi); len(anchors) > 0 {
		i, j := alo, blo
		for _, p := range anchors {
			d.diff(i, p[0], j, p[1])
			d.equal(p[0], p[1])
			i, j = p[0]+1, p[1]+1
		}
		d.diff(i, ahi, j, bhi)
		return
	}
	d.align(alo, ahi, blo, bhi)
}

// anchors returns the longest increasing chain of lines that occur exactly
// once in each range.
func (d *differ) anchors(alo, ahi, blo, bhi int) [][2]int {
	type seen struct{ ca, cb, ia, ib int }
	m := map[string]*seen{}
	for i := alo; i < ahi; i++ {
		s := m[d.a[i]]
		if s == nil {
			s = &seen{}
			m[d.a[i]] = s
		}
		s.ca++
		s.ia = i
	}
	for j := blo; j < bhi; j++ {
		if s := m[d.b[j]]; s != nil {
			s.cb++
			s.ib = j
		}
	}
	var pairs [][2]int // ordered by a index
	for i := alo; i < ahi; i++ {
		if s := m[d.a[i]]; s.ca == 1 && s.cb == 1 {
			pairs = append(pairs, [2]int{i, s.ib})
		}
	}
	return longestIncreasing(pairs)
}

// longestIncreasing returns the longest subsequence of pairs whose second
// elements increase (patience sorting, O(n log n)).
func longestIncreasing(pairs [][2]int) [][2]int {
	if len(pairs) == 0 {
		return nil
	}
	tails := []int{}                // index into pairs of the smallest tail per length
	prev := make([]int, len(pairs)) // predecessor in the chain
	for k, p := range pairs {
		n, _ := slices.BinarySearchFunc(tails, p[1], func(t, target int) int { return pairs[t][1] - target })
		if n > 0 {
			prev[k] = tails[n-1]
		} else {
			prev[k] = -1
		}
		if n == len(tails) {
			tails = append(tails, k)
		} else {
			tails[n] = k
		}
	}
	out := make([][2]int, len(tails))
	for k, n := tails[len(tails)-1], len(tails)-1; n >= 0; k, n = prev[k], n-1 {
		out[n] = pairs[k]
	}
	return out
}

// align diffs a gap without unique anchors exactly by longest common
// subsequence when it fits the budget, and as a replacement otherwise.
func (d *differ) align(alo, ahi, blo, bhi int) {
	n, m := ahi-alo, bhi-blo
	if n*m > d.budget || min(n, m) >= 1<<16 {
		d.exact = false
		for i := alo; i < ahi; i++ {
			d.del(i)
		}
		for j := blo; j < bhi; j++ {
			d.ins(j)
		}
		return
	}
	// lcs[i][j] is the LCS length of a[alo+i:ahi] and b[blo+j:bhi]; it never
	// exceeds min(n, m) < 1<<16.
	w := m + 1
	lcs := make([]uint16, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if d.a[alo+i] == d.b[blo+j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case d.a[alo+i] == d.b[blo+j]:
			d.equal(alo+i, blo+j)
			i, j = i+1, j+1
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			d.del(alo + i)
			i++
		default:
			d.ins(blo + j)
			j++
		}
	}
	for ; i < n; i++ {
		d.del(alo + i)
	}
	for ; j < m; j++ {
		d.ins(blo + j)
	}
}

// Hunk is a contiguous region of changes with surrounding context. Starts
// are 1-based line numbers; an empty side starts at the line before the
// hunk, as in the unified format.
type Hunk struct {
	AStart, ALen int
	BStart, BLen int
	Edits        []Edit
}

// Hunks groups the script into hunks with up to context equal lines around
// each change.
func (s Script) Hunks(context int) []Hunk {
	context = max(context, 0)
	edits := s.Edits
	// before[k] counts the old and new lines preceding edit k.
	before := make([][2]int, len(edits)+1)
	for k, e := range edits {
		before[k+1] = before[k]
		if e.A >= 0 {
			before[k+1][0]++
		}
		if e.B >= 0 {
			before[k+1][1]++
		}
	}
	var hunks []Hunk
	for k := 0; k < len(edits); {
		if edits[k].Op == OpEqual {
			k++
			continue
		}
		start := max(k-context, 0)
		end := k
		for end < len(edits) {
			if edits[end].Op != OpEqual {
				end++
				continue
			}
			run := end
			for run < len(edits) && edits[run].Op == OpEqual {
				run++
			}
			if run == len(edits) || run-end > 2*context {
				end = min(end+context, len(edits))
				break
			}
			end = run
		}
		hunks = append(hunks, newHunk(edits[start:end], before[start]))
		k = end
	}
	return hunks
}

func newHunk(edits []Edit, before [2]int) Hunk {
	h := Hunk{Edits: slices.Clone(edits), AStart: before[0], BStart: before[1]}
	for _, e := range edits {
		if e.A >= 0 {
			h.ALen++
		}
		if e.B >= 0 {
			h.BLen++
		}
	}
	if h.ALen > 0 {
		h.AStart++
	}
	if h.BLen > 0 {
		h.BStart++
	}
	return h
}

// Unified renders the script in unified diff format with the given file
// labels and context lines. mask, when not nil, rewrites every rendered
// line, for example to hide secrets. An unchanged script renders as "".
func Unified(s Script, labelA, labelB string, context int, mask func(string) string) string {
	hunks := s.Hunks(context)
	if len(hunks) == 0 {
		return ""
	}
	if mask == nil {
		mask = func(l string) string { return l }
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", labelA, labelB)
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", span(h.AStart, h.ALen), span(h.BStart, h.BLen))
		for _, e := range h.Edits {
			prefix := " "
			switch e.Op {
			case OpDelete:
				prefix = "-"
			case OpInsert:
				prefix = "+"
			case OpEqual:
			}
			b.WriteString(prefix + mask(e.Text) + "\n")
		}
	}
	return b.String()
}

// span formats a unified range.
func span(start, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}
