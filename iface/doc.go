// Package iface normalizes network interface names across vendors.
//
// Devices spell the same port many ways: "GigabitEthernet1/0/1", "GE1/0/1",
// "Gi1/0/1", "g1/0/1". Key gives a comparison key that equates them, Short
// the conventional abbreviation used in descriptions, and Long the full
// name accepted by configuration commands. Unknown families pass through
// unchanged (Key lowercases them).
package iface
