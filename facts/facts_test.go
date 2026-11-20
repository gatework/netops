package facts_test

import (
	"errors"
	"testing"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/facts"
)

// The outputs follow the vendors' published command references (carried
// over from the retired net-connector facts suite); they are not captures
// from production devices.
var fixtures = map[string]struct {
	outputs, partial []string
	want, wantPart   facts.Facts
}{
	"cisco_ios": {
		outputs:  []string{"Cisco IOS Software, C3750 Software (C3750-IPSERVICESK9-M), Version 12.2(55)SE9, RELEASE SOFTWARE (fc1)\r\nROM: Bootstrap program is C3750 boot loader\r\n\r\ncore-sw uptime is 1 week, 2 days, 3 hours, 4 minutes\r\nSystem image file is \"flash:c3750.bin\"\r\n\r\ncisco WS-C3750G-24TS (PowerPC405) processor (revision H0) with 131072K bytes of memory.\r\nProcessor board ID FOC1234X5YZ\r\n"},
		want:     facts.Facts{Hostname: "core-sw", Model: "WS-C3750G-24TS", Serial: "FOC1234X5YZ", Version: "12.2(55)SE9", Uptime: "1 week, 2 days, 3 hours, 4 minutes"},
		partial:  []string{"Cisco IOS XE Software, Catalyst L3 Switch Software (CAT9K_IOSXE), Version 16.9.4, RELEASE SOFTWARE (fc2)\r\n"},
		wantPart: facts.Facts{Version: "16.9.4"},
	},
	"cisco_nxos": {
		outputs:  []string{"Cisco Nexus Operating System (NX-OS) Software\r\nSoftware\r\n  BIOS: version 07.69\r\n  NXOS: version 9.3(8)\r\nHardware\r\n  cisco Nexus9000 C93180YC-EX chassis\r\n  Intel(R) Xeon(R) CPU with 24633476 kB of memory.\r\n  Processor Board ID FDO21120XYZ\r\n\r\n  Device name: n9k-1\r\n  bootflash:   53298520 kB\r\nKernel uptime is 10 day(s), 3 hour(s), 5 minute(s), 20 second(s)\r\n"},
		want:     facts.Facts{Hostname: "n9k-1", Model: "Nexus9000 C93180YC-EX", Serial: "FDO21120XYZ", Version: "9.3(8)", Uptime: "10 day(s), 3 hour(s), 5 minute(s), 20 second(s)"},
		partial:  []string{"Software\r\n  system:    version 7.0(3)I7(9)\r\n"},
		wantPart: facts.Facts{Version: "7.0(3)I7(9)"},
	},
	"h3c": {
		outputs: []string{
			"H3C Comware Software, Version 7.1.070, Release 6126P20\r\nCopyright (c) 2004-2021 New H3C Technologies Co., Ltd. All rights reserved.\r\nH3C S6800-54QF uptime is 12 weeks, 3 days, 4 hours, 5 minutes\r\nLast reboot reason : Cold reboot\r\n",
			" Slot 1 CPU 0:\r\n DEVICE_NAME          : S6800-54QF\r\n DEVICE_SERIAL_NUMBER : 210235A1ABCDEF000001\r\n MAC_ADDRESS          : 00E0-FC00-0001\r\n Fan 1:\r\n DEVICE_NAME          : LSWM1FANSA\r\n DEVICE_SERIAL_NUMBER : 210231A9FAN000000002\r\n",
		},
		want:     facts.Facts{Model: "S6800-54QF", Serial: "210235A1ABCDEF000001", Version: "7.1.070, Release 6126P20", Uptime: "12 weeks, 3 days, 4 hours, 5 minutes"},
		partial:  []string{"H3C Comware Software, Version 7.1.070, Release 6126P20\r\n", ""},
		wantPart: facts.Facts{Version: "7.1.070, Release 6126P20"},
	},
	"huawei": {
		outputs: []string{
			"Huawei Versatile Routing Platform Software\r\nVRP (R) software, Version 5.170 (S5720 V200R011C10SPC600)\r\nCopyright (C) 2000-2018 HUAWEI TECH Co., Ltd.\r\nHUAWEI S5720-28X-SI-AC Routing Switch uptime is 5 weeks, 1 day, 2 hours, 3 minutes\r\n",
			"ESN of slot 0: 2102350DLR10K1000123\r\n",
		},
		want:     facts.Facts{Model: "S5720-28X-SI-AC", Serial: "2102350DLR10K1000123", Version: "5.170 (S5720 V200R011C10SPC600)", Uptime: "5 weeks, 1 day, 2 hours, 3 minutes"},
		partial:  []string{"VRP (R) software, Version 8.180 (CE6850EI V200R005C10SPC800)\r\n", ""},
		wantPart: facts.Facts{Version: "8.180 (CE6850EI V200R005C10SPC800)"},
	},
	"juniper": {
		outputs: []string{
			"Hostname: mx1\r\nModel: mx480\r\nJunos: 20.4R3-S2.6\r\nJUNOS OS Kernel 64-bit  [20210618.f43645e_builder_stable_11]\r\n",
			"Hardware inventory:\r\nItem             Version  Part number  Serial number     Description\r\nChassis                                JN1234567AFA      MX480\r\nMidplane         REV 07   710-017414   ACRA1234          MX480 Midplane\r\n",
		},
		want:     facts.Facts{Hostname: "mx1", Model: "mx480", Serial: "JN1234567AFA", Version: "20.4R3-S2.6"},
		partial:  []string{"Junos: 18.4R2.7\r\n", ""},
		wantPart: facts.Facts{Version: "18.4R2.7"},
	},
	"fortinet": {
		outputs:  []string{"Version: FortiGate-100F v7.0.12,build0523,230606 (GA.M)\r\nVirus-DB: 91.00000(2023-01-01 00:00)\r\nSerial-Number: FG100FTK20012345\r\nBIOS version: 05000007\r\nHostname: fw1\r\nOperation Mode: NAT\r\n"},
		want:     facts.Facts{Hostname: "fw1", Model: "FortiGate-100F", Serial: "FG100FTK20012345", Version: "v7.0.12,build0523,230606"},
		partial:  []string{"Version: FortiGate-VM64 v7.2.5,build1517,230606 (GA.F)\r\n"},
		wantPart: facts.Facts{Model: "FortiGate-VM64", Version: "v7.2.5,build1517,230606"},
	},
}

