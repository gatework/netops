package device

import (
	"fmt"
	"maps"
	"net/netip"
	"regexp"
	"slices"
	"strings"
)

// Device is the identity of one managed device. It is a plain record: the
// zero value is invalid, and Validate states the invariants that an
// Inventory enforces.
type Device struct {
	// ID is the stable key. When empty, Normalize sets it to the
	// management address.
	ID string `json:"id" yaml:"id"`
	// Name is the operator-facing hostname or sysname.
	Name string `json:"name,omitempty" yaml:"name"`
	// Mgmt is the management address used to connect.
	Mgmt netip.Addr `json:"mgmt" yaml:"mgmt"`
	// Platform is the transport platform key, such as "cisco_nxos".
	Platform string `json:"platform,omitempty" yaml:"platform"`
	// Vendor, Model and Version describe the hardware and software.
	Vendor  string `json:"vendor,omitempty" yaml:"vendor"`
	Model   string `json:"model,omitempty" yaml:"model"`
	Version string `json:"version,omitempty" yaml:"version"`
	// Role and Site group devices for selection, baselines and rollout.
	Role string `json:"role,omitempty" yaml:"role"`
	Site string `json:"site,omitempty" yaml:"site"`
	// Labels are free operator attributes. Keys follow label syntax.
	Labels map[string]string `json:"labels,omitempty" yaml:"labels"`
}

var (
	idPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)
	labelPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

// Normalize returns d with the ID defaulted to the management address,
// attribute whitespace trimmed and an unmapped IPv4-in-IPv6 address
// unmapped. Labels are copied.
func (d Device) Normalize() Device {
	d.Mgmt = d.Mgmt.Unmap()
	d.Name = strings.TrimSpace(d.Name)
	d.Platform = strings.TrimSpace(d.Platform)
	d.Role = strings.TrimSpace(d.Role)
	d.Site = strings.TrimSpace(d.Site)
	if strings.TrimSpace(d.ID) == "" && d.Mgmt.IsValid() {
		d.ID = d.Mgmt.String()
	}
	d.ID = strings.TrimSpace(d.ID)
	d.Labels = maps.Clone(d.Labels)
	return d
}

// Validate reports the first broken invariant: a valid management address,
// a well-formed ID and label keys.
func (d Device) Validate() error {
	if !d.Mgmt.IsValid() {
		return fmt.Errorf("device: %q has no management address", d.ID)
	}
	if !idPattern.MatchString(d.ID) {
		return fmt.Errorf("device: invalid id %q", d.ID)
	}
	for _, k := range slices.Sorted(maps.Keys(d.Labels)) {
		if !labelPattern.MatchString(k) {
			return fmt.Errorf("device: %s: invalid label key %q", d.ID, k)
		}
	}
	return nil
}

// Label returns the operator-facing form "name(address)", or the address
// when the device has no name.
func (d Device) Label() string {
	addr := d.Mgmt.String()
	if d.Name == "" || d.Name == addr {
		return addr
	}
	return d.Name + "(" + addr + ")"
}

// Clone returns a copy that shares no mutable state with d.
func (d Device) Clone() Device {
	d.Labels = maps.Clone(d.Labels)
	return d
}

// Compare orders devices by management address, then ID.
func Compare(a, b Device) int {
	if c := a.Mgmt.Compare(b.Mgmt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}
