package lifecycle

import (
	"bytes"
	"fmt"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
)

// Status is one lifecycle verdict.
type Status string

// Statuses, from worst to best.
const (
	StatusBlocked          Status = "blocked"        // runs a release that must not run
	StatusEndOfSupport     Status = "end_of_support" // the vendor no longer supports it
	StatusBelowMinimum     Status = "below_minimum"  // older than the minimum release
	StatusEndOfSupportSoon Status = "end_of_support_soon"
	StatusNotTarget        Status = "not_target"  // acceptable but not the standard release
	StatusEndOfSale        Status = "end_of_sale" // no longer sold; plan replacement
	StatusUnknown          Status = "unknown"     // the version is not known
	StatusUnmanaged        Status = "unmanaged"   // no rule matches the device
	StatusCompliant        Status = "compliant"
)

var order = []Status{
	StatusBlocked, StatusEndOfSupport, StatusBelowMinimum, StatusEndOfSupportSoon,
	StatusNotTarget, StatusEndOfSale, StatusUnknown, StatusUnmanaged, StatusCompliant,
}

// Severity returns the finding severity of s.
func (s Status) Severity() assess.Severity {
	switch s {
	case StatusBlocked:
		return assess.SeverityCritical
	case StatusEndOfSupport, StatusBelowMinimum:
		return assess.SeverityHigh
	case StatusEndOfSupportSoon, StatusNotTarget:
		return assess.SeverityMedium
	case StatusEndOfSale, StatusUnknown:
		return assess.SeverityLow
	case StatusUnmanaged, StatusCompliant:
		return assess.SeverityInfo
	}
	return assess.SeverityInfo
}

// Rule is the version policy of the devices it matches.
type Rule struct {
	Name     string   `yaml:"name" json:"name"`
	Match    []string `yaml:"match" json:"match"` // device selector clauses; empty matches all
	Target   string   `yaml:"target" json:"target,omitempty"`
	Minimum  string   `yaml:"minimum" json:"minimum,omitempty"`
	Blocked  []string `yaml:"blocked" json:"blocked,omitempty"`
	Advisory string   `yaml:"advisory" json:"advisory,omitempty"` // reference shown with findings
	// EndOfSale and EndOfSupport are dates, YYYY-MM-DD.
	EndOfSale    string `yaml:"end_of_sale" json:"end_of_sale,omitempty"`
	EndOfSupport string `yaml:"end_of_support" json:"end_of_support,omitempty"`

	selector        device.Selector
	target, minimum Version
	blocked         []Version
	eos, eosup      time.Time
}

// Policy is an ordered list of rules; the first match decides.
type Policy struct {
	// SupportWarning is how long before end of support to warn; 0 means 180 days.
	SupportWarning time.Duration `yaml:"support_warning" json:"support_warning,omitempty"`
	Rules          []Rule        `yaml:"rules" json:"rules"`
}

// Decode parses a YAML policy strictly and compiles it.
func Decode(body []byte) (Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("lifecycle: decode: %w", err)
	}
	return Compile(p)
}

// Compile validates and prepares the rules and returns the policy ready
// for use; Decode calls it.
func Compile(p Policy) (Policy, error) {
	p.Rules = append([]Rule(nil), p.Rules...)
	err := compile(&p)
	return p, err
}

func compile(p *Policy) error {
	if p.SupportWarning == 0 {
		p.SupportWarning = 180 * 24 * time.Hour
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		var err error
		if r.Name == "" {
			return fmt.Errorf("lifecycle: rule %d has no name", i)
		}
		if r.selector, err = device.ParseSelector(r.Match...); err != nil {
			return fmt.Errorf("lifecycle: rule %s: %w", r.Name, err)
		}
		r.target, r.minimum = ParseVersion(r.Target), ParseVersion(r.Minimum)
		if r.target.Known() && r.minimum.Known() && r.target.Compare(r.minimum) < 0 {
			return fmt.Errorf("lifecycle: rule %s: target %s is below minimum %s", r.Name, r.Target, r.Minimum)
		}
		r.blocked = r.blocked[:0]
		for _, b := range r.Blocked {
			r.blocked = append(r.blocked, ParseVersion(b))
		}
		if r.eos, err = date(r.EndOfSale); err != nil {
			return fmt.Errorf("lifecycle: rule %s: end_of_sale: %w", r.Name, err)
		}
		if r.eosup, err = date(r.EndOfSupport); err != nil {
			return fmt.Errorf("lifecycle: rule %s: end_of_support: %w", r.Name, err)
		}
	}
	return nil
}

