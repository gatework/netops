// Package cache decides when collected evidence is fresh enough to reuse.
//
// Each dimension has a time to live. Decide compares the time each
// dimension of a device was last recorded with the policy and returns which
// dimensions are stale and whether to fetch them now: live mode always
// fetches, offline mode never does, and a device whose last attempt failed
// is left alone for a back-off period so a dead device does not slow every
// query. Group batches devices that need the same dimensions, so one
// collection round serves them all.
//
// The package is pure; the snapshot store records times and status.
package cache
