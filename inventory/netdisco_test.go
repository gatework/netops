package inventory_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/inventory"
	"github.com/gatework/netops/secret"
)

// classifier is a minimal stand-in for net-connector's driver registry.
type classifier struct{}

func (classifier) Platforms() []string { return []string{"h3c", "cisco_ios", "cisco_nxos"} }

func (classifier) Classify(id device.Identity) (string, bool) {
	switch n := id.Normalized(); {
	case strings.Contains(n.Vendor, "h3c"):
		return "h3c", true
	case strings.Contains(n.Vendor, "cisco"):
		return "cisco_ios", true
	}
	return "", false
}

var rows = []inventory.Row{
	{IP: "192.0.2.1", Name: "core-1", Vendor: "H3C", Model: "S12508"},
	{IP: "192.0.2.2", DNS: "leaf-2", Vendor: "Cisco", OS: "nx-os", Model: "N9K-C93180"},
	{IP: "192.0.2.3", Name: "edge-3", Vendor: "Cisco", Model: "ISR4451"},
	{IP: "198.51.100.9", Name: "unknown", Vendor: "Acme"},
	{IP: "not-an-ip", Name: "broken", Vendor: "H3C"},
}

// netdisco serves rows in pages of two and counts device requests.
func netdisco(t *testing.T, hits *atomic.Int32, fail *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Apikey k1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		hits.Add(1)
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		end := min(off+lim, len(rows))
		page := []inventory.Row{}
		if off < len(rows) {
			page = rows[off:end]
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func source(url, dir string, mode inventory.CacheMode, now func() time.Time) inventory.Netdisco {
	return inventory.Netdisco{
		Config: inventory.NetdiscoConfig{
			URL: url, AllowInsecureHTTP: true, CacheDirectory: dir, CacheMode: string(mode),
			DeviceRules: []inventory.DeviceRule{{Vendor: "cisco", OS: new("nx os"), Platform: "cisco_nxos"}},
			Site:        "dc1",
		},
		Credentials: inventory.NetdiscoCredentials{APIKey: secret.New("k1")},
		Classifier:  classifier{},
		Now:         now,
	}
}

func TestNetdiscoClassifiesAndReports(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	var fail atomic.Bool
	srv := netdisco(t, &hits, &fail)
	n := source(srv.URL, "", "", nil)
	devs, rep, err := n.Devices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range devs {
		got[d.Mgmt.String()] = d.Platform + "/" + d.Name + "/" + d.Site
	}
	want := map[string]string{
		"192.0.2.1": "h3c/core-1/dc1",
		"192.0.2.2": "cisco_nxos/leaf-2/dc1", // device rule beats the classifier; DNS fills the name
		"192.0.2.3": "cisco_ios/edge-3/dc1",
	}
	if len(got) != len(want) {
		t.Fatalf("devices = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if rep.Rows != len(rows) || rep.Dropped[inventory.DropUnsupported] != 1 || rep.Dropped[inventory.DropInvalidAddress] != 1 {
		t.Errorf("report = %+v", rep)
	}
}

func TestNetdiscoFilters(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	var fail atomic.Bool
	srv := netdisco(t, &hits, &fail)
	n := source(srv.URL, "", "", nil)
	n.Config.IncludeHosts = []string{"192.0.2.0/30"}
	n.Config.ExcludeNames = []string{"core-*"}
	n.Config.NameMatch = inventory.MatchGlob
	n.Config.ExcludePlatforms = []string{"cisco_ios"}
	devs, rep, err := n.Devices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].Name != "leaf-2" {
		t.Fatalf("devices = %v", devs)
	}
	if rep.Dropped[inventory.DropFiltered] != 3 {
		t.Errorf("report = %+v", rep)
	}
}

func TestNetdiscoRejectsUnknownPlatform(t *testing.T) {
	t.Parallel()
	n := source("https://netdisco.example.invalid", "", "", nil)
	n.Config.HostOverrides = map[string]string{"192.0.2.1": "nope"}
	if _, _, err := n.Devices(t.Context()); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestNetdiscoCache(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	var fail atomic.Bool
	srv := netdisco(t, &hits, &fail)
	dir := t.TempDir() + "/cache"
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	if _, _, err := source(srv.URL, dir, inventory.CacheOnly, clock).Devices(t.Context()); !errors.Is(err, inventory.ErrCacheEmpty) {
		t.Fatalf("cache-only before refresh: %v", err)
	}
	if _, rep, err := source(srv.URL, dir, inventory.CachePrefer, clock).Devices(t.Context()); err != nil || rep.Stale {
		t.Fatalf("first fetch: %v %+v", err, rep)
	}
	first := hits.Load()
	if _, _, err := source(srv.URL, dir, inventory.CachePrefer, clock).Devices(t.Context()); err != nil || hits.Load() != first {
		t.Fatalf("fresh cache refetched: %v hits %d→%d", err, first, hits.Load())
	}

	now = now.Add(73 * time.Hour) // past the default TTL
	fail.Store(true)
	devs, rep, err := source(srv.URL, dir, inventory.CachePrefer, clock).Devices(t.Context())
	if err != nil || !rep.Stale || len(devs) != 3 {
		t.Fatalf("stale fallback: %v %+v %d", err, rep, len(devs))
	}
	if _, rep, err := source(srv.URL, dir, inventory.CacheOnly, clock).Devices(t.Context()); err != nil || !rep.Stale {
		t.Fatalf("cache-only stale: %v %+v", err, rep)
	}
	other := source(srv.URL, dir, inventory.CacheOnly, clock)
	other.Credentials.APIKey = secret.New("k2") // a different account never reads k1's cache
	if _, _, err := other.Devices(t.Context()); !errors.Is(err, inventory.ErrCacheEmpty) {
		t.Fatalf("cache leaked across principals: %v", err)
	}
}
