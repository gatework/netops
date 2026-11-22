// Package lease is the file implementation of lease.Store: one private
// JSON file per device under a shared root, every update under the
// device's cross-process lock. Point every tool on a host at the same
// root so that their changes exclude each other.
package lease
