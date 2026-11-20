package device

import (
	"regexp"
	"strings"
)

// Identity is what an inventory source reports about a device's make: the
// vendor, operating system and model labels. Platform classification works
// on the normalized form.
type Identity struct {
	Vendor string `json:"vendor,omitempty"`
	OS     string `json:"os,omitempty"`
	Model  string `json:"model,omitempty"`
}

var labelSeparators = regexp.MustCompile(`[^a-z0-9]+`)

// NormalizeLabel lowercases an inventory label and collapses every run of
// other characters into one space, so "H3C-Comware" and "h3c comware"
// compare equal.
func NormalizeLabel(s string) string {
	return strings.TrimSpace(labelSeparators.ReplaceAllString(strings.ToLower(s), " "))
}

// Normalized returns the identity with every label normalized.
func (i Identity) Normalized() Identity {
	return Identity{Vendor: NormalizeLabel(i.Vendor), OS: NormalizeLabel(i.OS), Model: NormalizeLabel(i.Model)}
}
