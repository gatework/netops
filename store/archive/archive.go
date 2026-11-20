package archive

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gatework/netops/config"
	"github.com/gatework/netops/evidence"
	"github.com/gatework/netops/store"
)

// Schema is the version of the device index document.
const Schema = 1

// MaxConfig bounds one stored configuration.
const MaxConfig = 64 << 20

const lockWait = 30 * time.Second

// ErrNotFound reports a device or version that is not archived.
var ErrNotFound = errors.New("archive: not found")

type index struct {
	Schema int    `json:"schema"`
	Device string `json:"device"`
	// Checked is when the latest backup was taken, changed or not.
	Checked  time.Time        `json:"checked,omitzero"`
	Versions []config.Version `json:"versions"`
}

// Archive is a configuration repository. It is safe for concurrent use;
// writers of one device serialize on its lock.
type Archive struct {
	st *store.Store
}

// Open opens or creates a private archive root.
func Open(root string) (*Archive, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	return &Archive{st: st}, nil
}

// Put archives one configuration.
func (a *Archive) Put(ctx context.Context, e config.Backup) (config.Archived, error) {
	if e.Device == "" || !e.Normalized.Valid() || e.At.IsZero() {
		return config.Archived{}, fmt.Errorf("archive: invalid entry for %q", e.Device)
	}
	if len(e.Text) > MaxConfig {
		return config.Archived{}, fmt.Errorf("archive: configuration of %s exceeds %d bytes", e.Device, MaxConfig)
	}
	lock, err := a.st.Lock(ctx, "archive/"+e.Device, lockWait)
	if err != nil {
		return config.Archived{}, err
	}
	defer lock.Close()
	idx, err := a.index(ctx, e.Device)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return config.Archived{}, err
	}
	var prev *config.Version
	if n := len(idx.Versions); n > 0 {
		p := idx.Versions[n-1]
		prev = &p
		if e.At.Before(p.At) {
			return config.Archived{}, fmt.Errorf("archive: %s: backup at %s is older than version %d", e.Device, e.At.Format(time.RFC3339), p.Seq)
		}
		if p.Normalized == e.Normalized {
			idx.Checked = e.At.UTC()
			if err := a.writeIndex(ctx, idx); err != nil {
				return config.Archived{}, err
			}
			return config.Archived{Version: p, Changed: false, Previous: prev}, nil
		}
	}
	digest := evidence.DigestString(e.Text)
	if err := a.putObject(ctx, digest, e.Text); err != nil {
		return config.Archived{}, err
	}
	v := config.Version{
		Seq: len(idx.Versions) + 1, At: e.At.UTC(), Digest: digest, Normalized: e.Normalized,
		Bytes: len(e.Text), Source: e.Source, Note: e.Note,
	}
	if prev != nil {
		v.Seq = prev.Seq + 1
	}
	idx.Versions = append(idx.Versions, v)
	idx.Checked = v.At
	if err := a.writeIndex(ctx, idx); err != nil {
		return config.Archived{}, err
	}
	return config.Archived{Version: v, Changed: true, Previous: prev}, nil
}

// Versions returns the versions of a device, oldest first.
func (a *Archive) Versions(ctx context.Context, deviceID string) ([]config.Version, error) {
	idx, err := a.index(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return idx.Versions, nil
}

// Checked returns when the device was last backed up, changed or not.
func (a *Archive) Checked(ctx context.Context, deviceID string) (time.Time, error) {
	idx, err := a.index(ctx, deviceID)
	if err != nil {
		return time.Time{}, err
	}
	return idx.Checked, nil
}

// Latest returns the newest version of a device.
func (a *Archive) Latest(ctx context.Context, deviceID string) (config.Version, error) {
	vs, err := a.Versions(ctx, deviceID)
	if err != nil {
		return config.Version{}, err
	}
	if len(vs) == 0 {
		return config.Version{}, fmt.Errorf("%w: %s has no versions", ErrNotFound, deviceID)
	}
	return vs[len(vs)-1], nil
}

// At returns the version that was current at t: the newest collected at or
// before t.
func (a *Archive) At(ctx context.Context, deviceID string, t time.Time) (config.Version, error) {
	vs, err := a.Versions(ctx, deviceID)
	if err != nil {
		return config.Version{}, err
	}
	i, _ := slices.BinarySearchFunc(vs, t, func(v config.Version, t time.Time) int {
		if v.At.After(t) {
			return 1
		}
		return -1
	})
	if i == 0 {
		return config.Version{}, fmt.Errorf("%w: %s has no version before %s", ErrNotFound, deviceID, t.Format(time.RFC3339))
	}
	return vs[i-1], nil
}

// Seq returns version number seq of a device.
func (a *Archive) Seq(ctx context.Context, deviceID string, seq int) (config.Version, error) {
	vs, err := a.Versions(ctx, deviceID)
	if err != nil {
		return config.Version{}, err
	}
	for _, v := range vs {
		if v.Seq == seq {
			return v, nil
		}
	}
	return config.Version{}, fmt.Errorf("%w: %s version %d", ErrNotFound, deviceID, seq)
}

// Get returns the text of a version, verified against its digest.
func (a *Archive) Get(ctx context.Context, v config.Version) (string, error) {
	body, err := a.st.Read(ctx, objectPath(v.Digest), MaxConfig)
	if errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("%w: object %s", ErrNotFound, v.Digest.Short())
	}
	if err != nil {
		return "", err
	}
	if evidence.DigestOf(body) != v.Digest {
		return "", fmt.Errorf("archive: object %s is corrupt", v.Digest.Short())
	}
	return string(body), nil
}

