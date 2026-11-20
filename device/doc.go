// Package device is the single identity model for managed network devices.
//
// A Device names one management endpoint: its stable ID, management
// address, platform and the operator attributes (role, site, labels) used to
// select and group it. An Inventory is an immutable, validated and indexed
// set of devices; building one reports every rejected or duplicate record
// instead of dropping it silently. A Selector picks devices by address,
// prefix, name and attributes with one grammar shared by every tool.
//
// The package is pure: inventory sources live in the inventory package.
package device
