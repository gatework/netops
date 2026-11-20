package cache

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
)

// ErrInvalid reports an invalid policy.
var ErrInvalid = errors.New("cache: invalid policy")

// Mode selects how a query treats the cache.
type Mode string

// Cache modes.
const (
	ModeAuto    Mode = "auto"    // fetch stale dimensions, reuse fresh ones
	ModeLive    Mode = "live"    // fetch everything
	ModeOffline Mode = "offline" // never connect
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeAuto || m == ModeLive || m == ModeOffline }

// Policy is the freshness policy.
type Policy struct {
	Mode       Mode
	TTL        map[evidence.Dimension]time.Duration
	DefaultTTL time.Duration
	// Backoff is how long to wait after a failed attempt before trying the
	// same device again.
	Backoff time.Duration
}

// Validate checks the policy.
func (p Policy) Validate() error {
	if !p.Mode.Valid() || p.DefaultTTL <= 0 || p.Backoff < 0 {
		return fmt.Errorf("%w: mode %q ttl %s backoff %s", ErrInvalid, p.Mode, p.DefaultTTL, p.Backoff)
	}
	for _, d := range slices.Sorted(maps.Keys(p.TTL)) {
		if p.TTL[d] <= 0 {
			return fmt.Errorf("%w: ttl of %s", ErrInvalid, d)
		}
	}
	return nil
}

// TTLOf returns the time to live of dim.
func (p Policy) TTLOf(dim evidence.Dimension) time.Duration {
	if t, ok := p.TTL[dim]; ok {
		return t
	}
	return p.DefaultTTL
}

// Status is the refresh history of one device.
type Status struct {
	LastAttempt time.Time    `json:"last_attempt"`
	LastSuccess time.Time    `json:"last_success,omitzero"`
	Failure     failure.Code `json:"failure,omitempty"`
}

// Failed reports whether the last attempt failed.
func (s Status) Failed() bool { return s.Failure != failure.CodeUnset }

// Attempted returns the status after an attempt at time at.
func (s Status) Attempted(at time.Time, err error) Status {
	s.LastAttempt, s.Failure = at, failure.CodeUnset
	if err != nil {
		s.Failure = failure.Primary(err)
	} else {
		s.LastSuccess = at
	}
	return s
}

// Skip explains why stale evidence was not fetched.
type Skip string

// Skip reasons.
const (
	SkipNone          Skip = ""
	SkipOffline       Skip = "offline"
	SkipNoCredentials Skip = "no_credentials"
	SkipBackoff       Skip = "backoff"
)

// Entry is what the cache knows about one device.
type Entry struct {
	Device device.Device
	// Recorded is when each dimension was last recorded; absent means never.
	Recorded map[evidence.Dimension]time.Time
	Status   Status
	// CanConnect is false when no credentials are configured.
	CanConnect bool
}

// Decision is what to do for one device.
type Decision struct {
	Device device.Device
	Stale  []evidence.Dimension
	Fetch  bool
	Skip   Skip
}

// Decide returns the stale wanted dimensions of e at now and whether to
// fetch them.
func (p Policy) Decide(now time.Time, e Entry, wanted []evidence.Dimension) Decision {
	d := Decision{Device: e.Device, Stale: p.Stale(now, e.Recorded, wanted)}
	if p.Mode == ModeLive {
		d.Stale = slices.Clone(wanted)
	}
	switch {
	case len(d.Stale) == 0:
	case p.Mode == ModeOffline:
		d.Skip = SkipOffline
	case !e.CanConnect:
		d.Skip = SkipNoCredentials
	case p.Mode != ModeLive && e.Status.Failed() && now.Sub(e.Status.LastAttempt) < p.Backoff:
		d.Skip = SkipBackoff
	default:
		d.Fetch = true
	}
	return d
}

// Stale returns the wanted dimensions older than their TTL or never
// recorded, in wanted order.
func (p Policy) Stale(now time.Time, recorded map[evidence.Dimension]time.Time, wanted []evidence.Dimension) []evidence.Dimension {
	var out []evidence.Dimension
	for _, dim := range wanted {
		at, ok := recorded[dim]
		if !ok || at.IsZero() || now.Sub(at) > p.TTLOf(dim) {
			out = append(out, dim)
		}
	}
	return out
}

// Group is a set of devices that need the same dimensions.
type Group struct {
	Dimensions []evidence.Dimension
	Devices    []device.Device
}

// Groups batches the decisions to fetch by their stale dimension set.
// Groups are ordered by dimension set and devices by address.
func Groups(decisions []Decision) []Group {
	byKey := map[string]*Group{}
	for _, d := range decisions {
		if !d.Fetch {
			continue
		}
		dims := slices.Clone(d.Stale)
		slices.Sort(dims)
		k := key(dims)
		g := byKey[k]
		if g == nil {
			g = &Group{Dimensions: dims}
			byKey[k] = g
		}
		g.Devices = append(g.Devices, d.Device)
	}
	out := make([]Group, 0, len(byKey))
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		g := byKey[k]
		slices.SortFunc(g.Devices, device.Compare)
		out = append(out, *g)
	}
	return out
}

func key(dims []evidence.Dimension) string {
	s := make([]string, len(dims))
	for i, d := range dims {
		s[i] = string(d)
	}
	return strings.Join(s, ",")
}

// Freshness reports the age of the evidence a query used for one device.
type Freshness struct {
	Device    device.Device        `json:"device"`
	Oldest    time.Time            `json:"oldest,omitzero"`
	Stale     []evidence.Dimension `json:"stale,omitempty"`
	Refreshed bool                 `json:"refreshed,omitempty"`
	Skipped   Skip                 `json:"skipped,omitempty"`
	Failure   failure.Code         `json:"failure,omitempty"`
}

// Fresh returns the freshness of e after a query; refreshed reports
// whether this query fetched it successfully.
func (p Policy) Fresh(now time.Time, e Entry, wanted []evidence.Dimension, d Decision, refreshed bool) Freshness {
	f := Freshness{Device: e.Device, Stale: p.Stale(now, e.Recorded, wanted), Refreshed: refreshed, Skipped: d.Skip}
	for _, dim := range wanted {
		at, ok := e.Recorded[dim]
		if !ok || at.IsZero() {
			f.Oldest = time.Time{}
			break
		}
		if f.Oldest.IsZero() || at.Before(f.Oldest) {
			f.Oldest = at
		}
	}
	if (d.Fetch && !refreshed || d.Skip == SkipBackoff) && e.Status.Failed() {
		f.Failure = e.Status.Failure
	}
	return f
}
