package ops

import (
	"context"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/batch"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/evidence"
)

// Notify publishes the events of an operation. The zero value publishes
// nothing. Delivery failures never change the operation's result; they go
// to OnError when set.
type Notify struct {
	Publisher event.Publisher
	// Tool is the event source, such as "fabric".
	Tool string
	// OnError receives delivery failures, for logging.
	OnError func(error)
}

func (n Notify) emit(ctx context.Context, kind event.Kind, sev assess.Severity, at time.Time, d device.Device, summary string, fields ...evidence.Field) {
	if n.Publisher == nil {
		return
	}
	e, err := event.New(event.Event{
		Kind: kind, Severity: sev, At: at, Source: n.Tool, Device: d.ID,
		Subject: "device:" + d.Label(), Summary: summary, Fields: fields,
	})
	if err == nil {
		err = n.Publisher.Publish(context.WithoutCancel(ctx), e)
	}
	if err != nil && n.OnError != nil {
		n.OnError(err)
	}
}

// Pacing tunes read-only fleet operations: bounded retries of transient
// failures and a per-partition concurrency cap, such as per site.
type Pacing struct {
	Retry          *batch.Retry
	Partition      func(device.Device) string
	PartitionLimit int
}

func pace[T any](p Pacing, o batch.Options[T]) batch.Options[T] {
	o.Retry, o.Partition, o.PartitionLimit = p.Retry, p.Partition, p.PartitionLimit
	return o
}
