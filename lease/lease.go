package lease

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/evidence"
)

// ErrHeld reports a device leased by another holder; it wraps *HeldError.
var ErrHeld = errors.New("lease: device is held")

// ErrInvalid reports an invalid request.
var ErrInvalid = errors.New("lease: invalid")

// Holder identifies who holds a lease. Two holders are the same only when
// every field is equal.
type Holder struct {
	Tool    string `json:"tool"`
	Actor   string `json:"actor,omitempty"`
	Purpose string `json:"purpose,omitempty"` // such as a plan ID
}

// String returns "tool/actor (purpose)".
func (h Holder) String() string {
	s := h.Tool
	if h.Actor != "" {
		s += "/" + h.Actor
	}
	if h.Purpose != "" {
		s += " (" + h.Purpose + ")"
	}
	return s
}

// Lease is one device's lease.
type Lease struct {
	Device   string    `json:"device"`
	Holder   Holder    `json:"holder"`
	Acquired time.Time `json:"acquired"`
	Expires  time.Time `json:"expires"`
	Token    string    `json:"token"`
}

// Expired reports whether the lease has lapsed at now.
func (l Lease) Expired(now time.Time) bool { return !now.Before(l.Expires) }

// HeldError names the current holder.
type HeldError struct {
	Lease Lease
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("lease: %s is held by %s until %s", e.Lease.Device, e.Lease.Holder, e.Lease.Expires.Format(time.RFC3339))
}

// Unwrap makes errors.Is(err, ErrHeld) true.
func (e *HeldError) Unwrap() error { return ErrHeld }

// Store keeps leases. Update runs fn on the current lease of device
// (present reports whether one exists) atomically with respect to every
// other Update of the same device, across processes. fn returns the lease
// to store, or keep=false to delete it.
type Store interface {
	Get(ctx context.Context, device string) (l Lease, present bool, err error)
	Update(ctx context.Context, device string, fn func(cur Lease, present bool) (next Lease, keep bool, err error)) error
}

// Manager acquires and releases leases.
type Manager struct {
	store Store
	now   func() time.Time
}

// NewManager returns a manager over store; now may be nil.
func NewManager(store Store, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{store: store, now: now}
}

// Set is the leases one Acquire obtained.
type Set struct {
	Leases []Lease
}

// Devices returns the leased device IDs.
func (s Set) Devices() []string {
	out := make([]string, len(s.Leases))
	for i, l := range s.Leases {
		out[i] = l.Device
	}
	return out
}

// Acquire leases every device for holder, in sorted order. A device already
// held by the same holder is renewed; an expired lease is taken over. On a
// conflict, the leases obtained so far are released and a *HeldError is
// returned.
func (m *Manager) Acquire(ctx context.Context, devices []string, h Holder, ttl time.Duration) (Set, error) {
	if h.Tool == "" || ttl <= 0 {
		return Set{}, fmt.Errorf("%w: holder tool and positive ttl required", ErrInvalid)
	}
	ids := slices.Clone(devices)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var set Set
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return set, errors.Join(fmt.Errorf("%w: empty device", ErrInvalid), m.Release(context.WithoutCancel(ctx), set))
		}
		var got Lease
		err := m.store.Update(ctx, id, func(cur Lease, present bool) (Lease, bool, error) {
			now := m.now().UTC()
			if present && !cur.Expired(now) && cur.Holder != h {
				return cur, true, &HeldError{Lease: cur}
			}
			got = Lease{Device: id, Holder: h, Acquired: now, Expires: now.Add(ttl)}
			if present && cur.Holder == h && !cur.Expired(now) {
				got.Acquired, got.Token = cur.Acquired, cur.Token
			} else {
				got.Token = evidence.NewHasher("netops/lease/v1").String("device", id).
					String("holder", h.String()).Int("at", now.UnixNano()).Sum().Short()
			}
			return got, true, nil
		})
		if err != nil {
			return Set{}, errors.Join(err, m.Release(context.WithoutCancel(ctx), set))
		}
		set.Leases = append(set.Leases, got)
	}
	return set, nil
}

// Renew extends every lease of the set by ttl from now. A lease lost to
// another holder (after expiring) is reported with *HeldError.
func (m *Manager) Renew(ctx context.Context, set *Set, ttl time.Duration) error {
	var errs []error
	for i, l := range set.Leases {
		err := m.store.Update(ctx, l.Device, func(cur Lease, present bool) (Lease, bool, error) {
			if !present || cur.Token != l.Token {
				if present {
					return cur, true, &HeldError{Lease: cur}
				}
				return cur, false, fmt.Errorf("lease: %s was released", l.Device)
			}
			cur.Expires = m.now().UTC().Add(ttl)
			set.Leases[i] = cur
			return cur, true, nil
		})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Release deletes the leases of the set that are still held with the same
// token; a lease since taken over by another holder is left alone.
func (m *Manager) Release(ctx context.Context, set Set) error {
	var errs []error
	for _, l := range set.Leases {
		errs = append(errs, m.store.Update(ctx, l.Device, func(cur Lease, present bool) (Lease, bool, error) {
			if !present || cur.Token != l.Token {
				return cur, present, nil
			}
			return Lease{}, false, nil
		}))
	}
	return errors.Join(errs...)
}

// Holders returns the live leases of devices, for status displays.
func (m *Manager) Holders(ctx context.Context, devices []string) ([]Lease, error) {
	now := m.now()
	var out []Lease
	for _, id := range devices {
		l, ok, err := m.store.Get(ctx, id)
		if err != nil {
			return out, err
		}
		if ok && !l.Expired(now) {
			out = append(out, l)
		}
	}
	return out, nil
}
