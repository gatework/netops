package fake

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gatework/netops/change"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/ops"
)

// Config is the model of a fake device: its configuration lines.
type Config []string

// Fingerprint hashes the configuration as a set.
func Fingerprint(c Config) evidence.Digest {
	return evidence.NewHasher("fake/config/v1").Set("lines", c).Sum()
}

// Read reads the configuration through a session.
func Read(ctx context.Context, s ops.Session, _ *change.Plan[Config]) (Config, error) {
	c, err := s.Collect(ctx, []string{ShowConfig})
	if err != nil {
		return nil, err
	}
	o, ok := c.Answer(ShowConfig)
	if !ok {
		return nil, errors.New("fake: no configuration")
	}
	lines, err := o.Body(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := Config{}
	for _, l := range lines {
		if t := strings.TrimSpace(l.Text); t != "" {
			out = append(out, t)
		}
	}
	return out, nil
}

// AddLine is a change that adds one configuration line.
type AddLine struct {
	Line string `json:"line"`
}

// Kind returns "fake.add".
func (AddLine) Kind() change.Kind { return "fake.add" }

// Validate refuses an empty line or one starting with "no ".
func (a AddLine) Validate() error {
	if strings.TrimSpace(a.Line) == "" || strings.HasPrefix(a.Line, "no ") {
		return fmt.Errorf("fake: invalid line %q", a.Line)
	}
	return nil
}

// Eligible refuses lines starting with "forbidden".
func (a AddLine) Eligible(Config) error {
	if strings.HasPrefix(a.Line, "forbidden") {
		return errors.New("fake: forbidden")
	}
	return nil
}

// Satisfied reports whether the line is present.
func (a AddLine) Satisfied(c Config) bool { return slices.Contains(c, a.Line) }

// Render adds the line; rollback removes it.
func (a AddLine) Render(Config) (change.Script, error) {
	return change.Script{Commands: change.Commands(a.Line), Rollback: change.Commands("no " + a.Line)}, nil
}

// Preserved reports any other line that changed.
func (a AddLine) Preserved(before, after Config) error {
	other := func(c Config) []string {
		return slices.Sorted(slices.Values(slices.DeleteFunc(slices.Clone(c), func(l string) bool { return l == a.Line })))
	}
	if !slices.Equal(other(before), other(after)) {
		return errors.New("fake: other lines changed")
	}
	return nil
}
