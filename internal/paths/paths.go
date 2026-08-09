// Package paths resolves platform-appropriate locations for provider state
// (token files, model caches).
package paths

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

// AppName is the directory name used under both config and cache roots,
// matching the llm-proxy host process so token files land in the same
// cache directory.
const AppName = "llm-proxy"

// CacheDir returns the directory used for provider state such as token
// files. The directory is created (mode 0700) if missing.
//
// On macOS the default is ~/.cache/llm-proxy rather than
// ~/Library/Caches/llm-proxy (which os.UserCacheDir returns). Using the
// XDG-style ~/.cache path keeps the location consistent with Linux and
// matches the llm-proxy host process expectation on macOS.
func CacheDir() (string, error) {
	if dir, ok := os.LookupEnv("LLM_PROXY_CACHE_DIR"); ok {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create cache dir %q: %w", dir, err)
		}
		return dir, nil
	}
	if runtime.GOOS == "darwin" {
		return darwinCacheDir()
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	dir := filepath.Join(base, AppName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir %q: %w", dir, err)
	}
	return dir, nil
}

// darwinCacheDir returns ~/.cache/llm-proxy, creating it if absent.
// os.UserCacheDir on macOS returns ~/Library/Caches; we override to the
// XDG-style ~/.cache path so darwin and Linux resolve to the same location.
func darwinCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".cache", AppName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir %q: %w", dir, err)
	}
	return dir, nil
}

// ConfigDir returns the persistent configuration directory for the proxy.
func ConfigDir() (string, error) {
	if dir, ok := os.LookupEnv("LLM_PROXY_CONFIG_DIR"); ok {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create config dir %q: %w", dir, err)
		}
		return dir, nil
	}
	return configDirForGOOS(runtime.GOOS)
}

func configDirForGOOS(goos string) (string, error) {
	if u, _ := user.Current(); goos != "windows" && u != nil && u.Uid == "0" {
		return "/etc/" + AppName, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	dir := filepath.Join(base, AppName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir %q: %w", dir, err)
	}
	return dir, nil
}

// WriteSecret atomically writes data to path with mode 0600.
// It writes to a sibling temp file first, then renames into place.
func WriteSecret(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir %q: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	cleanup = false
	return nil
}
