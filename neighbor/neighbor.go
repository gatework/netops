package neighbor

import (
	"embed"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sirikothe/gotextfsm"

	"github.com/gatework/netops/iface"
)

// ErrParse reports output a probe cannot account for completely.
var ErrParse = errors.New("neighbor: unrecognized output")

// Protocol is the discovery protocol of an adjacency.
type Protocol string

// Discovery protocols.
const (
	LLDP Protocol = "lldp"
	CDP  Protocol = "cdp"
)

// Neighbor is one adjacency seen on a local port.
type Neighbor struct {
	LocalPort string   `json:"local_port"`
	System    string   `json:"system"` // the neighbor's advertised system name
	Port      string   `json:"port"`   // the neighbor's advertised port
	ChassisID string   `json:"chassis_id,omitempty"`
	Protocol  Protocol `json:"protocol"`
}

var unknown = regexp.MustCompile(`(?i)^(?:-+|n/?a|unknown|none|null|not\s+(?:available|advertised))?$`)

// Issue codes of an adjacency whose identity is not usable.
const (
	IssueUnknownSystem = "unknown_neighbor_name"
	IssueUnknownPort   = "unknown_neighbor_interface"
)

// Issue returns why the neighbor's identity cannot be trusted, or "".
func (n Neighbor) Issue() string {
	switch {
	case unknown.MatchString(strings.TrimSpace(n.System)):
		return IssueUnknownSystem
	case unknown.MatchString(strings.TrimSpace(n.Port)):
		return IssueUnknownPort
	}
	return ""
}

// ByLocalPort groups neighbors by the comparison key of their local port,
// keeping the input order within each group.
func ByLocalPort(ns []Neighbor) map[string][]Neighbor {
	out := map[string][]Neighbor{}
	for _, n := range ns {
		k := iface.Key(n.LocalPort)
		out[k] = append(out[k], n)
	}
	return out
}

// Probe is how one platform discovers neighbors.
type Probe struct {
	// Command is the read command that lists neighbors.
	Command  string
	Protocol Protocol
	template string
	// source is the template text. gotextfsm mutates a compiled TextFSM
	// while parsing, so every Parse compiles its own.
	source string
	// count returns the number of records independently of the template,
	// or -1 when the output is not recognized, and whether the output
	// positively states that there are no neighbors.
	count func(text string) (n int, empty bool)
}

// Parse returns the neighbors in output, the response body of Command.
// The template rows must match the independent record count.
func (p Probe) Parse(output string) ([]Neighbor, error) {
	if p.count == nil {
		return nil, fmt.Errorf("%w: zero probe", ErrParse)
	}
	want, empty := p.count(output)
	if want < 0 {
		return nil, fmt.Errorf("%w: %s", ErrParse, p.template)
	}
	var fsm gotextfsm.TextFSM
	if err := fsm.ParseString(p.source); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrParse, p.template, err)
	}
	var out gotextfsm.ParserOutput
	if err := out.ParseTextString(output, fsm, true); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrParse, p.template, err)
	}
	if len(out.Dict) != want || want == 0 && !empty {
		return nil, fmt.Errorf("%w: %s parsed %d of %d records", ErrParse, p.template, len(out.Dict), want)
	}
	ns := make([]Neighbor, len(out.Dict))
	for i, row := range out.Dict {
		ns[i] = Neighbor{
			LocalPort: field(row, "LOCAL_PORT"), System: field(row, "SYSTEM"),
			Port: field(row, "PORT"), ChassisID: field(row, "CHASSIS_ID"), Protocol: p.Protocol,
		}
	}
	return ns, nil
}

func field(row map[string]any, name string) string {
	s, _ := row[name].(string)
	return strings.TrimSpace(s)
}

//go:embed templates/*.textfsm
var templates embed.FS

func probe(command string, proto Protocol, template string, count func(string) (int, bool)) Probe {
	body, err := templates.ReadFile("templates/" + template + ".textfsm")
	if err != nil {
		panic("neighbor: missing template " + template)
	}
	p := Probe{Command: command, Protocol: proto, template: template, source: string(body), count: count}
	var fsm gotextfsm.TextFSM
	if err := fsm.ParseString(p.source); err != nil {
		panic("neighbor: template " + template + ": " + err.Error())
	}
	return p
}

// builtin maps platform keys to their probes. The keys are the platform
// vocabulary shared with the transport (net-connector) registry.
var builtin = func() map[string]Probe {
	comware := probe("display lldp neighbor-information list", LLDP, "comware_lldp_list", comwareCount)
	return map[string]Probe{
		"h3c":          comware,
		"h3c_wireless": comware,
		"cisco_ios":    probe("show cdp neighbors detail", CDP, "ios_cdp_detail", iosCount),
		"cisco_nxos":   probe("show cdp neighbors detail", CDP, "nxos_cdp_detail", nxosCount),
		"huawei":       probe("display lldp neighbor brief", LLDP, "vrp_lldp_brief", vrpCount),
		"juniper":      probe("show lldp neighbors detail | no-more", LLDP, "junos_lldp_detail", junosCount),
		"hillstone":    probe("show lldp neighbor-information", LLDP, "hillstone_lldp", hillstoneCount),
		"palo_alto":    probe("show lldp neighbors all", LLDP, "panos_lldp_all", panosCount),
	}
}()

// Lookup returns the probe of a platform.
func Lookup(platform string) (Probe, bool) {
	p, ok := builtin[platform]
	return p, ok
}

// Platforms lists the platforms with a probe, sorted.
func Platforms() []string { return slices.Sorted(maps.Keys(builtin)) }

