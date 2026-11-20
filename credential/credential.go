package credential

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/failure"
	"github.com/gatework/netops/secret"
)

// ErrNotFound reports that a provider has no credentials for a device.
var ErrNotFound = failure.New(failure.CodeNoCreds, "credential")

// Credentials are one login.
type Credentials struct {
	Username       string
	Password       secret.Secret
	EnablePassword secret.Secret
	// IdentityFile is a private key path; KeyPassphrase unlocks it.
	IdentityFile  string
	KeyPassphrase secret.Secret
}

// Valid reports whether the credentials can log in: a username and a
// password or identity file.
func (c Credentials) Valid() bool {
	return c.Username != "" && (!c.Password.Empty() || c.IdentityFile != "")
}

// Secrets returns every secret value, for building a redactor.
func (c Credentials) Secrets() []secret.Secret {
	return []secret.Secret{c.Password, c.EnablePassword, c.KeyPassphrase}
}

// Provider looks credentials up for a device.
type Provider interface {
	Lookup(ctx context.Context, d device.Device) (Credentials, error)
}

// Env maps an environment to credentials. Prefix defaults to "NC".
type Env struct {
	Prefix  string
	Environ map[string]string
}

// Lookup returns the platform-specific variables, falling back to the
// generic DEVICE ones field by field.
func (e Env) Lookup(_ context.Context, d device.Device) (Credentials, error) {
	prefix := e.Prefix
	if prefix == "" {
		prefix = "NC"
	}
	get := func(field string) string {
		if d.Platform != "" {
			if v := e.Environ[envName(prefix, d.Platform, field)]; v != "" {
				return v
			}
		}
		return e.Environ[envName(prefix, "device", field)]
	}
	c := Credentials{
		Username:       get("USERNAME"),
		Password:       secret.New(get("PASSWORD")),
		EnablePassword: secret.New(get("ENABLE_PASSWORD")),
		IdentityFile:   get("IDENTITY_FILE"),
		KeyPassphrase:  secret.New(get("KEY_PASSPHRASE")),
	}
	if !c.Valid() {
		return Credentials{}, ErrNotFound
	}
	return c, nil
}

// envName returns PREFIX_PLATFORM_FIELD with the platform upper-cased and
// non-alphanumerics replaced by "_".
func envName(prefix, platform, field string) string {
	p := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, platform)
	return prefix + "_" + p + "_" + field
}

// Static serves the same credentials for every device.
type Static Credentials

// Lookup returns the credentials when valid.
func (s Static) Lookup(context.Context, device.Device) (Credentials, error) {
	c := Credentials(s)
	if !c.Valid() {
		return Credentials{}, ErrNotFound
	}
	return c, nil
}

// Chain tries providers in order and returns the first credentials found.
type Chain []Provider

// Lookup returns the first provider's credentials that are not
// ErrNotFound.
func (c Chain) Lookup(ctx context.Context, d device.Device) (Credentials, error) {
	for _, p := range c {
		creds, err := p.Lookup(ctx, d)
		if err == nil {
			return creds, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Credentials{}, fmt.Errorf("credential: %w", err)
		}
	}
	return Credentials{}, ErrNotFound
}
