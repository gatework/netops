package parse

import (
	"iter"
	"strings"
)

// MaxDepth bounds tree nesting; deeper lines attach at the limit.
const MaxDepth = 64

// Node is one line of an indented hierarchy. The root has no line and
// depth -1. Nodes are built by Tree and then treated as immutable.
type Node struct {
	Line     Line
	Text     string // the line without indentation and trailing space
	Indent   int    // leading spaces, with a tab counted as one
	Depth    int    // -1 for the root
	Parent   *Node
	Children []*Node
}

// TreeOptions tunes Tree. The zero value builds a plain indentation tree.
type TreeOptions struct {
	// Skip leaves a line out of the tree, such as a blank or a comment.
	Skip func(Line) bool
	// Reset leaves a line out and returns to the top level, such as the "#"
	// separators of Comware and VRP configurations.
	Reset func(Line) bool
}

// Tree builds a hierarchy from lines by indentation: a line becomes a
// child of the nearest previous line with a smaller indent, or of the root
// after a reset line.
func Tree(lines iter.Seq[Line], o TreeOptions) *Node {
	root := &Node{Depth: -1, Indent: -1}
	stack := []*Node{root}
	for l := range lines {
		if o.Reset != nil && o.Reset(l) {
			stack = stack[:1]
			continue
		}
		if o.Skip != nil && o.Skip(l) {
			continue
		}
		text := strings.TrimRight(l.Text, " \t")
		trimmed := strings.TrimLeft(text, " \t")
		indent := len(text) - len(trimmed)
		for len(stack) > 1 && stack[len(stack)-1].Indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		n := &Node{Line: l, Text: trimmed, Indent: indent, Depth: parent.Depth + 1, Parent: parent}
		parent.Children = append(parent.Children, n)
		if n.Depth < MaxDepth-1 {
			stack = append(stack, n)
		}
	}
	return root
}

// Path returns the texts from the top-level ancestor down to n.
func (n *Node) Path() []string {
	var out []string
	for p := n; p != nil && p.Depth >= 0; p = p.Parent {
		out = append(out, p.Text)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Walk yields n's descendants depth first, in document order.
func (n *Node) Walk() iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		var visit func(*Node) bool
		visit = func(p *Node) bool {
			for _, c := range p.Children {
				if !yield(c) || !visit(c) {
					return false
				}
			}
			return true
		}
		visit(n)
	}
}

// Child returns the first child whose text equals text.
func (n *Node) Child(text string) (*Node, bool) {
	for _, c := range n.Children {
		if c.Text == text {
			return c, true
		}
	}
	return nil, false
}

// ChildrenWithPrefix returns the children whose text starts with prefix
// followed by a space or the end of the text.
func (n *Node) ChildrenWithPrefix(prefix string) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if rest, ok := strings.CutPrefix(c.Text, prefix); ok && (rest == "" || rest[0] == ' ') {
			out = append(out, c)
		}
	}
	return out
}

// Find follows a path of exact texts from n.
func (n *Node) Find(path ...string) (*Node, bool) {
	cur := n
	for _, p := range path {
		next, ok := cur.Child(p)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// LastLine returns the number of the last line in n's subtree, or n's own
// line for a leaf. For the root of an empty tree it is 0.
func (n *Node) LastLine() int {
	last := n.Line.Number
	for c := range n.Walk() {
		last = max(last, c.Line.Number)
	}
	return last
}
