package rollout

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/gatework/netops/device"
)

// ErrInvalid reports an invalid strategy.
var ErrInvalid = errors.New("rollout: invalid strategy")

// Strategy configures Schedule.
type Strategy struct {
	// Canary is the number of devices changed alone first; 0 disables it.
	Canary int
	// WaveSize is the maximum devices per wave; 0 means 4.
	WaveSize int
	// Tiers orders roles; devices of unlisted roles come last, by role.
	Tiers []string
	// Partition keeps devices with different keys in different waves, such
	// as func(d) string { return d.Site }. Nil puts everyone in one
	// partition.
	Partition func(device.Device) string
	// AntiAffinity returns a key shared by devices that must not change
	// together; "" means none.
	AntiAffinity func(device.Device) string
	// MaxFailures is how many failed or uncertain devices the rollout
	// tolerates before stopping; 0 stops at the first.
	MaxFailures int
}

// Wave is a set of devices changed concurrently.
type Wave struct {
	Index   int             `json:"index"`
	Label   string          `json:"label"`
	Devices []device.Device `json:"devices"`
}

// Schedule returns the waves for devices. The result is deterministic.
func Schedule(devices []device.Device, s Strategy) ([]Wave, error) {
	if s.WaveSize == 0 {
		s.WaveSize = 4
	}
	if s.WaveSize < 1 || s.Canary < 0 || s.MaxFailures < 0 {
		return nil, ErrInvalid
	}
	partition := s.Partition
	if partition == nil {
		partition = func(device.Device) string { return "" }
	}
	affinity := s.AntiAffinity
	if affinity == nil {
		affinity = func(device.Device) string { return "" }
	}
	sorted := slices.Clone(devices)
	slices.SortStableFunc(sorted, func(a, b device.Device) int {
		return cmp.Or(compareTier(s.Tiers, a.Role, b.Role), cmp.Compare(partition(a), partition(b)), device.Compare(a, b))
	})
	var waves []Wave
	add := func(label string, ds []device.Device) {
		waves = append(waves, Wave{Index: len(waves) + 1, Label: label, Devices: ds})
	}
	if s.Canary > 0 && len(sorted) > 0 {
		n := min(s.Canary, len(sorted))
		canary, rest := pack(sorted, n, affinity)
		add("canary", canary)
		sorted = rest
	}
	for len(sorted) > 0 {
		head := sorted[0]
		group := slices.DeleteFunc(slices.Clone(sorted), func(d device.Device) bool {
			return d.Role != head.Role || partition(d) != partition(head)
		})
		sorted = slices.DeleteFunc(sorted, func(d device.Device) bool {
			return d.Role == head.Role && partition(d) == partition(head)
		})
		for len(group) > 0 {
			var wave []device.Device
			wave, group = pack(group, s.WaveSize, affinity)
			add(label(head.Role, partition(head)), wave)
		}
	}
	return waves, Validate(waves, affinity)
}

// pack takes up to n devices in order without repeating an affinity key
// and returns them with the remaining devices.
func pack(ds []device.Device, n int, affinity func(device.Device) string) (taken, rest []device.Device) {
	keys := map[string]bool{}
	for _, d := range ds {
		k := affinity(d)
		if len(taken) < n && (k == "" || !keys[k]) {
			taken = append(taken, d)
			if k != "" {
				keys[k] = true
			}
			continue
		}
		rest = append(rest, d)
	}
	return taken, rest
}

func label(role, part string) string {
	switch {
	case role == "" && part == "":
		return "devices"
	case part == "":
		return role
	case role == "":
		return part
	}
	return role + " " + part
}

func compareTier(tiers []string, a, b string) int {
	rank := func(r string) int {
		if i := slices.Index(tiers, r); i >= 0 {
			return i
		}
		return len(tiers)
	}
	return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b))
}

// Validate checks that no wave holds two devices with one affinity key and
// that no device appears twice.
func Validate(waves []Wave, affinity func(device.Device) string) error {
	seen := map[string]bool{}
	for _, w := range waves {
		keys := map[string]bool{}
		for _, d := range w.Devices {
			if seen[d.ID] {
				return fmt.Errorf("%w: device %s scheduled twice", ErrInvalid, d.ID)
			}
			seen[d.ID] = true
			if affinity == nil {
				continue
			}
			if k := affinity(d); k != "" {
				if keys[k] {
					return fmt.Errorf("%w: wave %d changes two devices of %s", ErrInvalid, w.Index, k)
				}
				keys[k] = true
			}
		}
	}
	return nil
}

// Tracker applies the stop policy as waves finish. It is not safe for
// concurrent use.
type Tracker struct {
	maxFailures int
	failures    int
	stopped     bool
}

// NewTracker returns a tracker for s.
func NewTracker(s Strategy) *Tracker { return &Tracker{maxFailures: s.MaxFailures} }

// Finish records the number of devices of a wave that did not succeed and
// reports whether the rollout may continue.
func (t *Tracker) Finish(failed int) bool {
	t.failures += failed
	if failed > 0 && t.failures > t.maxFailures {
		t.stopped = true
	}
	return !t.stopped
}

// Stopped reports whether the rollout halted.
func (t *Tracker) Stopped() bool { return t.stopped }

// Failures returns the failures counted so far.
func (t *Tracker) Failures() int { return t.failures }
