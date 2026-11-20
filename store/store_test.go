package store_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gatework/netops/store"
)

func TestStore(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "a//b", `a\b`} {
		if _, err := st.Path(bad); !errors.Is(err, store.ErrUnsafe) {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := st.WriteJSON(t.Context(), "a/b.json", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	var v map[string]int
	if err := st.ReadJSON(t.Context(), "a/b.json", 0, &v); err != nil || v["n"] != 1 {
		t.Errorf("read back %v %v", v, err)
	}
	if _, err := st.Read(t.Context(), "a/missing", 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	info, _ := os.Stat(filepath.Join(st.Root(), "a"))
	if info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %v", info.Mode())
	}
	lock, err := st.Lock(t.Context(), "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Lock(t.Context(), "k", 0); err == nil {
		t.Error("lock not exclusive")
	}
	_ = lock.Close()
}

func TestOpen_rejectsSharedRoot(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(dir); !errors.Is(err, store.ErrUnsafe) {
		t.Errorf("world-writable root: %v", err)
	}
}

func ExampleName() {
	fmt.Println(store.Name("display wlan client verbose"))
	fmt.Println(store.Name("display wlan client verbose") != store.Name("display_wlan_client_verbose"))
	// Output:
	// display_wlan_client_verbose-16420cb7e146
	// true
}

func TestNewBatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	at := time.Date(2026, 11, 19, 1, 2, 3, 0, time.UTC)
	a, err := store.NewBatch(root, at)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.NewBatch(root, at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(a.Root()) != "2026-11-19_09-02-03" || filepath.Base(b.Root()) != "2026-11-19_09-02-03_01" {
		t.Errorf("batches %s %s", a.Root(), b.Root())
	}
}
