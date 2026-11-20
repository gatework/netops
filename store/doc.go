// Package store is the private, durable file area of an operations tool.
//
// A Store is a directory owned by the current user and closed to others.
// Every write is atomic (temporary file, sync, rename, directory sync)
// through net-connector's storage package; reads are bounded and refuse
// symbolic links; relative paths cannot escape the root. Lock serializes
// writers of one key across processes. Name derives collision-free file
// names from arbitrary keys such as command lines.
//
// The snapshot, archive and journal sub-packages build their formats on
// these guarantees.
package store
