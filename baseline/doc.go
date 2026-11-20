// Package baseline is the golden configuration of a fleet.
//
// A Profile layers expectations from general to specific (default, role,
// site, site/role) and Resolve yields the effective baseline of one device.
// A baseline holds keyed settings, compared with drift semantics, and text
// compliance rules (required and forbidden commands within matching
// blocks), checked against a parsed configuration. Derive proposes a
// baseline from what most peers already agree on.
package baseline
