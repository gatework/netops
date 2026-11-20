package describe

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"github.com/gatework/netops/iface"
	"github.com/gatework/netops/neighbor"
)

// ErrInvalid reports an invalid policy, description or change.
var ErrInvalid = errors.New("describe: invalid")

// MaxDescription bounds a description; most platforms accept 80 or more.
const MaxDescription = 80

// ValidDescription reports whether s is safe to send in a description
// command: 1..80 letters, digits and "_ . : / -" or space.
func ValidDescription(s string) bool {
	if s == "" || len(s) > MaxDescription || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune("_.: /-", r) {
			return false
		}
	}
	return true
}

// DefaultTemplate is the description of a port facing neighbor port .Port
// of system .System.
const DefaultTemplate = `To_{{.System}}_{{.Port}}`

// Policy decides the description a neighbor implies.
type Policy struct {
	// Template is a text/template over Fields; empty means DefaultTemplate.
	Template string
	// FullNames keeps the neighbor's port name as advertised instead of
	// abbreviating it ("GigabitEthernet1/0/1" rather than "Gi1/0/1").
	FullNames bool
	// Lowercase lowercases the port name.
	Lowercase bool
	// Equivalent decides whether the current description already says
	// the same; nil uses Equivalent.
	Equivalent func(current, expected string) bool
}

// Fields are the values a description template sees. Spaces in names are
// replaced with underscores.
type Fields struct {
	System    string
	Port      string // formatted per FullNames and Lowercase
	LocalPort string
	Protocol  neighbor.Protocol
}

// Describe returns the description of a port facing n.
func (p Policy) Describe(n neighbor.Neighbor) (string, error) {
	t, err := p.template()
	if err != nil {
		return "", err
	}
	return p.describe(t, n)
}

// Equivalent reports whether two descriptions say the same thing: equal
// ignoring case, or equal up to the spelling of a trailing interface name
// ("To_sw1_GE1/0/1" and "to_sw1_Gi1/0/1").
func Equivalent(current, expected string) bool {
	if strings.EqualFold(current, expected) {
		return true
	}
	i, j := strings.LastIndex(current, "_"), strings.LastIndex(expected, "_")
	if i < 0 || j < 0 || !strings.EqualFold(current[:i], expected[:j]) {
		return false
	}
	a, b := current[i+1:], expected[j+1:]
	return iface.Known(a) && iface.Known(b) && iface.Equal(a, b)
}

// Status is the verdict on one port.
type Status string

// Port verdicts.
const (
	StatusMatches    Status = "matches"
	StatusMissing    Status = "missing_description"
	StatusMismatch   Status = "content_mismatch"
	StatusUnverified Status = "unverified" // see Comparison.Reason
)

// Evidence issues that leave a port unverified.
const (
	ReasonAmbiguous = "ambiguous_neighbor" // more than one neighbor on the port
	ReasonNoPort    = "interface_missing"  // the port is not in the configuration
	ReasonUnsafe    = "unsafe_description" // the implied description or port name is unsafe
)

// Comparison is the verdict on one port with a neighbor.
type Comparison struct {
	Interface string `json:"interface"`
	Neighbor  string `json:"neighbor"`
	Port      string `json:"port"`
	Current   string `json:"current,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Status    Status `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// Audit is the verdict on every port with a neighbor, in neighbor order.
type Audit struct {
	Platform    string       `json:"platform"`
	Comparisons []Comparison `json:"comparisons"`
}

// Audit compares m's descriptions with those its neighbors imply. Ports
// whose evidence is not conclusive are reported unverified with a reason;
// they never produce a change.
func (p Policy) Audit(m Model) (Audit, error) {
	t, err := p.template()
	if err != nil {
		return Audit{}, err
	}
	equivalent := p.Equivalent
	if equivalent == nil {
		equivalent = Equivalent
	}
	groups := neighbor.ByLocalPort(m.Neighbors)
	a := Audit{Platform: m.Platform}
	seen := map[string]bool{}
	for _, n := range m.Neighbors {
		k := iface.Key(n.LocalPort)
		if seen[k] {
			continue
		}
		seen[k] = true
		c := Comparison{Interface: n.LocalPort, Neighbor: n.System, Port: n.Port, Status: StatusUnverified}
		port, configured := m.Ports[k]
		if configured {
			c.Interface, c.Current = port.Name, port.Description
		}
		switch {
		case len(groups[k]) != 1:
			c.Reason = ReasonAmbiguous
		case n.Issue() != "":
			c.Reason = n.Issue()
		case !configured:
			c.Reason = ReasonNoPort
		}
		if c.Reason == "" {
			want, err := p.describe(t, n)
			if err != nil || !iface.Safe(port.Name) {
				c.Reason = ReasonUnsafe
			} else {
				c.Expected = want
				switch {
				case equivalent(port.Description, want):
					c.Status = StatusMatches
				case port.Description == "":
					c.Status = StatusMissing
				default:
					c.Status = StatusMismatch
				}
			}
		}
		a.Comparisons = append(a.Comparisons, c)
	}
	return a, nil
}

func (p Policy) template() (*template.Template, error) {
	src := p.Template
	if src == "" {
		src = DefaultTemplate
	}
	t, err := template.New("description").Option("missingkey=error").Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%w: template: %w", ErrInvalid, err)
	}
	return t, nil
}

func (p Policy) describe(t *template.Template, n neighbor.Neighbor) (string, error) {
	port := n.Port
	if !p.FullNames {
		port = iface.Short(port)
	}
	if p.Lowercase {
		port = strings.ToLower(port)
	}
	under := strings.NewReplacer(" ", "_", "\t", "_")
	var b strings.Builder
	err := t.Execute(&b, Fields{System: under.Replace(n.System), Port: under.Replace(port), LocalPort: n.LocalPort, Protocol: n.Protocol})
	if err != nil {
		return "", fmt.Errorf("%w: template: %w", ErrInvalid, err)
	}
	if !ValidDescription(b.String()) {
		return "", fmt.Errorf("%w: description %q", ErrInvalid, b.String())
	}
	return b.String(), nil
}

// Unverified returns the comparisons with evidence issues.
func (a Audit) Unverified() []Comparison {
	var out []Comparison
	for _, c := range a.Comparisons {
		if c.Status == StatusUnverified {
			out = append(out, c)
		}
	}
	return out
}

// Change returns the change that corrects every verified mismatch, or false
// when there is nothing to correct.
func (a Audit) Change() (Change, bool) {
	ch := Change{Platform: a.Platform}
	for _, c := range a.Comparisons {
		if c.Status == StatusMissing || c.Status == StatusMismatch {
			ch.Items = append(ch.Items, Item{Interface: c.Interface, Description: c.Expected})
		}
	}
	return ch, len(ch.Items) > 0
}
