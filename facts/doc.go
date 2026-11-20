// Package facts reads what a device reports about itself — hostname,
// model, serial number, software version, uptime — and compares it with
// what the inventory records.
//
// A Probe is the per-platform recipe: read-only commands and a TextFSM
// template over their concatenated output. Output without a software
// version is not recognized, so an error page or a wrong platform never
// yields empty facts that look valid. The package does no I/O.
package facts
