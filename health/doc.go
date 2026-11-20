// Package health compares the operational state of a device before and
// after a change, and rates health from findings.
//
// A Snapshot records keyed facts per dimension, such as BGP neighbor
// 192.0.2.1 "established" or interface Eth1/1 "up", together with the
// evidence coverage of each dimension. Gate compares two snapshots: a
// healthy fact that disappears or leaves the healthy set is a regression,
// and a gated dimension without complete evidence fails closed.
package health
