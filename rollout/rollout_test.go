package rollout_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/rollout"
)

func devs() []device.Device {
	mk := func(id, ip, role, pair string) device.Device {
		return device.Device{ID: id, Mgmt: netip.MustParseAddr(ip), Role: role, Labels: map[string]string{"pair": pair}}
	}
	return []device.Device{
		mk("core1", "192.0.2.1", "core", "c"), mk("core2", "192.0.2.2", "core", "c"),
		mk("acc1", "192.0.2.11", "access", "a1"), mk("acc2", "192.0.2.12", "access", "a1"),
		mk("acc3", "192.0.2.13", "access", ""), mk("acc4", "192.0.2.14", "access", ""),
	}
}

func TestSchedule(t *testing.T) {
	t.Parallel()
	waves, err := rollout.Schedule(devs(), rollout.Strategy{
		Canary: 1, WaveSize: 3, Tiers: []string{"access", "core"},
		AntiAffinity: func(d device.Device) string { return d.Labels["pair"] },
	})
	if err != nil {
		t.Fatal(err)
	}
	var got [][]string
	for _, w := range waves {
		var ids []string
		for _, d := range w.Devices {
			ids = append(ids, d.ID)
		}
		got = append(got, ids)
	}
	want := fmt.Sprint([][]string{{"acc1"}, {"acc2", "acc3", "acc4"}, {"core1"}, {"core2"}})
	if fmt.Sprint(got) != want {
		t.Errorf("waves %v, want %v", got, want)
	}
}

func TestTracker(t *testing.T) {
	t.Parallel()
	tr := rollout.NewTracker(rollout.Strategy{MaxFailures: 1})
	if !tr.Finish(1) || tr.Finish(1) || !tr.Stopped() {
		t.Error("failure budget not applied")
	}
}

func ExampleSchedule() {
	waves, _ := rollout.Schedule(devs()[:2], rollout.Strategy{AntiAffinity: func(d device.Device) string { return d.Labels["pair"] }})
	for _, w := range waves {
		fmt.Println(w.Index, w.Label, len(w.Devices))
	}
	// Output:
	// 1 core 1
	// 2 core 1
}
