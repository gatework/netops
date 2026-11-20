// Package batch runs one task per device with bounded concurrency,
// per-device timeouts and a complete account of every device.
//
// Collection and deployment both use it. Every device ends in exactly one
// state: succeeded, failed (with failure codes) or skipped (never started
// because the run was cancelled or a result could not be persisted).
// Results are ordered by device, not by completion, so reports are
// deterministic. Persisting each result through the Sink happens as soon as
// the device finishes; a Sink failure stops new devices from starting.
package batch
