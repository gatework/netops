package event_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/event"
)

var t0 = time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)

func mk(t *testing.T, kind event.Kind, sev assess.Severity, dev string) event.Event {
	t.Helper()
	e, err := event.New(event.Event{Kind: kind, Severity: sev, At: t0, Device: dev, Summary: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestNew(t *testing.T) {
	t.Parallel()
	a := mk(t, event.KindConfigChanged, assess.SeverityLow, "sw1")
	b := mk(t, event.KindConfigChanged, assess.SeverityLow, "sw1")
	c := mk(t, event.KindConfigChanged, assess.SeverityLow, "sw2")
	if a.ID != b.ID || a.ID == c.ID {
		t.Error("ID is not derived from content")
	}
	for _, bad := range []event.Event{{Kind: "nodot", Severity: assess.SeverityLow, At: t0, Summary: "x"},
		{Kind: event.KindJobFailed, At: t0, Summary: "x"}, {Kind: event.KindJobFailed, Severity: assess.SeverityLow, Summary: "x"}} {
		if _, err := event.New(bad); !errors.Is(err, event.ErrInvalid) {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestBus_routesAndFailures(t *testing.T) {
	t.Parallel()
	inv, _ := device.New([]device.Device{{ID: "sw1", Mgmt: netip.MustParseAddr("192.0.2.1"), Site: "dc1"},
		{ID: "sw2", Mgmt: netip.MustParseAddr("192.0.2.2"), Site: "dc2"}})
	dc1, _ := device.ParseSelector("site=dc1")
	var all, high, site event.Memory
	broken := event.SinkFunc(func(context.Context, []event.Event) error { return errors.New("down") })
	bus, err := event.NewBus(
		event.Route{Name: "all", Sink: &all},
		event.Route{Name: "high", Sink: &high, Filter: event.Filter{MinSeverity: assess.SeverityHigh}},
		event.Route{Name: "dc1", Sink: &site, Filter: event.Filter{Devices: dc1, Inventory: inv}},
		event.Route{Name: "pager", Sink: broken, Filter: event.Filter{Kinds: []event.Kind{event.KindChangeUncertain}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = bus.Publish(t.Context(),
		mk(t, event.KindConfigChanged, assess.SeverityLow, "sw1"),
		mk(t, event.KindChangeUncertain, assess.SeverityCritical, "sw2"))
	var de *event.DeliveryError
	if !errors.As(err, &de) || len(de.Failed) != 1 || de.Failed["pager"] == nil {
		t.Errorf("delivery error %v", err)
	}
	if len(all.Events()) != 2 || len(high.Events()) != 1 || len(site.Events()) != 1 || site.Events()[0].Device != "sw1" {
		t.Errorf("routing all=%d high=%d site=%d", len(all.Events()), len(high.Events()), len(site.Events()))
	}
	if err := event.Emit(t.Context(), nil, mk(t, event.KindJobFailed, assess.SeverityLow, "")); err != nil {
		t.Error("nil publisher is not a discard")
	}
}

func ExampleBus_Publish() {
	var sink event.Memory
	bus, _ := event.NewBus(event.Route{Name: "audit", Sink: &sink})
	e, _ := event.New(event.Event{
		Kind: event.KindConfigOutOfSync, Severity: assess.SeverityMedium, At: t0,
		Source: "fabric", Device: "leaf1", Summary: "running configuration differs from version 12",
		Fields: event.Fields("added", event.Int(3), "removed", event.Int(1)),
	})
	_ = bus.Publish(context.Background(), e)
	got := sink.Events()[0]
	added, _ := got.Field("added")
	fmt.Println(got.Kind, got.Device, added)
	// Output: config.out_of_sync leaf1 3
}
