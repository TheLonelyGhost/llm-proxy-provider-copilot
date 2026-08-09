package paths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/paths"
)

func TestCacheDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LLM_PROXY_CACHE_DIR", dir)
	got, err := paths.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("CacheDir() = %q, want %q", got, dir)
	}
}

func TestCacheDir_Default(t *testing.T) {
	os.Unsetenv("LLM_PROXY_CACHE_DIR") //nolint:errcheck
	got, err := paths.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "llm-proxy") {
		t.Errorf("unexpected cache dir %q", got)
	}
}

func TestCacheDir_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	os.Unsetenv("LLM_PROXY_CACHE_DIR") //nolint:errcheck

	got, err := paths.CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, ".cache", "llm-proxy")
	if got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
	if strings.Contains(got, "Library/Caches") {
		t.Errorf("CacheDir() routed through ~/Library/Caches on darwin: %q", got)
	}
}

func TestConfigDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LLM_PROXY_CONFIG_DIR", dir)
	got, err := paths.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("ConfigDir() = %q, want %q", got, dir)
	}
}

func TestConfigDir_Default(t *testing.T) {
	os.Unsetenv("LLM_PROXY_CONFIG_DIR") //nolint:errcheck
	got, err := paths.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("ConfigDir returned empty string")
	}
}

func TestWriteSecret_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "secret.json")
	data := []byte(`{"key":"value"}`)

	if err := paths.WriteSecret(path, data); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("WriteSecret round-trip: got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteSecret_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")

	if err := paths.WriteSecret(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSecret(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second" {
		t.Errorf("overwrite: got %q, want %q", got, "second")
	}
}

func TestWriteSecret_MkdirFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can always mkdir")
	}
	// Create a file where we want a dir to be.
	f := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := paths.WriteSecret(filepath.Join(f, "sub", "secret.json"), []byte("data"))
	if err == nil {
		t.Fatal("expected error when parent is a file")
	}
}
