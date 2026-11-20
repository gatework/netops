// Package evidence records what was observed, how completely, and where it
// came from.
//
// An Observation[T] holds the typed facts of one Dimension together with
// its Status, the Issues met while decoding and SourceRefs that point back
// to the exact command output. Coverage summarizes the status of every
// dimension so that rules can refuse to judge on incomplete evidence.
//
// A Hasher builds fingerprints from explicitly named fields. Fingerprints
// therefore depend on what the caller declares, not on struct tags or
// encoder behavior.
//
// The package is pure: it performs no I/O and reads no clock.
package evidence
