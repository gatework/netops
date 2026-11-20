package iface_test

import (
	"testing"

	"github.com/gatework/netops/iface"
)

func TestNames(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, key, short, long string }{
		{"GigabitEthernet1/0/1", "gigabitethernet1/0/1", "Gi1/0/1", "GigabitEthernet1/0/1"},
		{"GE1/0/1", "gigabitethernet1/0/1", "Gi1/0/1", "GigabitEthernet1/0/1"},
		{"g1/0/1", "gigabitethernet1/0/1", "Gi1/0/1", "GigabitEthernet1/0/1"},
		{"XGE1/0/49", "tengigabitethernet1/0/49", "Te1/0/49", "Ten-GigabitEthernet1/0/49"},
		{"Te1/1", "tengigabitethernet1/1", "Te1/1", "TenGigabitEthernet1/1"},
		{"Ten-GigabitEthernet1/0/49", "tengigabitethernet1/0/49", "Te1/0/49", "Ten-GigabitEthernet1/0/49"},
		{"HGE1/0/1", "hundredgigabitethernet1/0/1", "Hu1/0/1", "HGE1/0/1"},
		{"BAGG10", "bridge-aggregation10", "BAGG10", "Bridge-Aggregation10"},
		{"Eth1/1", "ethernet1/1", "Eth1/1", "Ethernet1/1"},
		{"Po10", "port-channel10", "Po10", "port-channel10"},
		{"GigabitEthernet 0/0/1", "gigabitethernet0/0/1", "Gi0/0/1", "GigabitEthernet0/0/1"}, // VRP spacing
		{"ge-0/0/1", "ge-0/0/1", "ge-0/0/1", "ge-0/0/1"},                                     // Junos: unknown family
		{"mgmt0", "mgmt0", "mgmt0", "mgmt0"},
	}
	for _, tt := range tests {
		if got := iface.Key(tt.in); got != tt.key {
			t.Errorf("Key(%q) = %q, want %q", tt.in, got, tt.key)
		}
		if got := iface.Short(tt.in); got != tt.short {
			t.Errorf("Short(%q) = %q, want %q", tt.in, got, tt.short)
		}
		if got := iface.Long(tt.in); got != tt.long {
			t.Errorf("Long(%q) = %q, want %q", tt.in, got, tt.long)
		}
	}
	if !iface.Equal("GE1/0/1", "Gi1/0/1") || iface.Equal("GE1/0/1", "GE1/0/2") {
		t.Error("Equal")
	}
	if !iface.Safe("Ten-GigabitEthernet1/0/1.100") || iface.Safe("Gi1/0/1; reload") || iface.Safe("") {
		t.Error("Safe")
	}
}
