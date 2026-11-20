// Package term is the shared toolkit of command-line front ends: exit
// codes, safe terminal output, aligned tables that respect East Asian
// widths, typed confirmation before changes, JSON documents and localized
// messages for stable codes.
//
// Device text can contain terminal control sequences; Sanitize escapes
// them, and Table sanitizes every cell, so a hostile banner or interface
// description cannot rewrite the operator's screen.
package term
