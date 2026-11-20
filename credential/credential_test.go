package credential_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"github.com/gatework/netops/credential"
	"github.com/gatework/netops/device"
)

func TestEnv_Lookup(t *testing.T) {
	t.Parallel()
	d := device.Device{ID: "ac1", Mgmt: netip.MustParseAddr("192.0.2.1"), Platform: "h3c_wireless"}
	env := credential.Env{Environ: map[string]string{
		"NC_H3C_WIRELESS_USERNAME": "wlan", "NC_DEVICE_USERNAME": "generic", "NC_DEVICE_PASSWORD": "pw",
	}}
	c, err := env.Lookup(t.Context(), d)
	if err != nil || c.Username != "wlan" || c.Password.Reveal() != "pw" {
		t.Errorf("fallback per field: %+v %v", c, err)
	}
	_, err = credential.Env{}.Lookup(t.Context(), d)
	if !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("empty env: %v", err)
	}
	chain := credential.Chain{credential.Env{}, credential.Static{Username: "u", IdentityFile: "/k"}}
	if c, err := chain.Lookup(t.Context(), d); err != nil || c.Username != "u" {
		t.Errorf("chain %+v %v", c, err)
	}
}

func ExampleEnv() {
	d := device.Device{ID: "sw1", Mgmt: netip.MustParseAddr("192.0.2.1"), Platform: "cisco_nxos"}
	c, _ := credential.Env{Environ: map[string]string{"NC_CISCO_NXOS_USERNAME": "admin", "NC_CISCO_NXOS_PASSWORD": "pw"}}.Lookup(context.Background(), d)
	fmt.Printf("%s %v\n", c.Username, c.Password)
	// Output: admin [REDACTED]
}
