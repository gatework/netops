package cache_test

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/gatework/netops/cache"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
)

var now = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func policy(m cache.Mode) cache.Policy {
	return cache.Policy{Mode: m, TTL: map[evidence.Dimension]time.Duration{"clients": 5 * time.Minute}, DefaultTTL: time.Hour, Backoff: 2 * time.Minute}
}

func TestPolicy_Decide(t *testing.T) {
	t.Parallel()
	d := device.Device{ID: "ac1", Mgmt: netip.MustParseAddr("192.0.2.1")}
	fresh := map[evidence.Dimension]time.Time{"clients": now.Add(-time.Minute), "aps": now.Add(-time.Minute)}
	stale := map[evidence.Dimension]time.Time{"clients": now.Add(-10 * time.Minute), "aps": now.Add(-time.Minute)}
	failed := cache.Status{}.Attempted(now.Add(-time.Minute), errors.New("x"))
	wanted := []evidence.Dimension{"clients", "aps"}
	tests := []struct {
		name  string
		mode  cache.Mode
		rec   map[evidence.Dimension]time.Time
		st    cache.Status
		conn  bool
		fetch bool
		skip  cache.Skip
		stale int
	}{
		{"fresh reused", cache.ModeAuto, fresh, cache.Status{}, true, false, cache.SkipNone, 0},
		{"stale fetched", cache.ModeAuto, stale, cache.Status{}, true, true, cache.SkipNone, 1},
		{"live fetches all", cache.ModeLive, fresh, cache.Status{}, true, true, cache.SkipNone, 2},
		{"offline skips", cache.ModeOffline, stale, cache.Status{}, true, false, cache.SkipOffline, 1},
		{"no credentials", cache.ModeAuto, nil, cache.Status{}, false, false, cache.SkipNoCredentials, 2},
		{"backoff", cache.ModeAuto, stale, failed, true, false, cache.SkipBackoff, 1},
	}
	for _, tc := range tests {
		got := policy(tc.mode).Decide(now, cache.Entry{Device: d, Recorded: tc.rec, Status: tc.st, CanConnect: tc.conn}, wanted)
		if got.Fetch != tc.fetch || got.Skip != tc.skip || len(got.Stale) != tc.stale {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	if (cache.Policy{Mode: "sometimes", DefaultTTL: time.Hour}).Validate() == nil {
		t.Error("invalid mode accepted")
	}
}

func ExampleGroups() {
	mk := func(id, ip string) device.Device { return device.Device{ID: id, Mgmt: netip.MustParseAddr(ip)} }
	gs := cache.Groups([]cache.Decision{
		{Device: mk("b", "192.0.2.2"), Fetch: true, Stale: []evidence.Dimension{"clients"}},
		{Device: mk("a", "192.0.2.1"), Fetch: true, Stale: []evidence.Dimension{"clients"}},
		{Device: mk("c", "192.0.2.3"), Fetch: true, Stale: []evidence.Dimension{"clients", "aps"}},
	})
	for _, g := range gs {
		fmt.Println(g.Dimensions, len(g.Devices))
	}
	// Output:
	// [aps clients] 1
	// [clients] 2
}
