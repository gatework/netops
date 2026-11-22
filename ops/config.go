package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/baseline"
	"github.com/gatework/netops/batch"
	"github.com/gatework/netops/config"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/diff"
	"github.com/gatework/netops/event"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/parse"
)

// BackupRequest backs up running configurations into the archive.
type BackupRequest struct {
	Devices []device.Device
	// Command returns the command that prints the running configuration,
	// for sessions that do not implement ConfigReader.
	Command func(device.Device) string
	// Dialect returns the configuration dialect used for normalization.
	Dialect func(device.Device) config.Dialect
	Dialer  Dialer
	Archive Archive
	Source  string // recorded with each version; default "backup"
	Workers int
	Timeout time.Duration
	Events  batch.Events[config.Archived]
	// Pacing adds retries of transient failures and a per-site cap.
	Pacing Pacing
	// Notify publishes config.changed for configurations that changed since
	// their previous version.
	Notify Notify
	Now    func() time.Time
}

// Backup collects each configuration, normalizes it and archives it. An
// unchanged configuration is recorded as checked, not stored again.
func Backup(ctx context.Context, r BackupRequest) (batch.Report[config.Archived], error) {
	if r.Dialer == nil || r.Archive == nil || r.Command == nil || r.Dialect == nil {
		return batch.Report[config.Archived]{}, fmt.Errorf("ops: backup: missing dependency")
	}
	now := clock(r.Now)
	source := r.Source
	if source == "" {
		source = "backup"
	}
	rep, err := batch.Run(ctx, r.Devices, pace(r.Pacing, batch.Options[config.Archived]{
		Workers: r.Workers, Timeout: r.Timeout, Events: r.Events, Now: now,
		Task: func(ctx context.Context, d device.Device) (config.Archived, error) {
			text, at, err := runningConfig(ctx, r.Dialer, d, r.Command, now)
			if err != nil {
				return config.Archived{}, err
			}
			doc, err := config.Parse(ctx, text, r.Dialect(d), parse.Limits{})
			if err != nil {
				return config.Archived{}, failure.Wrap(failure.CodeLimit, "ops: backup: parse", err)
			}
			return r.Archive.Put(ctx, config.Backup{Device: d.ID, Text: text, Normalized: doc.Digest(), At: at, Source: source})
		},
	}))
	for _, res := range rep.Results {
		if a := res.Value; res.State == batch.StateSucceeded && a.Changed && a.Previous != nil {
			r.Notify.emit(ctx, event.KindConfigChanged, assess.SeverityLow, a.Version.At, res.Device,
				fmt.Sprintf("configuration changed: version %d replaces %d", a.Version.Seq, a.Previous.Seq),
				event.Fields("version", event.Int(a.Version.Seq), "previous", event.Int(a.Previous.Seq), "digest", a.Version.Digest.Short())...)
		}
	}
	if err != nil {
		return rep, fmt.Errorf("ops: backup: %w", err)
	}
	return rep, nil
}

// runningConfig returns the configuration body, without echo and prompt.
func runningConfig(ctx context.Context, dialer Dialer, d device.Device, commandOf func(device.Device) string, now func() time.Time) (string, time.Time, error) {
	var text string
	at := now().UTC()
	opErr, closeErr := dialer.Open(ctx, d, func(ctx context.Context, s Session) error {
		if cr, ok := s.(ConfigReader); ok {
			cfg, err := cr.Config(ctx)
			if err != nil {
				return err
			}
			text, at = strings.TrimRight(cfg, "\n")+"\n", now().UTC()
			return nil
		}
		command := commandOf(d)
		c, err := s.Collect(ctx, []string{command})
		if err != nil {
			return err
		}
		o, ok := c.Answer(command)
		if !ok {
			return failure.New(failure.CodeRejected, "ops: running configuration")
		}
		lines, err := o.Body(ctx, nil)
		if err != nil {
			return failure.Wrap(failure.CodeLimit, "ops: running configuration", err)
		}
		texts := make([]string, len(lines))
		for i, l := range lines {
			texts[i] = l.Text
		}
		text, at = strings.Join(texts, "\n")+"\n", o.RecordedAt
		return nil
	})
	if err := errors.Join(opErr, closeErr); err != nil {
		return "", at, err
	}
	return text, at, nil
}

// Comparison is the difference between two configurations.
type Comparison struct {
	Changed bool          `json:"changed"`
	Exact   bool          `json:"exact"`
	Added   int           `json:"added"`
	Removed int           `json:"removed"`
	Changes []diff.Change `json:"changes"`
	// Unified is the masked unified diff of the normalized texts.
	Unified string `json:"unified"`
}

// Compare diffs two configurations of one dialect after normalization:
// a structural block diff plus a unified text diff, both with secrets
// masked. Use it for device against device, two archived versions, or a
// device against its intended configuration.
func Compare(ctx context.Context, a, b string, labelA, labelB string, d config.Dialect) (Comparison, error) {
	da, err := config.Parse(ctx, a, d, parse.Limits{})
	if err != nil {
		return Comparison{}, fmt.Errorf("ops: compare %s: %w", labelA, err)
	}
	db, err := config.Parse(ctx, b, d, parse.Limits{})
	if err != nil {
		return Comparison{}, fmt.Errorf("ops: compare %s: %w", labelB, err)
	}
	script := diff.Lines(da.Normalized(), db.Normalized(), diff.Options{})
	added, removed := script.Stats()
	changes := diff.Tree(da, db, diff.TreeOptions{})
	for i := range changes {
		changes[i].Line = d.Mask(changes[i].Line)
		for j, l := range changes[i].Block {
			changes[i].Block[j] = d.Mask(l)
		}
	}
	return Comparison{
		Changed: da.Digest() != db.Digest(), Exact: script.Exact, Added: added, Removed: removed,
		Changes: changes, Unified: diff.Unified(script, labelA, labelB, 3, d.Mask),
	}, nil
}

// Compliance checks a configuration against the device's baseline and
// returns the violations as findings of rule "baseline.<rule id>".
func Compliance(ctx context.Context, d device.Device, text string, dialect config.Dialect, profile baseline.Profile) ([]assess.Finding, error) {
	doc, err := config.Parse(ctx, text, dialect, parse.Limits{})
	if err != nil {
		return nil, fmt.Errorf("ops: compliance %s: %w", d.ID, err)
	}
	resolved := profile.Resolve(d)
	severity := map[string]assess.Severity{}
	for _, r := range resolved.Rules {
		severity[r.ID] = assess.SeverityMedium
		if s, err := assess.ParseSeverity(r.Severity); err == nil {
			severity[r.ID] = s
		}
	}
	var out []assess.Finding
	for _, v := range resolved.CheckConfig(doc) {
		subject := assess.NewSubject("device", d.Label())
		for _, b := range v.Block {
			subject = subject.Child("block", dialect.Mask(b))
		}
		f := assess.Finding{
			Rule: assess.RuleID("baseline." + ruleSlug(v.Rule)), Severity: severity[v.Rule],
			Category: assess.CategoryCompliance, Subject: subject, Title: v.Title,
			Expected: v.Pattern, Actual: v.Text,
			Fields: []evidence.Field{{Key: "kind", Value: string(v.Kind)}},
		}
		if v.Line > 0 {
			f.Evidence = []evidence.SourceRef{{Device: d.ID, Command: "configuration", Digest: doc.RawDigest(), Line: v.Line}}
		}
		out = append(out, f)
	}
	return out, nil
}

// ruleSlug turns a baseline rule ID into a rule ID segment.
func ruleSlug(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r - 'A' + 'a'
		}
		return '_'
	}, id)
}
