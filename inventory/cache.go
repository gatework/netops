package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gatework/netops/store/disk"
)

// CacheMode selects how the Netdisco source uses its local cache.
type CacheMode string

// Cache modes.
const (
	// CachePrefer uses a fresh entry and otherwise fetches; a failed fetch
	// falls back to a stale entry.
	CachePrefer CacheMode = "prefer"
	// CacheRefresh always fetches; a failed fetch falls back to a stale
	// entry.
	CacheRefresh CacheMode = "refresh"
	// CacheOnly reads the cache without contacting the source.
	CacheOnly CacheMode = "only"
)

// CacheStatus tells where the rows came from.
type CacheStatus string

// Cache statuses.
const (
	CacheDisabled  CacheStatus = "disabled"
	CacheRefreshed CacheStatus = "refreshed"
	CacheFresh     CacheStatus = "fresh_cache"
	CacheStale     CacheStatus = "stale_cache"
)

// ErrCacheEmpty reports a cache-only read without a valid entry.
var ErrCacheEmpty = errors.New("inventory: no cached inventory; refresh the source first")

type cacheResult struct {
	Rows      []Row
	Status    CacheStatus
	FetchedAt time.Time
	// RefreshErr is the fetch failure behind a stale result.
	RefreshErr error
	// WriteErr is a failure to persist refreshed rows; the rows are valid.
	WriteErr error
}

type cacheEntry struct {
	Schema    int       `json:"schema"`
	Identity  string    `json:"identity"`
	FetchedAt time.Time `json:"fetched_at"`
	Rows      []Row     `json:"rows"`
}

const cacheSchema = 2

// cache is a private, identity-keyed file cache in front of a fetcher. A
// zero dir disables it.
type cache struct {
	source fetcher
	dir    string
	ttl    time.Duration
	now    func() time.Time
}

func (c *cache) fetch(ctx context.Context, mode CacheMode) (cacheResult, error) {
	if c.dir == "" {
		if mode == CacheOnly {
			return cacheResult{}, fmt.Errorf("%w: cache_mode only needs cache_directory", ErrInvalid)
		}
		rows, err := c.source.Rows(ctx)
		return cacheResult{Rows: rows, Status: CacheDisabled}, err
	}
	if mode != CacheOnly {
		if err := disk.PrepareDir(c.dir); err != nil {
			return cacheResult{}, fmt.Errorf("inventory: cache: %w", err)
		}
	}
	path := filepath.Join(c.dir, c.source.Identity()+".json")
	prev := c.read(ctx, path)
	if mode == CacheOnly {
		if prev == nil {
			return cacheResult{}, ErrCacheEmpty
		}
		return c.deliver(prev), nil
	}
	if mode == CachePrefer && prev != nil && c.fresh(prev) {
		return c.deliver(prev), nil
	}
	lock, err := disk.Lock(ctx, path+".lock", 0)
	if err != nil {
		return cacheResult{}, fmt.Errorf("inventory: cache: %w", err)
	}
	defer func() { _ = lock.Close() }()
	// Another process may have refreshed while we waited.
	if prev = c.read(ctx, path); mode == CachePrefer && prev != nil && c.fresh(prev) {
		return c.deliver(prev), nil
	}
	rows, err := c.source.Rows(ctx)
	if ctx.Err() != nil {
		return cacheResult{}, ctx.Err()
	}
	if err != nil {
		if prev == nil {
			return cacheResult{}, err
		}
		r := c.deliver(prev)
		r.Status, r.RefreshErr = CacheStale, err
		return r, nil
	}
	e := cacheEntry{Schema: cacheSchema, Identity: c.source.Identity(), FetchedAt: c.now().UTC(), Rows: rows}
	r := cacheResult{Rows: rows, Status: CacheRefreshed, FetchedAt: e.FetchedAt}
	body, err := json.Marshal(e)
	if err == nil {
		_, err = disk.WriteFile(ctx, path, body)
	}
	r.WriteErr = err
	return r, nil
}

func (c *cache) fresh(e *cacheEntry) bool { return c.now().Sub(e.FetchedAt) < c.ttl }

func (c *cache) deliver(e *cacheEntry) cacheResult {
	st := CacheStale
	if c.fresh(e) {
		st = CacheFresh
	}
	return cacheResult{Rows: e.Rows, Status: st, FetchedAt: e.FetchedAt}
}

// read returns the valid entry at path, or nil. Anything unreadable, of
// another identity or schema, or from the future is a miss.
func (c *cache) read(ctx context.Context, path string) *cacheEntry {
	body, _, err := disk.ReadFile(ctx, path, c.source.Limits().MaxBytes*2)
	if err != nil {
		return nil // missing or unreadable: a miss either way
	}
	var e cacheEntry
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || e.Schema != cacheSchema || e.Identity != c.source.Identity() ||
		e.Rows == nil || e.FetchedAt.After(c.now()) || len(e.Rows) > c.source.Limits().MaxDevices {
		return nil
	}
	return &e
}
