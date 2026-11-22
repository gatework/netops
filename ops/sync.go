package ops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/batch"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/diff"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/parse"
)

// Versions reads archived configurations. store/archive.Archive
// implements it; a device without versions returns config.ErrNotArchived.
type Versions interface {
	Latest(ctx context.Context, deviceID string) (config.Version, error)
	Get(ctx context.Context, v config.Version) (string, error)
}

// SyncStatus is the result of comparing a device with its archive.
type SyncStatus string

// Sync statuses.
const (
	SyncInSync     SyncStatus = "in_sync"     // running equals the latest archived version
	SyncOutOfSync  SyncStatus = "out_of_sync" // running changed since the latest version
	SyncNoBaseline SyncStatus = "no_baseline" // nothing archived yet
)

// Synced is one device's comparison. Added and Removed count normalized
// lines; the configuration text itself is not kept.
type Synced struct {
	Status   SyncStatus      `json:"status"`
	Latest   *config.Version `json:"latest,omitempty"`
	Observed evidence.Digest `json:"observed"`
	Added    int             `json:"added,omitempty"`
	Removed  int             `json:"removed,omitempty"`
}

// CheckSyncRequest compares running configurations with the archive
// without writing to it.
type CheckSyncRequest struct {
	Devices  []device.Device
	Command  func(device.Device) string
	Dialect  func(device.Device) config.Dialect
	Dialer   Dialer
	Versions Versions
	Workers  int
	Timeout  time.Duration
	Pacing   Pacing
	Notify   Notify
	Events   batch.Events[Synced]
	Now      func() time.Time
}

// CheckSync detects out-of-band changes: it reads each running
// configuration, normalizes it and compares it with the latest archived
// version, publishing config.out_of_sync for devices that changed. It is
// read-only on both the devices and the archive.
func CheckSync(ctx context.Context, r CheckSyncRequest) (batch.Report[Synced], error) {
	if r.Dialer == nil || r.Versions == nil || r.Command == nil || r.Dialect == nil {
		return batch.Report[Synced]{}, fmt.Errorf("ops: check sync: missing dependency")
	}
	now := clock(r.Now)
	rep, err := batch.Run(ctx, r.Devices, pace(r.Pacing, batch.Options[Synced]{
		Workers: r.Workers, Timeout: r.Timeout, Events: r.Events, Now: now,
		Task: func(ctx context.Context, d device.Device) (Synced, error) { return r.one(ctx, d, now) },
	}))
	for _, res := range rep.Results {
		if res.State == batch.StateSucceeded && res.Value.Status == SyncOutOfSync {
			v := res.Value
			r.Notify.emit(ctx, event.KindConfigOutOfSync, assess.SeverityMedium, now(), res.Device,
				fmt.Sprintf("running configuration differs from archived version %d", v.Latest.Seq),
				event.Fields("version", event.Int(v.Latest.Seq), "added", event.Int(v.Added), "removed", event.Int(v.Removed))...)
		}
	}
	if err != nil {
		return rep, fmt.Errorf("ops: check sync: %w", err)
	}
	return rep, nil
}

func (r CheckSyncRequest) one(ctx context.Context, d device.Device, now func() time.Time) (Synced, error) {
	text, _, err := runningConfig(ctx, r.Dialer, d, r.Command, now)
	if err != nil {
		return Synced{}, err
	}
	dialect := r.Dialect(d)
	doc, err := config.Parse(ctx, text, dialect, parse.Limits{})
	if err != nil {
		return Synced{}, failure.Wrap(failure.CodeLimit, "ops: check sync: parse", err)
	}
	s := Synced{Observed: doc.Digest()}
	latest, err := r.Versions.Latest(ctx, d.ID)
	switch {
	case errors.Is(err, config.ErrNotArchived):
		s.Status = SyncNoBaseline
		return s, nil
	case err != nil:
		return Synced{}, failure.Wrap(failure.CodeStorage, "ops: check sync: archive", err)
	}
	s.Latest = &latest
	if latest.Normalized == doc.Digest() {
		s.Status = SyncInSync
		return s, nil
	}
	s.Status = SyncOutOfSync
	old, err := r.Versions.Get(ctx, latest)
	if err != nil {
		return Synced{}, failure.Wrap(failure.CodeStorage, "ops: check sync: archive", err)
	}
	was, err := config.Parse(ctx, old, dialect, parse.Limits{})
	if err != nil {
		return Synced{}, failure.Wrap(failure.CodeLimit, "ops: check sync: parse", err)
	}
	s.Added, s.Removed = diff.Lines(was.Normalized(), doc.Normalized(), diff.Options{}).Stats()
	return s, nil
}
