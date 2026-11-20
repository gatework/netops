package inventory

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/secret"
)

// NetdiscoSource is "http" or "postgres".
type NetdiscoSource string

// Netdisco sources.
const (
	NetdiscoHTTP     NetdiscoSource = "http"
	NetdiscoPostgres NetdiscoSource = "postgres"
)

// DeviceRule maps an inventory vendor (and optionally OS and model prefix)
// to a platform.
type DeviceRule struct {
	Vendor      string  `yaml:"vendor"`
	Platform    string  `yaml:"platform"`
	OS          *string `yaml:"os"`
	ModelPrefix *string `yaml:"model_prefix"`
}

// NetdiscoConfig configures the Netdisco source.
type NetdiscoConfig struct {
	Source            NetdiscoSource `yaml:"source"`
	URL               string         `yaml:"url"`
	AllowInsecureHTTP bool           `yaml:"allow_insecure_http"`
	Query             string         `yaml:"query"`
	QueryParams       []string       `yaml:"query_params"`
	CacheMode         string         `yaml:"cache_mode"` // prefer (default), refresh, only
	CacheTTL          time.Duration  `yaml:"cache_ttl"`  // default 72h
	CacheDirectory    string         `yaml:"cache_directory"`
	CacheNamespace    string         `yaml:"cache_namespace"`
	MaxDevices        int            `yaml:"max_devices"`
	Timeout           time.Duration  `yaml:"timeout"`
	DBPort            int            `yaml:"db_port"`
	DBSSLMode         string         `yaml:"db_sslmode"`
	DBSSLRootCert     string         `yaml:"db_sslrootcert"`
	DBConnectTimeout  int            `yaml:"db_connect_timeout"`

	IncludeHosts      []string          `yaml:"include_hosts"`
	ExcludeHosts      []string          `yaml:"exclude_hosts"`
	IncludeNames      []string          `yaml:"include_names"`
	NameMatch         MatchMode         `yaml:"name_match"` // contains (default), prefix, glob
	ExcludeNames      []string          `yaml:"exclude_names"`
	IncludePlatforms  []string          `yaml:"include_platforms"`
	ExcludePlatforms  []string          `yaml:"exclude_platforms"`
	PlatformOverrides map[string]string `yaml:"platform_overrides"`
	HostOverrides     map[string]string `yaml:"host_overrides"`
	DeviceRules       []DeviceRule      `yaml:"device_rules"`
	// Platforms keeps only devices of these platforms; empty keeps all.
	Platforms []string `yaml:"platforms"`
	// Site and Role are applied to every Netdisco device.
	Site string `yaml:"site"`
	Role string `yaml:"role"`
}