// Devices returns the archived device IDs, sorted.
func (a *Archive) Devices(ctx context.Context) ([]string, error) {
	names, err := a.st.List("devices")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		var idx index
		if err := a.st.ReadJSON(ctx, "devices/"+n+"/index.json", 0, &idx); err == nil {
			out = append(out, idx.Device)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Retention keeps the newest Keep versions of each device plus the newest
// version of each of the last Daily days; zero fields keep nothing extra.
// The latest version is always kept.
type Retention struct {
	Keep  int
	Daily int
}

// Prune applies the retention to every device and removes objects no
// version references. It returns the number of versions dropped.
func (a *Archive) Prune(ctx context.Context, r Retention, now time.Time) (int, error) {
	ids, err := a.Devices(ctx)
	if err != nil {
		return 0, err
	}
	dropped := 0
	live := map[evidence.Digest]bool{}
	for _, id := range ids {
		n, err := a.pruneDevice(ctx, id, r, now, live)
		if err != nil {
			return dropped, err
		}
		dropped += n
	}
	return dropped, a.sweep(ctx, live)
}

func indexPath(deviceID string) string { return "devices/" + store.Name(deviceID) + "/index.json" }

func objectPath(d evidence.Digest) string { return "objects/" + string(d[:2]) + "/" + string(d) }

// Changes returns, for each device, whether its two newest versions
// differ in content, as a quick fleet change report. Devices with a single
// version are reported unchanged.
func (a *Archive) Changes(ctx context.Context) (map[string]bool, error) {
	ids, err := a.Devices(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		vs, err := a.Versions(ctx, id)
		if err != nil {
			return nil, err
		}
		out[id] = len(vs) > 1 && vs[len(vs)-1].Normalized != vs[len(vs)-2].Normalized
	}
	return out, nil
}

func (a *Archive) putObject(ctx context.Context, digest evidence.Digest, text string) error {
	rel := objectPath(digest)
	if _, err := a.st.Read(ctx, rel, MaxConfig); err == nil {
		return nil
	}
	_, err := a.st.Write(ctx, rel, []byte(text))
	return err
}

func (a *Archive) pruneDevice(ctx context.Context, id string, r Retention, now time.Time, live map[evidence.Digest]bool) (int, error) {
	lock, err := a.st.Lock(ctx, "archive/"+id, lockWait)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	idx, err := a.index(ctx, id)
	if err != nil {
		return 0, err
	}
	keep := map[int]bool{}
	n := len(idx.Versions)
	for i := max(0, n-max(r.Keep, 1)); i < n; i++ {
		keep[i] = true
	}
	days := map[string]bool{}
	for i := n - 1; i >= 0; i-- {
		v := idx.Versions[i]
		day := v.At.Format(time.DateOnly)
		if r.Daily > 0 && now.Sub(v.At) <= time.Duration(r.Daily)*24*time.Hour && !days[day] {
			days[day] = true
			keep[i] = true
		}
	}
	kept := make([]config.Version, 0, len(keep))
	for i, v := range idx.Versions {
		if keep[i] {
			kept = append(kept, v)
			live[v.Digest] = true
		}
	}
	dropped := n - len(kept)
	if dropped > 0 {
		idx.Versions = kept
		if err := a.writeIndex(ctx, idx); err != nil {
			return 0, err
		}
	}
	return dropped, nil
}

func (a *Archive) sweep(ctx context.Context, live map[evidence.Digest]bool) error {
	shards, err := a.st.List("objects")
	if err != nil {
		return err
	}
	for _, shard := range shards {
		objs, err := a.st.List("objects/" + shard)
		if err != nil {
			return err
		}
		for _, o := range objs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !live[evidence.Digest(o)] {
				if err := a.st.Remove("objects/" + shard + "/" + o); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (a *Archive) index(ctx context.Context, deviceID string) (index, error) {
	var idx index
	err := a.st.ReadJSON(ctx, indexPath(deviceID), 0, &idx)
	if errors.Is(err, store.ErrNotFound) {
		return index{Schema: Schema, Device: deviceID, Versions: []config.Version{}}, fmt.Errorf("%w: %s", ErrNotFound, deviceID)
	}
	if err != nil {
		return index{}, err
	}
	if idx.Schema != Schema || idx.Device != deviceID {
		return index{}, fmt.Errorf("archive: index of %s is invalid", deviceID)
	}
	return idx, nil
}

func (a *Archive) writeIndex(ctx context.Context, idx index) error {
	idx.Schema = Schema
	_, err := a.st.WriteJSON(ctx, indexPath(idx.Device), idx)
	return err
}
