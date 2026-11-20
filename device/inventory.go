package device

import (
	"net/netip"
	"slices"
)

// Reason explains why an inventory record was rejected.
type Reason string

// Rejection reasons.
const (
	ReasonInvalid          Reason = "invalid"           // Validate failed
	ReasonDuplicateID      Reason = "duplicate_id"      // an earlier record has the same ID
	ReasonDuplicateAddress Reason = "duplicate_address" // an earlier record has the same address
)

// Rejection is one record left out of an inventory.
type Rejection struct {
	Index  int    `json:"index"`
	ID     string `json:"id,omitempty"`
	Reason Reason `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// Report accounts for every record offered to New.
type Report struct {
	Offered  int         `json:"offered"`
	Accepted int         `json:"accepted"`
	Rejected []Rejection `json:"rejected,omitempty"`
}

// Inventory is an immutable, validated set of devices indexed by ID and
// management address. IDs and addresses are unique. It is safe for
// concurrent use.
type Inventory struct {
	devices []Device // sorted by Compare
	byID    map[string]int
	byAddr  map[netip.Addr]int
}

// New validates, deduplicates and indexes devices. The first record wins a
// duplicate; every left-out record is reported. Devices are copied.
func New(devices []Device) (*Inventory, Report) {
	r := Report{Offered: len(devices)}
	kept := make([]Device, 0, len(devices))
	ids := make(map[string]bool, len(devices))
	addrs := make(map[netip.Addr]bool, len(devices))
	for i, raw := range devices {
		d := raw.Normalize()
		reject := func(reason Reason, detail string) {
			r.Rejected = append(r.Rejected, Rejection{Index: i, ID: d.ID, Reason: reason, Detail: detail})
		}
		switch err := d.Validate(); {
		case err != nil:
			reject(ReasonInvalid, err.Error())
		case ids[d.ID]:
			reject(ReasonDuplicateID, "")
		case addrs[d.Mgmt]:
			reject(ReasonDuplicateAddress, d.Mgmt.String())
		default:
			ids[d.ID], addrs[d.Mgmt] = true, true
			kept = append(kept, d)
		}
	}
	r.Accepted = len(kept)
	return index(kept), r
}

func index(devices []Device) *Inventory {
	slices.SortFunc(devices, Compare)
	inv := &Inventory{
		devices: devices,
		byID:    make(map[string]int, len(devices)),
		byAddr:  make(map[netip.Addr]int, len(devices)),
	}
	for i, d := range devices {
		inv.byID[d.ID] = i
		inv.byAddr[d.Mgmt] = i
	}
	return inv
}

// Len returns the number of devices.
func (inv *Inventory) Len() int {
	if inv == nil {
		return 0
	}
	return len(inv.devices)
}

// All returns copies of every device, ordered by address then ID.
func (inv *Inventory) All() []Device {
	if inv == nil {
		return nil
	}
	out := make([]Device, len(inv.devices))
	for i, d := range inv.devices {
		out[i] = d.Clone()
	}
	return out
}

// Lookup returns the device with the given ID.
func (inv *Inventory) Lookup(id string) (Device, bool) {
	if inv == nil {
		return Device{}, false
	}
	i, ok := inv.byID[id]
	if !ok {
		return Device{}, false
	}
	return inv.devices[i].Clone(), true
}

// ByAddr returns the device with the given management address.
func (inv *Inventory) ByAddr(addr netip.Addr) (Device, bool) {
	if inv == nil {
		return Device{}, false
	}
	i, ok := inv.byAddr[addr.Unmap()]
	if !ok {
		return Device{}, false
	}
	return inv.devices[i].Clone(), true
}

// Select returns copies of the devices matched by s, in inventory order.
func (inv *Inventory) Select(s Selector) []Device {
	if inv == nil {
		return nil
	}
	var out []Device
	for _, d := range inv.devices {
		if s.Match(d) {
			out = append(out, d.Clone())
		}
	}
	return out
}

// Merge returns a new inventory of inv's devices overlaid by later ones:
// a device in later with the same ID or address replaces the earlier
// record. Later inventories win in argument order.
func (inv *Inventory) Merge(later ...*Inventory) *Inventory {
	var merged []Device
	if inv != nil {
		merged = slices.Clone(inv.devices)
	}
	for _, next := range later {
		if next == nil {
			continue
		}
		for _, d := range next.devices {
			merged = slices.DeleteFunc(merged, func(old Device) bool { return old.ID == d.ID || old.Mgmt == d.Mgmt })
			merged = append(merged, d)
		}
	}
	for i := range merged {
		merged[i] = merged[i].Clone()
	}
	return index(merged)
}
