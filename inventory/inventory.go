package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"

	"go.yaml.in/yaml/v3"

	"github.com/gatework/netops/device"
)

// ErrInvalid reports an invalid inventory file or source configuration.
var ErrInvalid = errors.New("inventory: invalid")

// Source yields device records. Records are validated by Load.
type Source interface {
	Name() string
	Devices(ctx context.Context) ([]device.Device, Report, error)
}

// Report accounts for the records of one source.
type Report struct {
	Source  string         `json:"source"`
	Rows    int            `json:"rows"`
	Dropped map[string]int `json:"dropped,omitempty"` // reason -> count
	// Stale is set when the records came from a cache after a failed
	// refresh.
	Stale bool `json:"stale,omitempty"`
}

func (r *Report) drop(reason string) {
	if r.Dropped == nil {
		r.Dropped = map[string]int{}
	}
	r.Dropped[reason]++
}

// Load reads every source and merges them in order. The returned reports
// cover each source plus the final validation.
func Load(ctx context.Context, sources ...Source) (*device.Inventory, []Report, device.Report, error) {
	var merged *device.Inventory
	var reports []Report
	var last device.Report
	for _, s := range sources {
		devices, rep, err := s.Devices(ctx)
		rep.Source = s.Name()
		reports = append(reports, rep)
		if err != nil {
			return nil, reports, last, fmt.Errorf("inventory: %s: %w", s.Name(), err)
		}
		inv, vr := device.New(devices)
		for _, rej := range vr.Rejected {
			reports[len(reports)-1].drop(string(rej.Reason))
		}
		merged = merged.Merge(inv)
		last = vr
	}
	if merged == nil {
		merged, last = device.New(nil)
	}
	return merged, reports, last, nil
}

// File is a static inventory document:
//
//	defaults: {platform: h3c_wireless, site: hq}
//	devices:
//	  - {name: ac1, mgmt: 192.0.2.1, role: ac}
type File struct {
	Defaults device.Device   `yaml:"defaults"`
	Devices  []device.Device `yaml:"devices"`
}

// DecodeFile parses a static inventory strictly and applies the defaults
// to fields a device leaves empty.
func DecodeFile(body []byte) ([]device.Device, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	out := make([]device.Device, len(f.Devices))
	for i, d := range f.Devices {
		d.Platform = cmpOr(d.Platform, f.Defaults.Platform)
		d.Vendor = cmpOr(d.Vendor, f.Defaults.Vendor)
		d.Role = cmpOr(d.Role, f.Defaults.Role)
		d.Site = cmpOr(d.Site, f.Defaults.Site)
		labels := maps.Clone(f.Defaults.Labels)
		if labels == nil && len(d.Labels) > 0 {
			labels = map[string]string{}
		}
		maps.Copy(labels, d.Labels)
		d.Labels = labels
		out[i] = d
	}
	return out, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Static is an in-memory source, such as a decoded File.
type Static struct {
	Label   string
	Records []device.Device
}

// Name returns the label.
func (s Static) Name() string { return cmpOr(s.Label, "static") }

// Devices returns copies of the records.
func (s Static) Devices(context.Context) ([]device.Device, Report, error) {
	out := make([]device.Device, len(s.Records))
	for i, d := range s.Records {
		out[i] = d.Clone()
	}
	return out, Report{Rows: len(out)}, nil
}
