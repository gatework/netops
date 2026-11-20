package ops

import (
	"context"
	"net/netip"
	"slices"
	"time"

	"github.com/gatework/netops/cache"
	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
)

// Session is an open device session. connect.Session implements it.
type Session interface {
	// Collect runs read commands; a rejected command is a failed output, not
	// an error.
	Collect(ctx context.Context, commands []string) (capture.Capture, error)
	// Write sends one configuration command.
	Write(ctx context.Context, cmd change.Command) error
	// Save persists the running configuration.
	Save(ctx context.Context) error
	// CanCheckpoint reports whether Checkpoint and Rollback are supported.
	CanCheckpoint() bool
	Checkpoint(ctx context.Context, name string) error
	Rollback(ctx context.Context, name string) error
}

// Dialer opens sessions. connect.Dialer.Port implements it.
type Dialer interface {
	// Open runs fn with a session on d and reports the operation error and
	// the session close error separately.
	Open(ctx context.Context, d device.Device, fn func(context.Context, Session) error) (opErr, closeErr error)
	// CanConnect reports whether credentials exist for d.
	CanConnect(ctx context.Context, d device.Device) bool
}

// Snapshots stores captures. store/snapshot.Store implements it.
type Snapshots interface {
	Load(ctx context.Context, host netip.Addr) (capture.Capture, bool, error)
	Merge(ctx context.Context, fresh capture.Capture) (capture.Capture, error)
	Status(ctx context.Context, host netip.Addr) (cache.Status, bool, error)
	SetStatus(ctx context.Context, host netip.Addr, st cache.Status) error
}

// Archive stores configuration versions. store/archive.Archive implements
// it.
type Archive interface {
	Put(ctx context.Context, b config.Backup) (config.Archived, error)
}

// Probes maps a dimension to the command that collects it on a device.
type Probes interface {
	Command(d device.Device, dim evidence.Dimension) (string, bool)
}

// ProbeTable is a platform-independent Probes.
type ProbeTable map[evidence.Dimension]string

// Command returns the command of dim.
func (t ProbeTable) Command(_ device.Device, dim evidence.Dimension) (string, bool) {
	c, ok := t[dim]
	return c, ok
}

// Commands returns the distinct commands of dims for d, in dims order.
func Commands(p Probes, d device.Device, dims []evidence.Dimension) []string {
	var out []string
	for _, dim := range dims {
		if c, ok := p.Command(d, dim); ok && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// Recorded returns when each dimension of c was last answered
// successfully.
func Recorded(p Probes, d device.Device, c capture.Capture, dims []evidence.Dimension) map[evidence.Dimension]time.Time {
	out := map[evidence.Dimension]time.Time{}
	for _, dim := range dims {
		cmd, ok := p.Command(d, dim)
		if !ok {
			continue
		}
		if o, ok := c.Answer(cmd); ok && !o.RecordedAt.IsZero() {
			out[dim] = o.RecordedAt
		}
	}
	return out
}

func clock(now func() time.Time) func() time.Time {
	if now == nil {
		return time.Now
	}
	return now
}
