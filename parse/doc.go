// Package parse is the text toolkit for device command output and
// configuration.
//
// Content indexes text by physical line once, under explicit Limits, and
// computes its digest while doing so. A Cursor walks the lines and records
// which ones a parser consumed, so that every line a parser did not
// understand can be reported instead of ignored. A Scanner turns that
// bookkeeping into evidence issues and a status.
//
// Helpers cover the recurring output shapes: "Label : value" fields with a
// LabelTable of synonyms, fixed-width tables with Columns, and indented
// hierarchies with Tree.
//
// Line numbers are 1-based and always refer to the original text, so issues
// point at the device output. The package is pure and safe for concurrent
// reads of a Content; a Cursor belongs to one goroutine.
package parse
