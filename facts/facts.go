package facts

import (
	"embed"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sirikothe/gotextfsm"

	"github.com/gatework/netops/device"
)

// ErrUnrecognized reports output that is not this platform's facts.
var ErrUnrecognized = errors.New("facts: unrecognized output")

// Facts is what a device reports. A field the platform does not print is
// empty; Version is always set.
type Facts struct {
	Hostname string `json:"hostname,omitempty"`
	Model    string `json:"model,omitempty"`
	Serial   string `json:"serial,omitempty"`
	Version  string `json:"version"`
	Uptime   string `json:"uptime,omitempty"`
}

// Probe is how one platform reports its facts.
type Probe struct {
	// Commands are read-only; Parse expects their outputs in this order.
	Commands []string
	template string
	source   string
}

// Parse reads the response bodies of Commands.
func (p Probe) Parse(outputs []string) (Facts, error) {
	if p.source == "" {
		return Facts{}, fmt.Errorf("%w: zero probe", ErrUnrecognized)
	}
	text := strings.ReplaceAll(strings.Join(outputs, "\n"), "\r", "")
	// gotextfsm mutates a compiled template while parsing; compile per call.
	var fsm gotextfsm.TextFSM
	if err := fsm.ParseString(p.source); err != nil {
		return Facts{}, fmt.Errorf("%w: %s: %w", ErrUnrecognized, p.template, err)
	}
	var out gotextfsm.ParserOutput
	if err := out.ParseTextString(text, fsm, true); err != nil {
		return Facts{}, fmt.Errorf("%w: %s: %w", ErrUnrecognized, p.template, err)
	}
	if len(out.Dict) != 1 {
		return Facts{}, fmt.Errorf("%w: %s matched %d records", ErrUnrecognized, p.template, len(out.Dict))
	}
	row := out.Dict[0]
	f := Facts{Hostname: field(row, "HOSTNAME"), Model: field(row, "MODEL"), Serial: field(row, "SERIAL"), Version: field(row, "VERSION"), Uptime: field(row, "UPTIME")}
	if f.Version == "" {
		return Facts{}, fmt.Errorf("%w: %s has no version", ErrUnrecognized, p.template)
	}
	return f, nil
}

func field(row map[string]any, name string) string {
	s, _ := row[name].(string)
	return strings.TrimSpace(s)
}

//go:embed templates/*.textfsm
var templates embed.FS

func probe(template string, commands ...string) Probe {
	body, err := templates.ReadFile("templates/" + template + ".textfsm")
	if err != nil {
		panic("facts: missing template " + template)
	}
	var fsm gotextfsm.TextFSM
	if err := fsm.ParseString(string(body)); err != nil {
		panic("facts: template " + template + ": " + err.Error())
	}
	return Probe{Commands: commands, template: template, source: string(body)}
}

// builtin maps platform keys, shared with the transport registry, to probes.
var builtin = func() map[string]Probe {
	comware := probe("comware_display_version", "display version", "display device manuinfo")
	return map[string]Probe{
		"h3c":          comware,
		"h3c_wireless": comware,
		"cisco_ios":    probe("ios_show_version", "show version"),
		"cisco_nxos":   probe("nxos_show_version", "show version"),
		"huawei":       probe("vrp_display_version", "display version", "display esn"),
		"juniper":      probe("junos_show_version", "show version | no-more", "show chassis hardware | no-more"),
		"fortinet":     probe("fortios_get_system_status", "get system status"),
	}
}()

// Lookup returns the probe of a platform.
func Lookup(platform string) (Probe, bool) {
	p, ok := builtin[platform]
	if ok {
		p.Commands = slices.Clone(p.Commands)
	}
	return p, ok
}

// Platforms lists the platforms with a probe, sorted.
func Platforms() []string { return slices.Sorted(maps.Keys(builtin)) }

// Mismatch is an inventory attribute the device contradicts.
type Mismatch struct {
	Field     string `json:"field"` // "name", "model" or "version"
	Inventory string `json:"inventory"`
	Device    string `json:"device"`
}

// Compare lists where d's inventory record disagrees with f. Attributes
// either side leaves empty are not compared; comparison ignores case, and
// a model matches when one names the other ("S6850-56HF" in
// "H3C S6850-56HF").
func Compare(d device.Device, f Facts) []Mismatch {
	var out []Mismatch
	check := func(name, inv, dev string, same func(a, b string) bool) {
		if inv != "" && dev != "" && !same(strings.ToLower(inv), strings.ToLower(dev)) {
			out = append(out, Mismatch{Field: name, Inventory: inv, Device: dev})
		}
	}
	eq := func(a, b string) bool { return a == b }
	contains := func(a, b string) bool { return strings.Contains(a, b) || strings.Contains(b, a) }
	check("name", d.Name, f.Hostname, eq)
	check("model", d.Model, f.Model, contains)
	check("version", d.Version, f.Version, contains)
	return out
}
