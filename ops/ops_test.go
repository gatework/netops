package ops_test

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/baseline"
	"github.com/gatework/netops/batch"
	"github.com/gatework/netops/cache"
	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/governance"
	"github.com/gatework/netops/health"
	"github.com/gatework/netops/internal/fake"
	"github.com/gatework/netops/ops"
	"github.com/gatework/netops/rollout"
	"github.com/gatework/netops/store/archive"
	"github.com/gatework/netops/store/snapshot"
)

var t0 = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func devices(n int) []device.Device {
	out := make([]device.Device, n)
	for i := range n {
		out[i] = device.Device{ID: "sw" + string(rune('1'+i)), Mgmt: netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)}), Role: "leaf"}
	}
	return out
}

func lab(n int, config ...string) (*fake.Dialer, map[string]*fake.Device) {
	devs := map[string]*fake.Device{}
	for _, d := range devices(n) {
		devs[d.ID] = fake.NewDevice(d.ID, config...)
		devs[d.ID].Answer("show clients", "3 clients")
	}
	f := fake.NewDialer(devs)
	f.Now = func() time.Time { return t0 }
	return f, devs
}

var probes = ops.ProbeTable{"clients": "show clients", "config": fake.ShowConfig}

func TestCollectAndRefresh(t *testing.T) {
	t.Parallel()
	dialer, devs := lab(3, "hostname x")
	devs["sw3"].Unreachable = true
	snaps, err := snapshot.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	clock := func() time.Time { return now }
	dialer.Now = clock
	var seen sync.Map
	rep, err := ops.Collect(t.Context(), ops.CollectRequest{
		Devices: devices(3), Dimensions: []evidence.Dimension{"clients"}, Probes: probes,
		Dialer: dialer, Snapshots: snaps, Now: clock,
		OnCapture: func(_ context.Context, d device.Device, c capture.Capture) error {
			seen.Store(d.ID, len(c.Outputs))
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := rep.Summary(); s.Counts[batch.StateSucceeded] != 2 || s.Counts[batch.StateFailed] != 1 {
		t.Fatalf("summary %+v", s)
	}
	if n, ok := seen.Load("sw1"); !ok || n != 1 {
		t.Errorf("OnCapture saw %v", n)
	}
	policy := cache.Policy{Mode: cache.ModeAuto, DefaultTTL: 5 * time.Minute, Backoff: 10 * time.Minute}
	now = t0.Add(time.Minute)
	opens := dialer.Opens()
	res, err := ops.Refresh(t.Context(), ops.RefreshRequest{
		Devices: devices(3), Dimensions: []evidence.Dimension{"clients"}, Probes: probes,
		Dialer: dialer, Snapshots: snaps, Policy: policy, Now: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dialer.Opens() != opens {
		t.Error("fresh devices or a backing-off device were contacted")
	}
	if len(res.Captures) != 2 || res.Freshness[2].Skipped != cache.SkipBackoff || res.Freshness[2].Failure == "" {
		t.Errorf("refresh %+v", res.Freshness)
	}
	now = t0.Add(20 * time.Minute)
	res, _ = ops.Refresh(t.Context(), ops.RefreshRequest{
		Devices: devices(3), Dimensions: []evidence.Dimension{"clients"}, Probes: probes,
		Dialer: dialer, Snapshots: snaps, Policy: policy, Now: clock,
	})
	if !res.Freshness[0].Refreshed || len(res.Rounds) != 1 {
		t.Errorf("stale devices not refreshed: %+v", res.Freshness)
	}
}

func TestBackupCompareCompliance(t *testing.T) {
	t.Parallel()
	dialer, devs := lab(1, "hostname sw1", "snmp-server community s3cret RO", "telnet server enable")
	arch, _ := archive.Open(t.TempDir())
	req := ops.BackupRequest{
		Devices: devices(1), Dialer: dialer, Archive: arch,
		Command: func(device.Device) string { return fake.ShowConfig },
		Dialect: func(device.Device) config.Dialect { return config.Cisco() },
		Now:     func() time.Time { return t0 },
	}
	rep, err := ops.Backup(t.Context(), req)
	if err != nil || !rep.Results[0].Value.Changed {
		t.Fatalf("first backup %+v %v", rep.Results, err)
	}
	devs["sw1"].SetConfig("hostname sw1", "snmp-server community n3w RO")
	dialer.Now = func() time.Time { return t0.Add(time.Hour) }
	rep, _ = ops.Backup(t.Context(), req)
	r := rep.Results[0].Value
	if !r.Changed || r.Previous == nil {
		t.Fatalf("second backup %+v", r)
	}
	before, _ := arch.Get(t.Context(), *r.Previous)
	after, _ := arch.Get(t.Context(), r.Version)
	cmp, err := ops.Compare(t.Context(), before, after, "v1", "v2", config.Cisco())
	if err != nil {
		t.Fatal(err)
	}
	if !cmp.Changed || strings.Contains(cmp.Unified, "s3cret") || strings.Contains(cmp.Unified, "n3w") || len(cmp.Changes) != 3 {
		t.Errorf("comparison leaked or missed changes:\n%s\n%+v", cmp.Unified, cmp.Changes)
	}
	profile, _ := baseline.Decode([]byte("default:\n  rules:\n    - {id: no-telnet, title: no telnet, forbid: ['^telnet server enable'], severity: high}\n"))
	fs, err := ops.Compliance(t.Context(), devices(1)[0], before, config.Cisco(), profile)
	if err != nil || len(fs) != 1 || fs[0].Rule != "baseline.no_telnet" {
		t.Errorf("compliance %+v %v", fs, err)
	}
}

type journals struct {
	mu   sync.Mutex
	recs map[string][]apply.Record
}

func (j *journals) For(p *change.Plan[fake.Config]) apply.Journal {
	return writer{j: j, key: p.Meta().ID + "/" + p.Target().ID}
}

func (j *journals) last(key string) apply.Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	rs := j.recs[key]
	return rs[len(rs)-1]
}

type writer struct {
	j   *journals
	key string
}

func (w writer) Write(_ context.Context, r apply.Record) error {
	w.j.mu.Lock()
	defer w.j.mu.Unlock()
	if w.j.recs == nil {
		w.j.recs = map[string][]apply.Record{}
	}
	w.j.recs[w.key] = append(w.j.recs[w.key], r)
	return nil
}

func plans(t *testing.T, n int, line string, devs map[string]*fake.Device) []*change.Plan[fake.Config] {
	t.Helper()
	var out []*change.Plan[fake.Config]
	for _, d := range devices(n) {
		p, err := change.NewPlan(change.Meta{ID: "chg", Created: t0, Author: "alice"}, d,
			fake.Config(devs[d.ID].Config()), change.Change[fake.Config](fake.AddLine{Line: line}), fake.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func healthOf(ctx context.Context, s ops.Session, d device.Device) (health.Snapshot, error) {
	c, err := s.Collect(ctx, []string{"show bgp"})
	if err != nil {
		return health.Snapshot{}, err
	}
	cov, _ := evidence.NewCoverage(map[evidence.Dimension]evidence.Status{"bgp": evidence.StatusComplete})
	var facts []health.Fact
	if o, ok := c.Answer("show bgp"); ok {
		lines, _ := o.Body(ctx, nil)
		for _, l := range lines {
			if k, v, ok := strings.Cut(l.Text, " "); ok {
				facts = append(facts, health.Fact{Dimension: "bgp", Key: k, State: v})
			}
		}
	}
	return health.NewSnapshot(cov, facts), nil
}

func TestDeploy(t *testing.T) {
	t.Parallel()
	base := []string{"hostname sw"}
	tests := []struct {
		name       string
		mode       apply.Mode
		prepare    func(devs map[string]*fake.Device)
		checkpoint bool
		gov        *ops.Governance
		succeeded  bool
		stopped    bool
		check      func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, j *journals)
	}{
		{name: "verified and saved, one session per device", succeeded: true, check: func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, _ *journals) {
			t.Helper()
			for _, d := range devs {
				if !slices.Contains(d.Saved(), "ntp 1") {
					t.Errorf("%s not saved", d.Name)
				}
			}
		}},
		{name: "idempotent re-run", succeeded: true, prepare: func(devs map[string]*fake.Device) {
			devs["sw1"].SetConfig("hostname sw", "ntp 1")
		}, check: func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, _ *journals) {
			t.Helper()
			if len(devs["sw1"].Writes()) != 0 || rep.Waves[0].Outcomes[0].Record.Status != apply.StatusSatisfied {
				t.Error("satisfied device was written")
			}
		}},
		{name: "drift on the canary stops the rollout", stopped: true, prepare: func(devs map[string]*fake.Device) {
			devs["sw1"].SetConfig("hostname changed-out-of-band")
		}, check: func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, _ *journals) {
			t.Helper()
			if len(rep.Pending) != 2 || len(devs["sw2"].Writes()) != 0 {
				t.Errorf("rollout continued: pending %d", len(rep.Pending))
			}
		}},
		{name: "health regression rolls back through the inverse", stopped: true, prepare: func(devs map[string]*fake.Device) {
			for _, d := range devs {
				d.Answer("show bgp", "192.0.2.254 established")
				d.AfterWrite = func(d *fake.Device) {
					if slices.Contains(d.Config(), "ntp 1") {
						d.Answer("show bgp", "192.0.2.254 idle")
					} else {
						d.Answer("show bgp", "192.0.2.254 established")
					}
				}
			}
		}, check: func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, j *journals) {
			t.Helper()
			o := rep.Waves[0].Outcomes[0]
			if !o.RolledBack || o.Health.Pass || slices.Contains(devs["sw1"].Config(), "ntp 1") || o.Saved {
				t.Errorf("not rolled back: %+v config %v", o, devs["sw1"].Config())
			}
			if j.last("chg-rollback/sw1").Status != apply.StatusVerified {
				t.Error("rollback not journaled as verified")
			}
		}},
		{name: "health regression rolls back through the checkpoint", checkpoint: true, stopped: true, prepare: func(devs map[string]*fake.Device) {
			for _, d := range devs {
				d.Checkpoints = true
				d.Answer("show bgp", "192.0.2.254 established")
				d.AfterWrite = func(d *fake.Device) { d.Answer("show bgp", "192.0.2.254 idle") }
			}
		}, check: func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, _ *journals) {
			t.Helper()
			o := rep.Waves[0].Outcomes[0]
			if !o.RolledBack || o.Checkpoint == "" || slices.Contains(devs["sw1"].Config(), "ntp 1") {
				t.Errorf("checkpoint rollback %+v", o)
			}
		}},
		{name: "dry run writes nothing", mode: apply.ModeDryRun, succeeded: true, check: func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, _ *journals) {
			t.Helper()
			for _, d := range devs {
				if len(d.Writes()) != 0 || len(d.Saved()) != 0 {
					t.Errorf("%s written in a dry run", d.Name)
				}
			}
		}},
		{name: "governance denial touches nothing", gov: &ops.Governance{
			Policy:  governance.Policy{RequireTicket: true},
			Request: governance.Request{Class: governance.ClassNormal, Now: t0},
		}, check: func(t *testing.T, rep ops.DeployReport, devs map[string]*fake.Device, _ *journals) {
			t.Helper()
			if rep.Gate == nil || rep.Gate.Allowed || len(rep.Waves) != 0 {
				t.Errorf("gate %+v", rep.Gate)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dialer, devs := lab(3, base...)
			for _, d := range devs {
				d.Answer("show bgp", "")
			}
			ps := plans(t, 3, "ntp 1", devs)
			if tc.prepare != nil {
				tc.prepare(devs)
			}
			j := &journals{}
			rep, _ := ops.Deploy(t.Context(), ops.DeployRequest[fake.Config]{
				Plans: ps, Dialer: dialer, Read: fake.Read, Journal: j.For,
				Health: healthOf, HealthPolicy: health.Policy{
					Dimensions: []evidence.Dimension{"bgp"},
					Healthy:    map[evidence.Dimension][]string{"bgp": {"established"}},
				},
				Governance: tc.gov, Strategy: rollout.Strategy{Canary: 1, WaveSize: 2},
				Mode: tc.mode, Checkpoint: tc.checkpoint, Rollback: true, Save: true,
				Now: func() time.Time { return t0 },
			})
			if rep.Succeeded() != tc.succeeded || rep.Stopped != tc.stopped {
				t.Errorf("succeeded %v stopped %v: %+v", rep.Succeeded(), rep.Stopped, rep)
			}
			if tc.gov == nil && tc.succeeded && dialer.Opens() != 3 {
				t.Errorf("sessions opened = %d, want one per device", dialer.Opens())
			}
			tc.check(t, rep, devs, j)
		})
	}
}

// configDialer hands out sessions that read the configuration themselves.
type configDialer struct {
	text string
	err  error
}

type configSession struct {
	ops.Session
	d *configDialer
}

func (s configSession) Config(context.Context) (string, error) { return s.d.text, s.d.err }

func (d *configDialer) CanConnect(context.Context, device.Device) bool { return true }

func (d *configDialer) Open(ctx context.Context, _ device.Device, fn func(context.Context, ops.Session) error) (error, error) {
	return fn(ctx, configSession{d: d}), nil
}

// Backup prefers the session's own configuration reader, so platform
// guards and completeness checks apply; its refusal fails the backup.
func TestBackupPrefersConfigReader(t *testing.T) {
	t.Parallel()
	dev := device.Device{ID: "sw1", Mgmt: netip.MustParseAddr("192.0.2.1"), Platform: "cisco_ios"}.Normalize()
	req := func(d ops.Dialer, a ops.Archive) ops.BackupRequest {
		return ops.BackupRequest{
			Devices: []device.Device{dev}, Dialer: d, Archive: a,
			Command: func(device.Device) string { t.Error("Command used despite ConfigReader"); return "" },
			Dialect: func(device.Device) config.Dialect { return config.Cisco() },
		}
	}
	a, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := ops.Backup(t.Context(), req(&configDialer{text: "hostname sw1\n!\nend"}, a))
	if err != nil || len(rep.Results) != 1 || rep.Results[0].Err != nil {
		t.Fatalf("%+v %v", rep, err)
	}
	rep, _ = ops.Backup(t.Context(), req(&configDialer{err: failure.New(failure.CodeIncomplete, "x").Because("config_truncated")}, a))
	if len(rep.Results) != 1 || failure.Primary(rep.Results[0].Err) != failure.CodeIncomplete {
		t.Fatalf("truncated: %+v", rep)
	}
}
