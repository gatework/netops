package diff

import (
	"cmp"
	"slices"
	"strings"

	"github.com/gatework/netops/config"
	"github.com/gatework/netops/parse"
)

// Kind classifies a keyed or structural change.
type Kind string

// Change kinds.
const (
	KindAdded   Kind = "added"
	KindRemoved Kind = "removed"
	KindChanged Kind = "changed"
)

// Change is one structural configuration change: a command, with its whole
// block, added under or removed from the block at Path.
type Change struct {
	Kind Kind     `json:"kind"`
	Path []string `json:"path"` // the enclosing blocks, top level first
	Line string   `json:"line"` // the command text without indentation
	// Block is the command with its sub-commands, indented one space per
	// level relative to Line.
	Block []string `json:"block,omitempty"`
	// LineA and LineB are the 1-based source lines on each side, 0 when
	// absent.
	LineA int `json:"line_a,omitempty"`
	LineB int `json:"line_b,omitempty"`
}

// TreeOptions tunes Tree. The zero value compares every block as an
// unordered multiset of commands.
type TreeOptions struct {
	// Ordered reports whether the children of the block at path are
	// order-sensitive, such as ACL entries; their reordering is a change.
	Ordered func(path []string) bool
	// Ignore leaves a command and its block out of the comparison.
	Ignore func(path []string, line string) bool
}

// Tree returns the structural changes from a to b. Output order is
// deterministic: for each block, removals in a's order, additions in b's
// order, then the nested changes of matched sub-blocks in b's order.
func Tree(a, b *config.Document, o TreeOptions) []Change {
	var out []Change
	compareNodes(a.Root(), b.Root(), nil, o, &out)
	return out
}

func compareNodes(a, b *parse.Node, path []string, o TreeOptions, out *[]Change) {
	ac, bc := filterChildren(a, path, o), filterChildren(b, path, o)
	var pairs [][2]*parse.Node
	if o.Ordered != nil && o.Ordered(path) {
		pairs = orderedPairs(ac, bc, path, out)
	} else {
		pairs = unorderedPairs(ac, bc, path, out)
	}
	for _, p := range pairs {
		compareNodes(p[0], p[1], append(slices.Clone(path), p[1].Text), o, out)
	}
}

func filterChildren(n *parse.Node, path []string, o TreeOptions) []*parse.Node {
	if o.Ignore == nil {
		return n.Children
	}
	return slices.DeleteFunc(slices.Clone(n.Children), func(c *parse.Node) bool { return o.Ignore(path, c.Text) })
}

// unorderedPairs matches children by text as multisets.
func unorderedPairs(ac, bc []*parse.Node, path []string, out *[]Change) [][2]*parse.Node {
	free := map[string][]*parse.Node{}
	for _, n := range bc {
		free[n.Text] = append(free[n.Text], n)
	}
	matched := map[*parse.Node]*parse.Node{} // b -> a
	for _, n := range ac {
		if c := free[n.Text]; len(c) > 0 {
			matched[c[0]] = n
			free[n.Text] = c[1:]
			continue
		}
		*out = append(*out, change(KindRemoved, path, n, true))
	}
	var pairs [][2]*parse.Node
	for _, n := range bc {
		if a, ok := matched[n]; ok {
			pairs = append(pairs, [2]*parse.Node{a, n})
			continue
		}
		*out = append(*out, change(KindAdded, path, n, false))
	}
	return pairs
}

// orderedPairs aligns children with a line diff of their texts.
func orderedPairs(ac, bc []*parse.Node, path []string, out *[]Change) [][2]*parse.Node {
	at, bt := texts(ac), texts(bc)
	var pairs [][2]*parse.Node
	var added []Change
	for _, e := range Lines(at, bt, Options{}).Edits {
		switch e.Op {
		case OpEqual:
			pairs = append(pairs, [2]*parse.Node{ac[e.A], bc[e.B]})
		case OpDelete:
			*out = append(*out, change(KindRemoved, path, ac[e.A], true))
		case OpInsert:
			added = append(added, change(KindAdded, path, bc[e.B], false))
		}
	}
	*out = append(*out, added...)
	return pairs
}

func texts(nodes []*parse.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Text
	}
	return out
}

func change(k Kind, path []string, n *parse.Node, sideA bool) Change {
	c := Change{Kind: k, Path: slices.Clone(path), Line: n.Text, Block: block(n)}
	if c.Path == nil {
		c.Path = []string{}
	}
	if sideA {
		c.LineA = n.Line.Number
	} else {
		c.LineB = n.Line.Number
	}
	return c
}

func block(n *parse.Node) []string {
	if len(n.Children) == 0 {
		return nil
	}
	out := []string{n.Text}
	for c := range n.Walk() {
		out = append(out, strings.Repeat(" ", c.Depth-n.Depth)+c.Text)
	}
	return out
}

// Delta is one keyed difference between two maps.
type Delta[K cmp.Ordered, V any] struct {
	Kind   Kind `json:"kind"`
	Key    K    `json:"key"`
	Before V    `json:"before,omitzero"`
	After  V    `json:"after,omitzero"`
}

// Maps returns the keys added, removed or changed from before to after,
// ordered by key. equal compares values present on both sides.
func Maps[K cmp.Ordered, V any](before, after map[K]V, equal func(a, b V) bool) []Delta[K, V] {
	var out []Delta[K, V]
	for k, b := range before {
		a, ok := after[k]
		switch {
		case !ok:
			out = append(out, Delta[K, V]{Kind: KindRemoved, Key: k, Before: b})
		case !equal(b, a):
			out = append(out, Delta[K, V]{Kind: KindChanged, Key: k, Before: b, After: a})
		}
	}
	for k, a := range after {
		if _, ok := before[k]; !ok {
			out = append(out, Delta[K, V]{Kind: KindAdded, Key: k, After: a})
		}
	}
	slices.SortFunc(out, func(x, y Delta[K, V]) int { return cmp.Compare(x.Key, y.Key) })
	return out
}

// Sets returns the sorted, deduplicated elements added to and removed from
// before to reach after.
func Sets[K cmp.Ordered](before, after []K) (added, removed []K) {
	b := map[K]bool{}
	for _, k := range before {
		b[k] = true
	}
	a := map[K]bool{}
	for _, k := range after {
		a[k] = true
		if !b[k] {
			added = append(added, k)
		}
	}
	for _, k := range before {
		if !a[k] {
			removed = append(removed, k)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return slices.Compact(added), slices.Compact(removed)
}
