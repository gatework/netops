package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/gatework/netops/secret"
)

// ErrSource reports a failed inventory fetch. Its message carries a stable
// code only; the cause is wrapped for errors.Is but never formatted with
// credentials.
var ErrSource = errors.New("inventory: source failed")

func sourceError(code string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrSource, code)
	}
	return fmt.Errorf("%w: %s: %w", ErrSource, code, cause)
}

// Limits bound one fetch.
type Limits struct {
	PageSize, MaxPages, MaxDevices int
	MaxBytes                       int64 // whole inventory
	Timeout                        time.Duration
}

func (l Limits) normalize() Limits {
	if l.PageSize <= 0 {
		l.PageSize = 500
	}
	if l.MaxPages <= 0 {
		l.MaxPages = 10000
	}
	if l.MaxDevices <= 0 {
		l.MaxDevices = 100000
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = 128 << 20
	}
	if l.Timeout <= 0 {
		l.Timeout = 5 * time.Minute
	}
	return l
}

// fetcher is one inventory backend. Identity is an opaque digest of
// everything that determines its rows except secrets, used as cache key.
type fetcher interface {
	Rows(ctx context.Context) ([]Row, error)
	Identity() string
	Limits() Limits
}

func identity(kind string, descriptor any) string {
	body, _ := json.Marshal(struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		Details any    `json:"details"`
	}{3, kind, descriptor})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// HTTPDoer is the part of *http.Client the HTTP source uses.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type httpOptions struct {
	URL, Username, Namespace string
	Password, APIKey         secret.Secret
	Client                   HTTPDoer
	Limits                   Limits
	NeedSecret               bool
}

// httpSource reads /api/v1/search/device page by page.
type httpSource struct {
	o    httpOptions
	base *url.URL
	id   string
}

const (
	httpEndpoint = "api/v1/search/device"
	httpFields   = "ip,name,dns,vendor,os,model,os_ver,serial"
)

func newHTTP(o httpOptions) (*httpSource, error) {
	u, err := url.Parse(o.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: netdisco.url", ErrInvalid)
	}
	key := o.APIKey.Reveal()
	if strings.ContainsAny(key, "\r\n\x00") || strings.ContainsAny(o.Username, "\r\n\x00:") {
		return nil, fmt.Errorf("%w: netdisco credentials", ErrInvalid)
	}
	if o.NeedSecret && key == "" && (o.Username == "" || o.Password.Reveal() == "") {
		return nil, fmt.Errorf("%w: netdisco credentials are missing", ErrInvalid)
	}
	if o.Client == nil {
		o.Client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	principal := o.Username
	if key != "" && o.Namespace != "" {
		principal = "ns:" + o.Namespace
	} else if key != "" && principal == "" {
		sum := sha256.Sum256([]byte("netdisco-api-principal\x00" + key))
		principal = "key:" + hex.EncodeToString(sum[:8])
	}
	id := identity("http", struct{ URL, Principal, Fields string }{strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.Path, principal, httpFields})
	return &httpSource{o: o, base: u, id: id}, nil
}

func (h *httpSource) Identity() string { return h.id }
func (h *httpSource) Limits() Limits   { return h.o.Limits }

func (h *httpSource) Rows(ctx context.Context) ([]Row, error) {
	ctx, cancel := context.WithTimeout(ctx, h.o.Limits.Timeout)
	defer cancel()
	key, err := h.login(ctx)
	if err != nil {
		return nil, err
	}
	var (
		rows  []Row
		total int64
		seen  = map[Row]bool{}
		ips   = map[string]bool{}
	)
	for page := range h.o.Limits.MaxPages {
		q := url.Values{"fields": {httpFields}, "limit": {strconv.Itoa(h.o.Limits.PageSize)}, "offset": {strconv.Itoa(page * h.o.Limits.PageSize)}}
		body, status, err := h.request(ctx, http.MethodGet, httpEndpoint, q, key, &total)
		if err != nil {
			return nil, err
		}
		if status == http.StatusBadRequest && strings.Contains(string(body), "Missing query") {
			// Older Netdisco needs a query and returns everything at once.
			body, status, err = h.request(ctx, http.MethodGet, httpEndpoint, url.Values{"q": {"%"}, "seeallcolumns": {"true"}}, key, &total)
			if err != nil {
				return nil, err
			}
			if status/100 != 2 {
				return nil, sourceError("http_"+strconv.Itoa(status), nil)
			}
			return decodeRows(body, h.o.Limits.MaxDevices, nil, map[Row]bool{})
		}
		if status/100 != 2 {
			return nil, sourceError("http_"+strconv.Itoa(status), nil)
		}
		before := len(ips)
		got := 0
		if rows, err = decodeRowsInto(body, h.o.Limits.MaxDevices, rows, seen, ips, &got); err != nil {
			return nil, err
		}
		if got == 0 {
			return rows, nil
		}
		if len(ips) == before {
			return nil, sourceError("pagination_stalled", nil)
		}
	}
	return nil, sourceError("max_pages", nil)
}

func (h *httpSource) login(ctx context.Context) (string, error) {
	if k := h.o.APIKey.Reveal(); k != "" {
		return k, nil
	}
	var total int64
	body, status, err := h.request(ctx, http.MethodPost, "login", nil, "", &total)
	if err != nil {
		return "", err
	}
	var data struct {
		APIKey string `json:"api_key"`
	}
	if status/100 != 2 || json.Unmarshal(body, &data) != nil || data.APIKey == "" || strings.ContainsAny(data.APIKey, "\r\n\x00") {
		return "", sourceError("authentication_failed", nil)
	}
	return data.APIKey, nil
}

func (h *httpSource) request(ctx context.Context, method, endpoint string, q url.Values, key string, total *int64) ([]byte, int, error) {
	u := *h.base
	u.Path += "/" + endpoint
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, 0, sourceError("request", err)
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Apikey "+key)
	} else {
		req.SetBasicAuth(h.o.Username, h.o.Password.Reveal())
	}
	resp, err := h.o.Client.Do(req)
	if err != nil {
		return nil, 0, sourceError("connection_failed", err)
	}
	defer func() { _ = resp.Body.Close() }()
	left := h.o.Limits.MaxBytes - *total
	body, err := io.ReadAll(io.LimitReader(resp.Body, left+1))
	if err != nil {
		return nil, 0, sourceError("connection_failed", err)
	}
	*total += int64(len(body))
	if *total > h.o.Limits.MaxBytes {
		return nil, 0, sourceError("max_bytes", nil)
	}
	return body, resp.StatusCode, nil
}

