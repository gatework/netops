// Package apply executes one plan on one open device session, exactly
// once, with a durable journal.
//
// The state machine is deliberately conservative:
//
//   - the planned record is persisted before the device is touched;
//   - the device is re-read and the plan re-checked: an already satisfied
//     plan is skipped (idempotent re-run), a drifted device is refused;
//   - in dry-run mode execution stops after the re-check;
//   - before each write the pending command is persisted (intent), and the
//     acknowledgement after it;
//   - after the last write the device is re-read and the plan verified.
//
// Nothing is retried, replayed or rolled back automatically. Once any
// intent may have reached the device, every failure is reported as
// uncertain, never as a clean failure. Journal records carry failure codes
// and redacted commands, never device output or secrets.
//
// Opening and closing the session belongs to the caller, so one session
// can serve a whole workflow (health check, checkpoint, apply, save).
package apply
