package config_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gatework/netops/config"
	"github.com/gatework/netops/parse"
)

const comware = `#
 version 7.1.064, Release 5474
#
 sysname AC1
#
interface Vlan-interface10
 ip address 192.0.2.1 255.255.255.0
#
wlan service-template corp
 preshared-key pass-phrase cipher $c$3$abcdef==
 service-template enable
#
return
`

func TestParse_comware(t *testing.T) {
	t.Parallel()
	doc, err := config.Parse(t.Context(), comware, config.Comware(), parse.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	top := []string{}
	for _, n := range doc.Root().Children {
		top = append(top, n.Text)
	}
	want := []string{"version 7.1.064, Release 5474", "sysname AC1", "interface Vlan-interface10", "wlan service-template corp"}
	if !slices.Equal(top, want) {
		t.Errorf("top = %q", top)
	}
	if n, ok := doc.Section("wlan service-template corp", "service-template enable"); !ok || n.Line.Number != 11 {
		t.Error("Section lookup")
	}
	masked := strings.Join(doc.Masked(), "\n")
	if strings.Contains(masked, "abcdef") || !strings.Contains(masked, "[REDACTED]") {
		t.Errorf("not masked:\n%s", masked)
	}
}

func TestDigest_ignoresVolatileLines(t *testing.T) {
	t.Parallel()
	a := "!Command: show running-config\n!Time: Mon Jan 1 2026\nhostname sw1\n!\ninterface Eth1/1\n  description up\n"
	b := "!Command: show running-config\n!Time: Tue Jan 2 2026\nhostname sw1\n!\ninterface Eth1/1\n description up\n"
	da, _ := config.Parse(t.Context(), a, config.Cisco(), parse.Limits{})
	db, _ := config.Parse(t.Context(), b, config.Cisco(), parse.Limits{})
	if da.Digest() != db.Digest() || da.RawDigest() == db.RawDigest() {
		t.Error("volatile lines or indentation width changed the digest")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(comware)
	f.Add("hostname a\n interface x\n\t ip y\n!\n")
	f.Fuzz(func(t *testing.T, s string) {
		doc, err := config.Parse(t.Context(), s, config.Comware(), parse.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for range doc.Root().Walk() {
			n++
		}
		if n != len(doc.Normalized()) {
			t.Fatal("normalized text and tree disagree")
		}
	})
}

func ExampleDialect_Mask() {
	fmt.Println(config.Cisco().Mask("snmp-server community Pr1vate RO"))
	// Output: snmp-server community [REDACTED] RO
}
