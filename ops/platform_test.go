package ops_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gatework/netops/apply"
	"github.com/gatework/netops/authz"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/internal/fake"
	"github.com/gatework/netops/lease"
	"github.com/gatework/netops/ops"
	"github.com/gatework/netops/rollout"
	"github.com/gatework/netops/store/archive"
	filelease "github.com/gatework/netops/store/lease"
)

func notifier(t *testing.T) (ops.Notify, *event.Memory) {
	t.Helper()
	var mem event.Memory
	bus, err := event.NewBus(event.Route{Name: "mem", Sink: &mem})
	if err != nil {
		t.Fatal(err)
	}
	return ops.Notify{Publisher: bus, Tool: "test", OnError: func(err error) { t.Errorf("delivery: %v", err) }}, &mem
}

func kinds(es []event.Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, string(e.Kind))
	}
	return out
}

func TestDeploy_authorizeLeaseNotify(t *testing.T) {
	t.Parallel()
	dialer, devs := lab(2, "hostname sw")
	for _, d := range devs {
		d.Answer("show bgp", "")
	}
	ps := plans(t, 2, "ntp 1", devs)
	pol, err := authz.Decode([]byte("roles: [{name: ops, permissions: [deploy], scope: ['id=sw1']}]\nbindings: {alice: [ops]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	j := &journals{}
	base := ops.DeployRequest[fake.Config]{
		Plans: ps, Dialer: dialer, Read: fake.Read, Journal: j.For,
		Strategy: rollout.Strategy{WaveSize: 2}, Now: func() time.Time { return t0 },
	}

	denied := base
	denied.Authorize = func(d device.Device) error { return pol.Authorize("alice", authz.PermDeploy, d) }
	rep, err := ops.Deploy(t.Context(), denied)
	if !errors.Is(err, ops.ErrUnauthorized) || len(rep.Denied) != 1 || dialer.Opens() != 0 {
		t.Fatalf("authorization: %v %v opens=%d", err, rep.Denied, dialer.Opens())
	}

	st, _ := filelease.Open(t.TempDir())
	leases := lease.NewManager(st, func() time.Time { return t0 })
	other, err := leases.Acquire(t.Context(), []string{lease.Key(devices(2)[1])}, lease.Holder{Tool: "firemon", Actor: "bob"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	notify, mem := notifier(t)
	busy := base
	busy.Leases, busy.Holder, busy.Notify = leases, lease.Holder{Tool: "fabric", Actor: "alice"}, notify
	rep, _ = ops.Deploy(t.Context(), busy)
	o := rep.Waves[0].Outcomes
	if rep.Succeeded() || len(o) != 2 || o[0].Codes[0] != failure.CodeBusy || dialer.Opens() != 0 {
		t.Fatalf("lease conflict: %+v opens=%d", rep, dialer.Opens())
	}
	if got := kinds(mem.Events()); len(got) != 1 || got[0] != string(event.KindLeaseConflict) {
		t.Errorf("events %v", got)
	}
	_ = leases.Release(t.Context(), other)

	notify, mem = notifier(t)
	busy.Notify = notify
	rep, err = ops.Deploy(t.Context(), busy)
	if err != nil || !rep.Succeeded() {
		t.Fatalf("deploy after release: %v %+v", err, rep)
	}
	if got := kinds(mem.Events()); len(got) != 2 || got[0] != string(event.KindChangeExecuted) {
		t.Errorf("events %v", got)
	}
	if hs, _ := leases.Holders(t.Context(), []string{lease.Key(devices(2)[0]), lease.Key(devices(2)[1])}); len(hs) != 0 {
		t.Errorf("leases not released: %+v", hs)
	}
	if j.last("chg/sw1").Status != apply.StatusVerified {
		t.Error("journal")
	}
}

func TestCheckSyncAndBackupEvents(t *testing.T) {
	t.Parallel()
	dialer, devs := lab(2, "hostname sw", "ntp server 192.0.2.1")
	arch, _ := archive.Open(t.TempDir())
	notify, mem := notifier(t)
	backup := ops.BackupRequest{
		Devices: devices(2), Dialer: dialer, Archive: arch, Notify: notify,
		Command: func(device.Device) string { return fake.ShowConfig },
		Dialect: func(device.Device) config.Dialect { return config.Cisco() },
		Now:     func() time.Time { return t0 },
	}
	if _, err := ops.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	sync := ops.CheckSyncRequest{
		Devices: devices(2), Dialer: dialer, Versions: arch, Notify: notify,
		Command: backup.Command, Dialect: backup.Dialect, Now: func() time.Time { return t0.Add(time.Hour) },
	}
	devs["sw2"].SetConfig("hostname sw", "ntp server 192.0.2.9", "snmp-server community x RO")
	rep, err := ops.CheckSync(t.Context(), sync)
	if err != nil {
		t.Fatal(err)
	}
	if s := rep.Results[0].Value; s.Status != ops.SyncInSync {
		t.Errorf("sw1 %+v", s)
	}
	if s := rep.Results[1].Value; s.Status != ops.SyncOutOfSync || s.Added != 2 || s.Removed != 1 || s.Latest.Seq != 1 {
		t.Errorf("sw2 %+v", s)
	}
	if v, _ := arch.Versions(t.Context(), "sw2"); len(v) != 1 {
		t.Error("check sync wrote the archive")
	}
	backup.Now = func() time.Time { return t0.Add(2 * time.Hour) }
	dialer.Now = backup.Now
	if _, err := ops.Backup(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(kinds(mem.Events()), ",")
	if got != "config.out_of_sync,config.changed" {
		t.Errorf("events %s", got)
	}
	empty, _ := archive.Open(t.TempDir())
	sync.Versions = empty
	rep, _ = ops.CheckSync(t.Context(), sync)
	if rep.Results[0].Value.Status != ops.SyncNoBaseline {
		t.Errorf("no baseline: %+v", rep.Results[0].Value)
	}
}
