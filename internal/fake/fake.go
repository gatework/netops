// Package fake provides an in-memory device and dialer that implement the
// ops ports, for tests of workflows without a network.
//
// A Device holds a configuration as an ordered list of lines. Writing a
// line adds it; writing "no <line>" removes it. Read commands answer from a
// table, and "show running-config" prints the configuration with an echo
// and a prompt, like a real CLI.
package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gatework/netops/capture"
	"github.com/gatework/netops/change"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/ops"
)

// ShowConfig is the command that prints the configuration.
const ShowConfig = "show running-config"

// Device is one simulated device. It is safe for concurrent use.
type Device struct {
	mu          sync.Mutex
	Name        string
	config      []string
	saved       []string
	answers     map[string]string
	checkpoints map[string][]string
	writes      []string
	// FailWrite makes the write with this 1-based index fail; 0 never.
	FailWrite int
	// RejectWrite makes that write a device rejection instead.
	RejectWrite bool
	// Checkpoints enables checkpoint support.
	Checkpoints bool
	// Unreachable makes Open fail.
	Unreachable bool
	// AfterWrite runs after each successful write, to simulate side
	// effects such as a neighbor going down.
	AfterWrite func(d *Device)
	// State is free operational state for health checks.
	State map[string]string
}

// NewDevice returns a device with the given configuration lines.
func NewDevice(name string, config ...string) *Device {
	return &Device{
		Name: name, config: slices.Clone(config), answers: map[string]string{},
		checkpoints: map[string][]string{}, State: map[string]string{},
	}
}

// Answer sets the response body of a read command.
func (d *Device) Answer(command, body string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.answers[command] = body
}

// Config returns the configuration lines.
func (d *Device) Config() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.config)
}

// SetConfig replaces the configuration, simulating an out-of-band change.
func (d *Device) SetConfig(lines ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.config = slices.Clone(lines)
}

// Saved returns the saved configuration.
func (d *Device) Saved() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.saved)
}

// Writes returns every command written, in order.
func (d *Device) Writes() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.writes)
}

// Dialer serves fake devices by device ID.
type Dialer struct {
	mu      sync.Mutex
	devices map[string]*Device
	opens   int
	// NoCredentials lists device IDs without credentials.
	NoCredentials map[string]bool
	// CloseErr is returned as the close error of every session.
	CloseErr error
	Now      func() time.Time
}

// NewDialer returns a dialer for the devices keyed by device ID.
func NewDialer(devices map[string]*Device) *Dialer {
	return &Dialer{devices: devices, NoCredentials: map[string]bool{}}
}

var _ ops.Dialer = (*Dialer)(nil)

// Opens returns how many sessions were opened.
func (f *Dialer) Opens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

// CanConnect reports whether credentials exist.
func (f *Dialer) CanConnect(_ context.Context, d device.Device) bool { return !f.NoCredentials[d.ID] }

// Open runs fn with a session on the device.
func (f *Dialer) Open(ctx context.Context, d device.Device, fn func(context.Context, ops.Session) error) (opErr, closeErr error) {
	f.mu.Lock()
	dev, ok := f.devices[d.ID]
	f.opens++
	now := f.Now
	f.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	if !ok || dev.Unreachable {
		return failure.New(failure.CodeUnreachable, "fake: open"), nil
	}
	if f.NoCredentials[d.ID] {
		return failure.New(failure.CodeNoCreds, "fake: open"), nil
	}
	if err := ctx.Err(); err != nil {
		return err, nil
	}
	return fn(ctx, &Session{dev: dev, now: now}), f.CloseErr
}

// Session is a session on a fake device.
type Session struct {
	dev *Device
	now func() time.Time
}

var _ ops.Session = (*Session)(nil)

// Collect answers each command; unknown commands are rejected.
func (s *Session) Collect(ctx context.Context, commands []string) (capture.Capture, error) {
	c := capture.Capture{Prompt: s.prompt()}
	for _, cmd := range commands {
		if err := ctx.Err(); err != nil {
			return c, err
		}
		o := capture.Output{Command: cmd, Prompt: s.prompt(), RecordedAt: s.now().UTC()}
		body, ok := s.body(cmd)
		if !ok {
			o.Failure = failure.CodeRejected
		} else {
			o.Raw = cmd + "\r\n" + body + "\r\n" + s.prompt()
		}
		c.Outputs = append(c.Outputs, o)
	}
	return c, nil
}

// Write applies one command.
func (s *Session) Write(ctx context.Context, cmd change.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d := s.dev
	d.mu.Lock()
	d.writes = append(d.writes, cmd.Text)
	n := len(d.writes)
	if d.FailWrite == n {
		d.mu.Unlock()
		if d.RejectWrite {
			return failure.New(failure.CodeRejected, "fake: write")
		}
		return failure.New(failure.CodeClosed, "fake: write")
	}
	if line, ok := strings.CutPrefix(cmd.Text, "no "); ok {
		d.config = slices.DeleteFunc(d.config, func(l string) bool { return l == line })
	} else if !slices.Contains(d.config, cmd.Text) {
		d.config = append(d.config, cmd.Text)
	}
	after := d.AfterWrite
	d.mu.Unlock()
	if after != nil {
		after(d)
	}
	return nil
}

// Save copies the configuration to the saved one.
func (s *Session) Save(context.Context) error {
	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()
	s.dev.saved = slices.Clone(s.dev.config)
	return nil
}

// CanCheckpoint reports the device's checkpoint support.
func (s *Session) CanCheckpoint() bool { return s.dev.Checkpoints }

// Checkpoint stores the configuration under name.
func (s *Session) Checkpoint(_ context.Context, name string) error {
	if !s.dev.Checkpoints {
		return failure.New(failure.CodeUnsupported, "fake: checkpoint")
	}
	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()
	s.dev.checkpoints[name] = slices.Clone(s.dev.config)
	return nil
}

// Rollback restores the checkpoint name.
func (s *Session) Rollback(_ context.Context, name string) error {
	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()
	cfg, ok := s.dev.checkpoints[name]
	if !ok {
		return fmt.Errorf("fake: no checkpoint %s", name)
	}
	s.dev.config = slices.Clone(cfg)
	return nil
}

// Prompt is the fake device prompt.
func (s *Session) prompt() string { return s.dev.Name + "#" }

func (s *Session) body(cmd string) (string, bool) {
	s.dev.mu.Lock()
	defer s.dev.mu.Unlock()
	if cmd == ShowConfig {
		return strings.Join(s.dev.config, "\n"), true
	}
	b, ok := s.dev.answers[cmd]
	return b, ok
}
