package ops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/batch"
	"github.com/gatework/netops/cache"
	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
)

// CollectRequest collects dimensions from devices into the snapshot store.
type CollectRequest struct {
	Devices    []device.Device
	Dimensions []evidence.Dimension
	Probes     Probes
	Dialer     Dialer
	Snapshots  Snapshots
	Workers    int
	Timeout    time.Duration
	Events     batch.Events[capture.Capture]
	// OnCapture, when set, sees each device's fresh capture before it is
	// merged into the store, for derived records such as sighting logs.
	// Its error fails the device.
	OnCapture func(ctx context.Context, d device.Device, fresh capture.Capture) error
	// Pacing adds retries of transient failures and a per-site cap.
	Pacing Pacing
	// Notify publishes collect.failed for devices that failed.
	Notify Notify
	Now    func() time.Time
}

// Collect opens one session per device, runs the probe commands, merges
// the capture into the store (a partial capture is kept) and records the
// refresh status. Each result's value is the merged capture.
func Collect(ctx context.Context, r CollectRequest) (batch.Report[capture.Capture], error) {
	if r.Dialer == nil || r.Snapshots == nil || r.Probes == nil {
		return batch.Report[capture.Capture]{}, fmt.Errorf("ops: collect: missing dependency")
	}
	now := clock(r.Now)
	rep, err := batch.Run(ctx, r.Devices, pace(r.Pacing, batch.Options[capture.Capture]{
		Workers: r.Workers, Timeout: r.Timeout, Events: r.Events, Now: now,
		Task: func(ctx context.Context, d device.Device) (capture.Capture, error) {
			return collectOne(ctx, r, d, now)
		},
	}))
	for _, res := range rep.Results {
		if res.State == batch.StateFailed {
			r.Notify.emit(ctx, event.KindCollectFailed, assess.SeverityMedium, now(), res.Device,
				"collection of "+res.Device.Label()+" failed", event.Fields("code", string(failure.Primary(res.Err)))...)
		}
	}
	if err != nil {
		return rep, fmt.Errorf("ops: collect: %w", err)
	}
	return rep, nil
}

func collectOne(ctx context.Context, r CollectRequest, d device.Device, now func() time.Time) (capture.Capture, error) {
	started := now().UTC()
	commands := Commands(r.Probes, d, r.Dimensions)
	fresh := capture.Capture{Host: d.Mgmt}
	opErr, closeErr := r.Dialer.Open(ctx, d, func(ctx context.Context, s Session) error {
		c, err := s.Collect(ctx, commands)
		fresh = c
		return err
	})
	err := errors.Join(opErr, closeErr)
	st, _, _ := r.Snapshots.Status(ctx, d.Mgmt)
	statusErr := r.Snapshots.SetStatus(context.WithoutCancel(ctx), d.Mgmt, st.Attempted(started, opErr))
	if fresh.Empty() {
		return capture.Capture{Host: d.Mgmt}, errors.Join(err, statusErr)
	}
	fresh.Host = d.Mgmt
	var hookErr error
	if r.OnCapture != nil {
		hookErr = r.OnCapture(ctx, d, fresh)
	}
	merged, saveErr := r.Snapshots.Merge(context.WithoutCancel(ctx), fresh)
	if saveErr != nil {
		merged = fresh
	}
	return merged, errors.Join(err, saveErr, statusErr, hookErr)
}

// RefreshRequest returns captures fresh enough for a query, collecting
// only what is stale.
type RefreshRequest struct {
	Devices    []device.Device
	Dimensions []evidence.Dimension
	Probes     Probes
	// Dialer may be nil in offline use; devices then report
	// SkipNoCredentials when stale.
	Dialer    Dialer
	Snapshots Snapshots
	Policy    cache.Policy
	Workers   int
	Timeout   time.Duration
	Events    batch.Events[capture.Capture]
	OnCapture func(ctx context.Context, d device.Device, fresh capture.Capture) error
	Now       func() time.Time
}

// Refreshed is the result of Refresh.
type Refreshed struct {
	// Captures are the stored captures of the requested devices, in device
	// order; devices never collected are absent.
	Captures  []capture.Capture
	Freshness []cache.Freshness
	// Rounds are the collection summaries, one per dimension group.
	Rounds []batch.Summary
}

// Refresh decides per device what is stale under the policy, collects the
// stale dimensions in groups and returns the stored captures with their
// freshness. Collection failures fall back to the cached evidence.
func Refresh(ctx context.Context, r RefreshRequest) (Refreshed, error) {
	if r.Snapshots == nil || r.Probes == nil {
		return Refreshed{}, fmt.Errorf("ops: refresh: missing dependency")
	}
	if err := r.Policy.Validate(); err != nil {
		return Refreshed{}, err
	}
	now := clock(r.Now)
	devices := slices.Clone(r.Devices)
	slices.SortFunc(devices, device.Compare)
	entries, decisions, err := r.decide(ctx, devices, now())
	if err != nil {
		return Refreshed{}, err
	}
	var out Refreshed
	refreshed := map[string]bool{}
	for _, g := range cache.Groups(decisions) {
		rep, err := Collect(ctx, CollectRequest{
			Devices: g.Devices, Dimensions: g.Dimensions, Probes: r.Probes, Dialer: r.Dialer,
			Snapshots: r.Snapshots, Workers: r.Workers, Timeout: r.Timeout, Events: r.Events,
			OnCapture: r.OnCapture, Now: now,
		})
		out.Rounds = append(out.Rounds, rep.Summary())
		for _, res := range rep.Results {
			if res.State == batch.StateSucceeded {
				refreshed[res.Device.ID] = true
			}
		}
		if err != nil && ctx.Err() != nil {
			return out, err
		}
	}
	for i, d := range devices {
		c, ok, err := r.Snapshots.Load(ctx, d.Mgmt)
		if err != nil {
			return out, fmt.Errorf("ops: refresh: load %s: %w", d.ID, err)
		}
		e := entries[i]
		if ok {
			out.Captures = append(out.Captures, c)
			e.Recorded = Recorded(r.Probes, d, c, r.Dimensions)
		}
		if st, has, _ := r.Snapshots.Status(ctx, d.Mgmt); has {
			e.Status = st
		}
		out.Freshness = append(out.Freshness, r.Policy.Fresh(now(), e, r.Dimensions, decisions[i], refreshed[d.ID]))
	}
	return out, nil
}

func (r RefreshRequest) decide(ctx context.Context, devices []device.Device, now time.Time) ([]cache.Entry, []cache.Decision, error) {
	entries := make([]cache.Entry, len(devices))
	decisions := make([]cache.Decision, len(devices))
	for i, d := range devices {
		e := cache.Entry{Device: d, CanConnect: r.Dialer != nil && r.Dialer.CanConnect(ctx, d)}
		c, ok, err := r.Snapshots.Load(ctx, d.Mgmt)
		if err != nil {
			return nil, nil, fmt.Errorf("ops: refresh: load %s: %w", d.ID, err)
		}
		if ok {
			e.Recorded = Recorded(r.Probes, d, c, r.Dimensions)
		}
		if st, has, _ := r.Snapshots.Status(ctx, d.Mgmt); has {
			e.Status = st
		}
		entries[i], decisions[i] = e, r.Policy.Decide(now, e, r.Dimensions)
	}
	return entries, decisions, nil
}
