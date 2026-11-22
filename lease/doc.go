// Package lease keeps two changes from touching one device at once, across
// tools and processes: a fabric deployment, a wireless station action and a
// firewall rule push all take a lease on each device before writing.
//
// A Lease names its holder (tool, actor, purpose such as a plan ID) and
// expires after its TTL, so a crashed process cannot block a device
// forever. A Manager acquires leases for a set of devices all-or-nothing,
// renews them for long rollouts and releases only what it holds. A
// conflict reports the current holder instead of taking over. The Store
// port performs each read-modify-write atomically; store/lease provides
// the file implementation.
package lease
