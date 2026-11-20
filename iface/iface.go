package iface

import (
	"regexp"
	"strings"
)

// family is one interface family: the spellings it accepts, its comparison
// stem, its abbreviation and, when commands need it, its configuration
// name.
type family struct {
	spellings string // alternation of lowercase prefixes, longest first
	stem      string // comparison stem
	short     string // display abbreviation
	long      string // configuration name; "" leaves the input unchanged
}

// families are matched in order, so a longer spelling must come before any
// family whose spelling is its prefix (ge before g, xge before xe).
var families = []family{
	{`bridge-aggregation|bagg`, "bridge-aggregation", "BAGG", "Bridge-Aggregation"},
	{`route-aggregation|ragg`, "route-aggregation", "RAGG", "Route-Aggregation"},
	{`aggregate|agg`, "aggregate", "Agg", ""},
	{`hundredgigabitethernet|hundredgige|hge|100ge|hu`, "hundredgigabitethernet", "Hu", ""},
	{`fortygigabitethernet|fortygige|fge|40ge|fo`, "fortygigabitethernet", "Fo", ""},
	{`twenty-?fivegigabitethernet|twenty-?fivegige|wge|25ge|twe`, "twentyfivegigabitethernet", "Twe", ""},
	{`xge|10ge|xgigabitethernet|ten-gigabitethernet|tengigabitethernet|ten-?gige|xethernet|xe|te`, "tengigabitethernet", "Te", ""},
	{`smartrate-ethernet|se`, "smartrate-ethernet", "SE", "Smartrate-Ethernet"},
	{`gigabitethernet|ge|gi|g`, "gigabitethernet", "Gi", "GigabitEthernet"},
	{`fastethernet|fa`, "fastethernet", "Fa", "FastEthernet"},
	{`ethernet|eth`, "ethernet", "Eth", "Ethernet"},
	{`port-channel|po`, "port-channel", "Po", "port-channel"},
}

var patterns = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(families))
	for i, f := range families {
		out[i] = regexp.MustCompile(`(?i)^(?:` + f.spellings + `)\s*([0-9].*)$`)
	}
	return out
}()

// longExpansions are the abbreviations whose configuration name depends on
// the spelling: Comware's XGE is "Ten-GigabitEthernet" while Cisco's Te is
// "TenGigabitEthernet".
var longExpansions = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`(?i)^xge\s*([0-9].*)$`), "Ten-GigabitEthernet"},
	{regexp.MustCompile(`(?i)^te\s*([0-9].*)$`), "TenGigabitEthernet"},
}

func split(name string) (int, string, bool) {
	name = strings.TrimSpace(name)
	for i, re := range patterns {
		if m := re.FindStringSubmatch(name); m != nil {
			return i, m[1], true
		}
	}
	return -1, name, false
}

// Known reports whether name belongs to a recognized family.
func Known(name string) bool {
	_, _, ok := split(name)
	return ok
}

// Key returns the case-insensitive comparison key of name.
func Key(name string) string {
	i, rest, ok := split(name)
	if !ok {
		return strings.ToLower(strings.TrimSpace(name))
	}
	return families[i].stem + strings.ToLower(rest)
}

// Equal reports whether a and b name the same interface.
func Equal(a, b string) bool { return Key(a) == Key(b) }

// Short returns the conventional abbreviation of name, such as "Gi1/0/1".
func Short(name string) string {
	i, rest, ok := split(name)
	if !ok {
		return strings.TrimSpace(name)
	}
	return families[i].short + rest
}

// Long returns the full name configuration commands accept, such as
// "GigabitEthernet1/0/1" for "GE1/0/1". A name that is already long, or of
// a family without a configuration name, is returned unchanged.
func Long(name string) string {
	name = strings.TrimSpace(name)
	for _, e := range longExpansions {
		if m := e.re.FindStringSubmatch(name); m != nil {
			return e.name + m[1]
		}
	}
	i, rest, ok := split(name)
	if !ok || families[i].long == "" {
		return name
	}
	return families[i].long + rest
}

var safe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.:/-]*$`)

// Safe reports whether name may be placed in an interface command: a
// letter followed by letters, digits and ". : / -".
func Safe(name string) bool { return safe.MatchString(name) }
