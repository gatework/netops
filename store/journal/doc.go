// Package journal is the durable record of change management: plans as
// reviewed, approvals, execution records and an append-only audit trail.
//
// Layout:
//
//	<root>/plans/<plan>.json                      the plan envelope as reviewed
//	<root>/approvals/<plan>.json                  approvals of the plan
//	<root>/executions/<plan>/<device>.json        the latest apply.Record per device
//	<root>/audit.jsonl                            one line per event, append-only
//
// Execution records are replaced atomically on every state transition, so
// after a crash the file shows the last persisted state, including a
// pending command. The audit trail records who did what when, with codes
// only, never device output.
package journal
