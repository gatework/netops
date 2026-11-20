package config

import (
	"regexp"
	"slices"
	"strings"

	"github.com/gatework/netops/secret"
)

// Dialect describes the configuration syntax of a CLI family. Dialects are
// values; the constructors return fresh copies.
type Dialect struct {
	// Name identifies the dialect, such as "cisco" or "comware".
	Name string
	// Comments are line prefixes (after indentation) of comment lines.
	Comments []string
	// Separators are whole lines that close every open block, such as "#".
	Separators []string
	// Terminators are whole lines that end a block or the file and carry no
	// configuration, such as "exit", "quit", "return" and "end".
	Terminators []string
	// Volatile matches lines that change without a configuration change.
	// They are left out of the normalized text and the digest.
	Volatile []*regexp.Regexp
	// Sensitive matches secrets; the first capture group is masked by Mask.
	Sensitive []*regexp.Regexp
}

// Cisco returns the dialect of IOS, IOS-XE and NX-OS running configurations.
func Cisco() Dialect {
	return Dialect{
		Name:        "cisco",
		Comments:    []string{"!"},
		Terminators: []string{"end", "exit"},
		Volatile: []*regexp.Regexp{
			regexp.MustCompile(`^Building configuration`),
			regexp.MustCompile(`^Current configuration\s*:`),
			regexp.MustCompile(`^!(Command|Time|Running configuration last done at):`),
			regexp.MustCompile(`^! (Last configuration change|NVRAM config last updated) at`),
			regexp.MustCompile(`^\s*ntp clock-period\s`),
		},
		Sensitive: commonSensitive(),
	}
}

// Comware returns the dialect of H3C Comware and Huawei VRP configurations:
// "#" separators, one-space indentation and "return" at the end.
func Comware() Dialect {
	return Dialect{
		Name:        "comware",
		Separators:  []string{"#"},
		Terminators: []string{"return", "quit"},
		Volatile: []*regexp.Regexp{
			regexp.MustCompile(`^!(Software Version|Last configuration was)`),
		},
		Sensitive: commonSensitive(),
	}
}

func commonSensitive() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:password|secret|passphrase|preshared-key|pre-shared-key|key-string|auth-key|authentication-key)\s+(?:(?:pass-phrase|raw|cipher|simple|hash|encrypted|plain|0|5|7|8|9)\s+){0,2}(\S+)`),
		regexp.MustCompile(`(?i)\bsnmp-agent community\s+(?:read|write)\s+(?:(?:cipher|simple)\s+)?(\S+)`),
		regexp.MustCompile(`(?i)\bsnmp-server community\s+(\S+)`),
		regexp.MustCompile(`(?i)\b(?:tacacs-server|radius-server)\b.*\bkey\s+(?:\d\s+)?(\S+)`),
		regexp.MustCompile(`(?i)\bkey\s+(?:cipher|simple)\s+(\S+)`),
	}
}

// Mask replaces the secret values in line with secret.Mask.
func (d Dialect) Mask(line string) string {
	for _, re := range d.Sensitive {
		line = maskGroup(re, line)
	}
	return line
}

// SensitiveLine reports whether line carries a secret.
func (d Dialect) SensitiveLine(line string) bool {
	return slices.ContainsFunc(d.Sensitive, func(re *regexp.Regexp) bool { return re.MatchString(line) })
}

func (d Dialect) comment(text string) bool {
	t := strings.TrimSpace(text)
	return slices.ContainsFunc(d.Comments, func(p string) bool { return strings.HasPrefix(t, p) })
}

func (d Dialect) separator(text string) bool {
	return slices.Contains(d.Separators, strings.TrimSpace(text))
}

func (d Dialect) terminator(text string) bool {
	return slices.Contains(d.Terminators, strings.TrimSpace(text))
}

func (d Dialect) volatile(text string) bool {
	return slices.ContainsFunc(d.Volatile, func(re *regexp.Regexp) bool { return re.MatchString(text) })
}

func maskGroup(re *regexp.Regexp, line string) string {
	matches := re.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return line
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		if len(m) < 4 || m[2] < 0 {
			continue
		}
		b.WriteString(line[last:m[2]])
		b.WriteString(secret.Mask)
		last = m[3]
	}
	b.WriteString(line[last:])
	return b.String()
}
