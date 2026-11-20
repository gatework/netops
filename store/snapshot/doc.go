// Package snapshot stores raw captures, one directory per device.
//
// Layout (schema 1, read-compatible with the net-connector batch layout and
// the earlier wireless and fabric caches):
//
//	<root>/<mgmt-ip>/NN-<command>.output   raw response bytes
//	<root>/<mgmt-ip>/NN-<command>.json     sidecar: command, digest, size, times, failure
//	<root>/.status/<mgmt-ip>.json          refresh status (cache.Status)
//	<root>/.locks/                         per-device writer locks
//
// A device directory is replaced atomically: the new capture is written to
// a staging directory and renamed into place, so a reader never sees half a
// capture. Reads verify each output against its sidecar size and digest.
// Merge holds the device lock across load, merge and save, so concurrent
// refreshes of one device do not lose each other's commands.
package snapshot
