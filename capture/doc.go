// Package capture is the raw evidence of collection: the response of each
// command sent to a device, exactly as received.
//
// An Output keeps the raw response with its command echo and prompt, the
// time it was recorded and, for a command that failed, a failure code
// instead of a body. Body strips the framing in one place, so every vendor
// decoder sees the same response lines with the same line numbers.
//
// A Capture is the set of outputs of one device. Merge combines an older
// and a newer capture command by command, so an incremental refresh keeps
// what it did not re-collect and a failed command never hides an earlier
// good answer.
package capture
