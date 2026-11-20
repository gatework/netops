// Package describe keeps interface descriptions in line with the
// discovered topology: "To_<neighbor>_<port>" on every port with exactly
// one known LLDP/CDP neighbor.
//
// The flow is read → audit → plan → apply:
//
//   - A Profile names, per platform, the commands to collect (neighbors and
//     running configuration) and turns their output into a Model.
//   - Policy.Audit compares each port's description with the one its
//     neighbor implies and records evidence issues (ambiguous or unknown
//     neighbors, missing interfaces) instead of guessing.
//   - Audit.Change returns a Change, a change.Change[Model] whose Render
//     uses the platform's configuration syntax and whose rollback restores
//     the previous descriptions. It is applied with ops.Deploy like any other
//     netops change: precheck, journal, write, read back, verify.
//
// The package does no I/O.
package describe