var (
	rule       = regexp.MustCompile(`^[-=+ ]+$`)
	promptLine = regexp.MustCompile(`^(?:<[^>]+>|\[[^\]]+\]|\S+[#>])$`)
)

// tableLines returns the nonblank rows after the first header line.
func tableLines(text string, header *regexp.Regexp) ([]string, bool) {
	var lines []string
	seen := false
	for line := range strings.SplitSeq(text, "\n") {
		if !seen {
			seen = header.MatchString(line)
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || rule.MatchString(line) || promptLine.MatchString(line) {
			continue
		}
		lines = append(lines, line)
	}
	return lines, seen
}

var (
	comwareHeader   = regexp.MustCompile(`(?i)^\s*(?:System Name\s+Local Interface\b|(?:Local Interface|LocalIf)\s+)`)
	comwareNameLead = regexp.MustCompile(`(?im)^[ \t]*System Name[ \t]+Local Interface\b`)
	comwarePort     = regexp.MustCompile(`(?i)^(?:[A-Za-z][A-Za-z-]*\d+(?:/\d+)+|(?:Bridge-Aggregation|BAGG|Route-Aggregation|RAGG)\d+)(?:[:.]\d+)*$`)
)

func comwareCount(text string) (int, bool) {
	lines, seen := tableLines(text, comwareHeader)
	if !seen {
		return -1, false
	}
	col := 0
	if comwareNameLead.MatchString(text) {
		col = 1
	}
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) <= col || !comwarePort.MatchString(f[col]) {
			return -1, false
		}
	}
	return len(lines), len(lines) == 0
}

var (
	cdpNone    = regexp.MustCompile(`(?i)Total cdp entries displayed\s*:\s*0|no cdp neighbors`)
	iosRecord  = regexp.MustCompile(`(?im)^\s*Device ID:\s*\S+`)
	nxosRecord = regexp.MustCompile(`(?im)^\s*Device[ -]ID:`)
)

func iosCount(text string) (int, bool) {
	return len(iosRecord.FindAllStringIndex(text, -1)), cdpNone.MatchString(text)
}

func nxosCount(text string) (int, bool) {
	return len(nxosRecord.FindAllStringIndex(text, -1)), cdpNone.MatchString(text)
}

var vrpHeader = regexp.MustCompile(`(?i)^[ \t]*Local Intf[ \t]+Neighbor Dev[ \t]+Neighbor Intf[ \t]+Exptime`)

func vrpCount(text string) (int, bool) {
	lines, seen := tableLines(text, vrpHeader)
	return len(lines), seen && len(lines) == 0
}

var (
	junosNone   = regexp.MustCompile(`(?im)^\s*(?:No LLDP neighbors\.?|Total entries displayed\s*:\s*0)\s*$`)
	junosField  = regexp.MustCompile(`(?im)^\s*(?:Local Interface|Chassis (?:ID|Id)|Port ID|System name)\s*:`)
	junosRecord = regexp.MustCompile(`(?im)^\s*Local Interface\s*:`)
)

func junosCount(text string) (int, bool) {
	return len(junosRecord.FindAllStringIndex(text, -1)), junosNone.MatchString(text) && !junosField.MatchString(text)
}

var (
	hillstoneSkip  = regexp.MustCompile(`(?i)^(?:System Name\s+Local Interface\b|Total lldp neighbor number:\s*\d+\.?$|[-=+ ]+$|\S+(?:#|\([MBF]\))$|show lldp neighbor-information$)`)
	hillstoneRow   = regexp.MustCompile(`(?i)^\S+\s+(?:(?:x?ethernet|eth|xe)\d+/\d+|(?:aggregate|agg)\d+)(?:\.\d+)?\s`)
	hillstoneTotal = regexp.MustCompile(`(?i)Total lldp neighbor number:\s*(\d+)`)
	hillstoneNone  = regexp.MustCompile(`(?i)Total lldp neighbor number:\s*0\b|System Name\s+Local Interface`)
)

func hillstoneCount(text string) (int, bool) {
	n := 0
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || hillstoneSkip.MatchString(line) {
			continue
		}
		if !hillstoneRow.MatchString(line) {
			return -1, false
		}
		n++
	}
	if m := hillstoneTotal.FindStringSubmatch(text); m != nil {
		if total, _ := strconv.Atoi(m[1]); total != n {
			return -1, false
		}
	}
	return n, n == 0 && hillstoneNone.MatchString(text)
}

var (
	panosLocal    = regexp.MustCompile(`(?im)^\s*Local information:\s*$`)
	panosIface    = regexp.MustCompile(`(?im)^\s*Local interface:\s*\S+`)
	panosNeighbor = regexp.MustCompile(`(?im)^\s*Neighbor information:\s*$`)
	panosNone     = regexp.MustCompile(`(?i)No LLDP neighbors`)
)

func panosCount(text string) (int, bool) {
	total, blank := 0, 0
	for _, block := range panosLocal.Split(text, -1) {
		if !panosIface.MatchString(block) {
			continue
		}
		total++
		parts := panosNeighbor.Split(block, 2)
		if len(parts) != 2 {
			continue
		}
		if blankBlock(parts[1]) {
			blank++
		}
	}
	return total - blank, panosNone.MatchString(text) || total > 0 && total == blank
}

// blankBlock reports whether s holds nothing but blank and prompt lines.
func blankBlock(s string) bool {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" && !promptLine.MatchString(line) {
			return false
		}
	}
	return true
}
