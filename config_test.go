package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigManagerCreatesDefaultConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	manager, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	if got := manager.Snapshot().UserAgent; got != defaultUserAgent {
		t.Fatalf("default UserAgent = %q, want %q", got, defaultUserAgent)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("generated config is invalid JSON: %v", err)
	}
	if config.UserAgent != defaultUserAgent {
		t.Fatalf("generated UserAgent = %q, want %q", config.UserAgent, defaultUserAgent)
	}
}

func TestConfigManagerReloadsValidChanges(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	manager, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	if err := os.WriteFile(configPath, []byte("{\n  \"user_agent\": \"custom-client/1.2.3\"\n}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	changed, err := manager.Reload()
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if !changed {
		t.Fatal("Reload() changed = false, want true")
	}
	if got := manager.Snapshot().UserAgent; got != "custom-client/1.2.3" {
		t.Fatalf("reloaded UserAgent = %q", got)
	}
}

func TestConfigManagerWatchesFileChanges(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	manager, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	manager.Start(10 * time.Millisecond)
	t.Cleanup(manager.Close)

	if err := os.WriteFile(configPath, []byte("{\n  \"user_agent\": \"watched-client/4.0\"\n}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if manager.Snapshot().UserAgent == "watched-client/4.0" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watched UserAgent = %q, want %q", manager.Snapshot().UserAgent, "watched-client/4.0")
}

func TestConfigManagerKeepsLastValidConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	manager, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	if err := os.WriteFile(configPath, []byte("{\"user_agent\": \"\"}"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	if changed, err := manager.Reload(); err == nil || changed {
		t.Fatalf("Reload() = (%v, %v), want (false, error)", changed, err)
	}
	if got := manager.Snapshot().UserAgent; got != defaultUserAgent {
		t.Fatalf("UserAgent after invalid reload = %q, want %q", got, defaultUserAgent)
	}
}

func TestConfigManagerUpdatePersistsImmediately(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	manager, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	if err := manager.Update(Config{UserAgent: "settings-page/2.0"}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got := manager.Snapshot().UserAgent; got != "settings-page/2.0" {
		t.Fatalf("updated UserAgent = %q", got)
	}

	reloaded, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("reload persisted config: %v", err)
	}
	if got := reloaded.Snapshot().UserAgent; got != "settings-page/2.0" {
		t.Fatalf("persisted UserAgent = %q", got)
	}
}
