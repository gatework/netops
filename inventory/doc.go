// Package inventory loads devices from their sources into a validated
// device.Inventory.
//
// Sources are a static YAML file maintained by operators and Netdisco
// (over HTTP or PostgreSQL, through net-connector, with its local cache).
// Load merges sources in order, later sources overriding earlier records
// of the same device, and returns a report that counts every record that
// was dropped and why. Nothing is discarded silently.
package inventory
