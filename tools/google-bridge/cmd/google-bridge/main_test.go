package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeKeepsKeysOutsideDataAndDoesNotOverwrite(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	path := filepath.Join(base, "google-bridge", "config.json")
	if err := initialize(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AdminToken) != 64 || len(cfg.Keys[cfg.KeyVersion]) != 64 || cfg.AdminToken == cfg.Keys[cfg.KeyVersion] {
		t.Fatal("missing independent random keys")
	}
	if cfg.Listen != "127.0.0.1:8094" || cfg.PublicURL != "http://127.0.0.1:8094" {
		t.Fatal("unsafe defaults")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe file permissions")
	}
	if err := initialize(path); err == nil {
		t.Fatal("existing encryption keys overwritten")
	}
	after, err := load(path)
	if err != nil || after.AdminToken != cfg.AdminToken {
		t.Fatal("config changed")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := load(path); err == nil {
		t.Fatal("world-readable keys accepted")
	}
}
func TestConfigurationRejectsSharedKeyAndDataStorageAndPublicHTTP(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	path := filepath.Join(base, "config.json")
	if err := initialize(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	write := func() {
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg.DataDir = base
	write()
	if _, err := load(path); err == nil {
		t.Fatal("keys inside database directory")
	}
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Listen = "0.0.0.0:8094"
	write()
	if _, err := load(path); err == nil {
		t.Fatal("public HTTP listener")
	}
	cfg.PublicURL = "https://bridge.example"
	write()
	if _, err := load(path); err != nil {
		t.Fatal(err)
	}
}
func TestDataDirectoryHasOneProcessOwner(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Unix lock")
	}
	dir := t.TempDir()
	release, err := lockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if another, err := lockDataDir(dir); err == nil {
		another()
		t.Fatal("concurrent instance accepted")
	}
	release()
	next, err := lockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	next()
}