func decodeRows(body []byte, maxDevices int, rows []Row, seen map[Row]bool) ([]Row, error) {
	n := 0
	return decodeRowsInto(body, maxDevices, rows, seen, map[string]bool{}, &n)
}

// decodeRowsInto appends the distinct rows of one JSON page. A null field
// decodes as empty; a row without ip is invalid.
func decodeRowsInto(body []byte, maxDevices int, rows []Row, seen map[Row]bool, ips map[string]bool, got *int) ([]Row, error) {
	if strings.TrimSpace(string(body)) == "" {
		return rows, nil
	}
	if !utf8.Valid(body) {
		return nil, sourceError("invalid_json", nil)
	}
	var page []*Row
	if err := json.Unmarshal(body, &page); err != nil || page == nil {
		return nil, sourceError("invalid_inventory", err)
	}
	*got = len(page)
	for _, r := range page {
		if r == nil || strings.TrimSpace(r.IP) == "" {
			return nil, sourceError("invalid_inventory", nil)
		}
		ips[r.IP] = true
		if !seen[*r] {
			seen[*r] = true
			rows = append(rows, *r)
		}
		if len(rows) > maxDevices {
			return nil, sourceError("max_devices", nil)
		}
	}
	return rows, nil
}

// pgSource runs an operator query through a read-only cursor.
type pgSource struct {
	cfg    *pgx.ConnConfig
	query  string
	params []any
	limits Limits
	id     string
}

func newPostgres(dsn, query string, params []any, limits Limits, namespace string) (*pgSource, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: netdisco database settings", ErrInvalid)
	}
	id := identity("postgres", struct {
		Namespace, Host, User, Database, Query string
		Port                                   uint16
		Params                                 []any
	}{namespace, strings.ToLower(cfg.Host), cfg.User, cfg.Database, query, cfg.Port, params})
	return &pgSource{cfg: cfg, query: query, params: params, limits: limits, id: id}, nil
}

func (p *pgSource) Identity() string { return p.id }
func (p *pgSource) Limits() Limits   { return p.limits }

func (p *pgSource) Rows(ctx context.Context) (rows []Row, err error) {
	ctx, cancel := context.WithTimeout(ctx, p.limits.Timeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, p.cfg.Copy())
	if err != nil {
		return nil, sourceError("connection_failed", err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(c)
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, sourceError("query_failed", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// The extended protocol refuses multiple statements in the query.
	args := append([]any{pgx.QueryExecModeExec}, p.params...)
	if _, err := tx.Exec(ctx, "DECLARE netops_inventory NO SCROLL CURSOR FOR\n"+p.query, args...); err != nil {
		return nil, sourceError("query_failed", err)
	}
	var total int64
	fetch := fmt.Sprintf("FETCH FORWARD %d FROM netops_inventory", p.limits.PageSize)
	for range p.limits.MaxPages {
		page, err := tx.Query(ctx, fetch, pgx.QueryExecModeExec)
		if err != nil {
			return nil, sourceError("query_failed", err)
		}
		got, err := pgx.CollectRows(page, func(r pgx.CollectableRow) (Row, error) {
			row, err := pgx.RowToStructByNameLax[pgRow](r)
			if err != nil {
				return Row{}, err
			}
			out := row.row()
			total += int64(out.size())
			if strings.TrimSpace(out.IP) == "" {
				return Row{}, sourceError("invalid_inventory", nil)
			}
			return out, nil
		})
		if err != nil {
			return nil, sourceError("query_failed", err)
		}
		rows = append(rows, got...)
		switch {
		case len(rows) > p.limits.MaxDevices:
			return nil, sourceError("max_devices", nil)
		case total > p.limits.MaxBytes:
			return nil, sourceError("max_bytes", nil)
		case len(got) < p.limits.PageSize:
			if err := tx.Commit(ctx); err != nil {
				return nil, sourceError("query_failed", err)
			}
			return rows, nil
		}
	}
	return nil, sourceError("max_pages", nil)
}

// pgRow maps the query columns; missing columns stay nil and NULL decodes
// as empty. Column ip is required.
type pgRow struct {
	IP        *string `db:"ip"`
	Name      *string `db:"name"`
	DNS       *string `db:"dns"`
	Vendor    *string `db:"vendor"`
	OS        *string `db:"os"`
	Model     *string `db:"model"`
	OSVersion *string `db:"os_ver"`
	Serial    *string `db:"serial"`
}

func (r pgRow) row() Row {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.ToValidUTF8(*p, "�")
	}
	return Row{IP: s(r.IP), Name: s(r.Name), DNS: s(r.DNS), Vendor: s(r.Vendor), OS: s(r.OS), Model: s(r.Model), OSVersion: s(r.OSVersion), Serial: s(r.Serial)}
}
