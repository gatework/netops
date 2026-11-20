// Package neighbor models link-layer adjacencies discovered with LLDP or
// CDP and parses them from vendor CLI output.
//
// A Probe is the per-platform recipe: the command to send and how to read
// its answer. Parsing is template driven (TextFSM) and cross-checked: every
// probe also counts the records independently, and a parse whose row count
// disagrees is rejected rather than silently dropping neighbors.
package neighbor
