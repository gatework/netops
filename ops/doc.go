// Package ops composes the netops packages into the operations a tool
// offers: batch collection, cache-aware refresh, configuration backup and
// comparison, compliance, and governed, waved deployment with health gates,
// checkpoints and rollback.
//
// The package depends on ports (Dialer, Session, Snapshots, Archive,
// Probes, apply.Journal), never on transports or files. A tool wires the
// adapters (connect, store/snapshot, store/archive, store/journal) in its
// own bootstrap and supplies the domain parts: the model type, how to read
// it from a session and how to judge its health.
package ops
