// Package disk holds the private-file primitives the stores share: atomic
// replacement, bounded reads with a content digest, private append-only
// logs, directory preparation and cross-process locks.
//
// Every file and directory it creates is private to the current user
// (0600 / 0700). Atomic replacement is delegated to renameio and locking to
// flock; this package only adds the bounds and privacy checks.
package disk
