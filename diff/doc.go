// Package diff compares configurations and collections.
//
// Lines computes a line edit script with bounded memory: common prefix and
// suffix are trimmed, lines unique to both sides anchor the alignment
// (patience diff), and the remaining gaps are aligned exactly when they fit
// the budget. A gap beyond the budget is reported as a replacement and the
// script is marked inexact, never silently truncated. Unified renders a
// script in the familiar unified format.
//
// Tree compares two parsed configurations block by block, so a moved
// interface section or a reordered top-level command is not reported as a
// change, while order-sensitive blocks (ACLs, route maps) can opt in to
// ordered comparison.
//
// Maps and Sets compare keyed facts with deterministic output.
package diff