func date(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q is not YYYY-MM-DD: %w", s, err)
	}
	return t, nil
}

// Verdict is the lifecycle state of one device.
type Verdict struct {
	Device   device.Device `json:"device"`
	Rule     string        `json:"rule,omitempty"`
	Version  string        `json:"version"`
	Status   Status        `json:"status"`             // the worst of Statuses
	Statuses []Status      `json:"statuses,omitempty"` // every problem, worst first
	Target   string        `json:"target,omitempty"`
	Advisory string        `json:"advisory,omitempty"`
}

// Evaluate judges the device's Version at now under the first matching
// rule.
func (p Policy) Evaluate(d device.Device, now time.Time) Verdict {
	v := Verdict{Device: d.Clone(), Version: d.Version, Status: StatusUnmanaged}
	i := slices.IndexFunc(p.Rules, func(r Rule) bool { return r.selector.Match(d) })
	if i < 0 {
		return v
	}
	r := p.Rules[i]
	v.Rule, v.Target, v.Advisory = r.Name, r.Target, r.Advisory
	have := ParseVersion(d.Version)
	var got []Status
	add := func(s Status) {
		if !slices.Contains(got, s) {
			got = append(got, s)
		}
	}
	switch {
	case !have.Known():
		add(StatusUnknown)
	default:
		if slices.ContainsFunc(r.blocked, have.Equal) {
			add(StatusBlocked)
		}
		if r.minimum.Known() && have.Compare(r.minimum) < 0 {
			add(StatusBelowMinimum)
		}
		if r.target.Known() && !have.Equal(r.target) {
			add(StatusNotTarget)
		}
	}
	switch {
	case !r.eosup.IsZero() && !now.Before(r.eosup):
		add(StatusEndOfSupport)
	case !r.eosup.IsZero() && now.Add(p.SupportWarning).After(r.eosup):
		add(StatusEndOfSupportSoon)
	}
	if !r.eos.IsZero() && !now.Before(r.eos) {
		add(StatusEndOfSale)
	}
	if len(got) == 0 {
		got = []Status{StatusCompliant}
	}
	slices.SortFunc(got, func(a, b Status) int { return slices.Index(order, a) - slices.Index(order, b) })
	v.Statuses, v.Status = got, got[0]
	return v
}

// EvaluateAll judges every device and returns the verdicts worst first, then
// by device.
func (p Policy) EvaluateAll(devices []device.Device, now time.Time) []Verdict {
	out := make([]Verdict, 0, len(devices))
	for _, d := range devices {
		out = append(out, p.Evaluate(d, now))
	}
	slices.SortStableFunc(out, func(a, b Verdict) int {
		if c := slices.Index(order, a.Status) - slices.Index(order, b.Status); c != 0 {
			return c
		}
		return device.Compare(a.Device, b.Device)
	})
	return out
}

// Compliant reports whether the verdict needs no action. End of sale alone
// is informational.
func (v Verdict) Compliant() bool {
	return v.Status == StatusCompliant || v.Status == StatusEndOfSale || v.Status == StatusUnmanaged
}

// Finding converts the verdict to a finding of rule "lifecycle.<status>".
func (v Verdict) Finding() assess.Finding {
	f := assess.Finding{
		Rule:     assess.RuleID("lifecycle." + string(v.Status)),
		Severity: v.Status.Severity(),
		Category: assess.CategoryCompliance,
		Subject:  assess.NewSubject("device", v.Device.Label()),
		Title:    "software " + string(v.Status),
		Expected: v.Target,
		Actual:   v.Version,
	}
	for _, s := range v.Statuses {
		f.Fields = append(f.Fields, evidenceField("status", string(s)))
	}
	if v.Rule != "" {
		f.Fields = append(f.Fields, evidenceField("rule", v.Rule))
	}
	if v.Advisory != "" {
		f.References = []string{v.Advisory}
	}
	return f
}

func evidenceField(k, v string) evidence.Field { return evidence.Field{Key: k, Value: v} }
