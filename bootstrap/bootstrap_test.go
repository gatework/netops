package bootstrap_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gatework/netops/bootstrap"
)

func TestLoadEnv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "tool.yml")
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("NC_DEVICE_USERNAME=admin\nNC_DEVICE_PASSWORD=file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.LoadEnv(t.Context(), "", cfg, nil); !errors.Is(err, bootstrap.ErrConfig) {
		t.Errorf("readable .env accepted: %v", err)
	}
	_ = os.Chmod(env, 0o600)
	got, err := bootstrap.LoadEnv(t.Context(), "", cfg, map[string]string{"NC_DEVICE_PASSWORD": "process"})
	if err != nil || got["NC_DEVICE_USERNAME"] != "admin" || got["NC_DEVICE_PASSWORD"] != "process" {
		t.Errorf("env %v %v", got, err)
	}
}

func TestDecodeYAML_strict(t *testing.T) {
	t.Parallel()
	var v struct {
		Workers int `yaml:"workers"`
	}
	if err := bootstrap.DecodeYAML([]byte("wrokers: 3"), &v); !errors.Is(err, bootstrap.ErrConfig) {
		t.Errorf("typo accepted: %v", err)
	}
}

func TestEnv_Discover(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p := filepath.Join(home, ".config", "tool", "tool.yml")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, nil, 0o600)
	e := bootstrap.Env{App: "tool", Home: home, Environ: map[string]string{}}
	if got := e.Discover(); got != p {
		t.Errorf("Discover = %q", got)
	}
	e.Environ["TOOL_CONFIG"] = "~/x.yml"
	if got := e.Discover(); got != filepath.Join(home, "x.yml") {
		t.Errorf("explicit = %q", got)
	}
}

func ExampleResolvePath() {
	fmt.Println(bootstrap.ResolvePath("~/cache", "/etc/tool", "/home/ops"))
	fmt.Println(bootstrap.ResolvePath("cache", "/etc/tool", "/home/ops"))
	// Output:
	// /home/ops/cache
	// /etc/tool/cache
}