func TestParse(t *testing.T) {
	t.Parallel()
	for platform, f := range fixtures {
		p, ok := facts.Lookup(platform)
		if !ok {
			t.Fatalf("%s: no probe", platform)
		}
		if len(p.Commands) != len(f.outputs) {
			t.Fatalf("%s: %d outputs for %d commands", platform, len(f.outputs), len(p.Commands))
		}
		if got, err := p.Parse(f.outputs); err != nil || got != f.want {
			t.Errorf("%s: %+v %v, want %+v", platform, got, err, f.want)
		}
		if got, err := p.Parse(f.partial); err != nil || got != f.wantPart {
			t.Errorf("%s partial: %+v %v, want %+v", platform, got, err, f.wantPart)
		}
		bad := make([]string, len(f.outputs))
		bad[0] = "% Unrecognized command found at '^' position.\r\n"
		if _, err := p.Parse(bad); !errors.Is(err, facts.ErrUnrecognized) {
			t.Errorf("%s: unrelated output: %v", platform, err)
		}
	}
	if len(facts.Platforms()) != 7 {
		t.Errorf("platforms = %v", facts.Platforms())
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()
	d := device.Device{Name: "core-sw", Model: "H3C S6800-54QF", Version: "7.1.070"}
	if m := facts.Compare(d, facts.Facts{Hostname: "CORE-SW", Model: "S6800-54QF", Version: "7.1.070, Release 6126P20"}); len(m) != 0 {
		t.Errorf("matching facts: %+v", m)
	}
	m := facts.Compare(d, facts.Facts{Hostname: "core-sw-2", Model: "S5560X", Version: "7.1.064"})
	if len(m) != 3 || m[0].Field != "name" || m[1].Field != "model" || m[2].Field != "version" {
		t.Errorf("mismatches = %+v", m)
	}
	if m := facts.Compare(device.Device{}, facts.Facts{Version: "1"}); len(m) != 0 {
		t.Errorf("empty inventory compared: %+v", m)
	}
}