// Validate checks the configuration.
func (c NetdiscoConfig) Validate() error {
	switch c.Source {
	case "", NetdiscoHTTP:
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || (u.Scheme != "https" && !(u.Scheme == "http" && c.AllowInsecureHTTP)) {
			return fmt.Errorf("%w: netdisco.url", ErrInvalid)
		}
	case NetdiscoPostgres:
		if strings.TrimSpace(c.Query) == "" {
			return fmt.Errorf("%w: netdisco.query", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: netdisco.source %q", ErrInvalid, c.Source)
	}
	if !slices.Contains([]string{"", "prefer", "refresh", "only"}, c.CacheMode) {
		return fmt.Errorf("%w: netdisco.cache_mode %q", ErrInvalid, c.CacheMode)
	}
	if c.CacheTTL < 0 || c.Timeout < 0 || c.MaxDevices < 0 || c.DBPort < 0 || c.DBPort > 65535 || c.DBConnectTimeout < 0 {
		return fmt.Errorf("%w: netdisco limits", ErrInvalid)
	}
	if !slices.Contains([]string{"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"}, c.DBSSLMode) {
		return fmt.Errorf("%w: netdisco.db_sslmode", ErrInvalid)
	}
	for _, r := range c.DeviceRules {
		if r.Vendor == "" || r.Platform == "" {
			return fmt.Errorf("%w: netdisco.device_rules", ErrInvalid)
		}
	}
	return nil
}

// NetdiscoCredentials are the Netdisco API or database credentials.
type NetdiscoCredentials struct {
	Username   string
	Password   secret.Secret
	APIKey     secret.Secret
	DBHost     string
	DBName     string
	DBUser     string
	DBPassword secret.Secret
}

// Netdisco is the Netdisco source. Classifier supplies the platform
// catalog, normally net-connector's driver registry.
type Netdisco struct {
	Config      NetdiscoConfig
	Credentials NetdiscoCredentials
	Classifier  Classifier
	// HTTPClient is borrowed for the HTTP source; nil uses a private one.
	HTTPClient HTTPDoer
	// Now is the clock of the cache; nil uses time.Now.
	Now func() time.Time
}

// Name returns "netdisco".
func (Netdisco) Name() string { return "netdisco" }

// Devices fetches (or reads from the cache), selects and classifies the
// Netdisco rows.
func (n Netdisco) Devices(ctx context.Context) ([]device.Device, Report, error) {
	c := n.Config
	if err := c.Validate(); err != nil {
		return nil, Report{}, err
	}
	r, err := newRules(c, n.Classifier)
	if err != nil {
		return nil, Report{}, err
	}
	fetcher, err := n.fetcher()
	if err != nil {
		return nil, Report{}, err
	}
	res, err := c.cache(fetcher, n.Now).fetch(ctx, CacheMode(cmpOr(c.CacheMode, string(CachePrefer))))
	if err != nil {
		return nil, Report{}, fmt.Errorf("inventory: netdisco: %w", err)
	}
	rep := Report{Rows: len(res.Rows), Stale: res.Status == CacheStale}
	out := make([]device.Device, 0, len(res.Rows))
	for _, row := range res.Rows {
		d, drop := r.device(row)
		switch {
		case drop != "":
			rep.drop(drop)
			continue
		case len(c.Platforms) > 0 && !slices.Contains(c.Platforms, d.Platform):
			rep.drop(DropPlatformNotKept)
			continue
		}
		d.Site, d.Role = c.Site, c.Role
		out = append(out, d)
	}
	return out, rep, nil
}

func (n Netdisco) fetcher() (fetcher, error) {
	c, cr := n.Config, n.Credentials
	limits := Limits{MaxDevices: c.MaxDevices, Timeout: c.Timeout}.normalize()
	if c.Source == NetdiscoPostgres {
		params := make([]any, len(c.QueryParams))
		for i, p := range c.QueryParams {
			params[i] = p
		}
		return newPostgres(c.dsn(cr), c.Query, params, limits, c.CacheNamespace)
	}
	return newHTTP(httpOptions{
		URL: c.URL, Username: cr.Username, Password: cr.Password, APIKey: cr.APIKey,
		Namespace: c.CacheNamespace, Client: n.HTTPClient, Limits: limits,
		NeedSecret: c.CacheMode != string(CacheOnly),
	})
}

func (c NetdiscoConfig) cache(f fetcher, now func() time.Time) *cache {
	if c.CacheDirectory == "" {
		return &cache{source: f}
	}
	ttl := c.CacheTTL
	if ttl == 0 {
		ttl = 72 * time.Hour
	}
	if now == nil {
		now = time.Now
	}
	return &cache{source: f, dir: c.CacheDirectory, ttl: ttl, now: now}
}

// dsn builds a libpq connection string. In cache-only mode the password is
// left out.
func (c NetdiscoConfig) dsn(cr NetdiscoCredentials) string {
	var parts []string
	add := func(k, v string) {
		parts = append(parts, k+"='"+strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), "'", `\'`)+"'")
	}
	add("host", cr.DBHost)
	add("dbname", cr.DBName)
	add("user", cr.DBUser)
	if c.DBPort > 0 {
		add("port", strconv.Itoa(c.DBPort))
	}
	if c.DBSSLMode != "" {
		add("sslmode", c.DBSSLMode)
	}
	if c.DBSSLRootCert != "" {
		add("sslrootcert", c.DBSSLRootCert)
	}
	if c.DBConnectTimeout > 0 {
		add("connect_timeout", strconv.Itoa(c.DBConnectTimeout))
	}
	if c.CacheMode != "only" {
		add("password", cr.DBPassword.Reveal())
	}
	return strings.Join(parts, " ")
}
