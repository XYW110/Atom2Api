package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreMigratesLegacyFilesIntoSQLite(t *testing.T) {
	directory := t.TempDir()
	config, err := NewConfigManager(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	encryptor := &Store{config: config}
	accessToken, err := encryptor.encrypt("legacy-access-token")
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	refreshToken, err := encryptor.encrypt("legacy-refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	legacyState := persistedState{
		Version: stateVersion,
		Accounts: []Account{{
			ID: "acc_legacy", Name: "Legacy Account", Status: "active", Enabled: true,
			User: UserInfo{ID: "user-legacy", Username: "legacy"},
			Credentials: OAuthCredentials{
				AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer", CreatedAt: now,
			},
			CreatedAt: now, UpdatedAt: now,
		}},
		APIKeys: []APIKey{{
			ID: "key_legacy", Name: "Legacy Key", Prefix: "sk-atom2-legacy...", Hash: "digest",
			Enabled: true, CreatedAt: now,
		}},
		ModelSettings: map[string]ModelSetting{
			"legacy-model": {Upstream: "legacy-model", Alias: "legacy-alias", Enabled: true},
		},
		PlanClaimLogs: []PlanClaimLog{{
			ID: "claim_legacy", AccountID: "acc_legacy", AccountName: "Legacy Account",
			Status: "success", StartedAt: now,
		}},
	}
	legacyPath := filepath.Join(directory, "state.json")
	stateData, err := json.MarshalIndent(legacyState, "", "  ")
	if err != nil {
		t.Fatalf("encode legacy state: %v", err)
	}
	if err := os.WriteFile(legacyPath, append(stateData, '\n'), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
	legacyUsage := UsageRecord{
		ID: "req_legacy", Timestamp: now, AccountID: "acc_legacy", Status: 200,
		InputTokens: 12, OutputTokens: 3,
	}
	usageData, err := json.Marshal(legacyUsage)
	if err != nil {
		t.Fatalf("encode legacy usage: %v", err)
	}
	usagePath := legacyPath + ".usage.ndjson"
	if err := os.WriteFile(usagePath, append(usageData, '\n'), 0o600); err != nil {
		t.Fatalf("write legacy usage: %v", err)
	}

	store, err := NewStore(legacyPath, config)
	if err != nil {
		t.Fatalf("NewStore migration: %v", err)
	}
	if store.path != filepath.Join(directory, "state.db") {
		t.Fatalf("SQLite path = %q", store.path)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy state still exists: %v", err)
	}
	if _, err := os.Stat(usagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy usage still exists: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}

	reloaded, err := NewStore(legacyPath, config)
	if err != nil {
		t.Fatalf("reload migrated store: %v", err)
	}
	t.Cleanup(func() {
		if err := reloaded.Close(); err != nil {
			t.Errorf("close reloaded store: %v", err)
		}
	})
	account, access, refresh, err := reloaded.Account("acc_legacy")
	if err != nil || account.Name != "Legacy Account" || access != "legacy-access-token" || refresh != "legacy-refresh-token" {
		t.Fatalf("migrated account = (%#v, %q, %q, %v)", account, access, refresh, err)
	}
	if keys := reloaded.APIKeys(); len(keys) != 1 || keys[0].ID != "key_legacy" {
		t.Fatalf("migrated API keys = %#v", keys)
	}
	if setting := reloaded.ModelSettings()["legacy-model"]; setting.Alias != "legacy-alias" {
		t.Fatalf("migrated model setting = %#v", setting)
	}
	if logs := reloaded.PlanClaimLogs("acc_legacy", 10); len(logs) != 1 || logs[0].ID != "claim_legacy" {
		t.Fatalf("migrated plan claim logs = %#v", logs)
	}
	if usage := reloaded.UsageRecords(); len(usage) != 1 || usage[0].ID != "req_legacy" || usage[0].InputTokens != 12 {
		t.Fatalf("migrated usage = %#v", usage)
	}
}

func TestStoreKeepsLegacyFilesWhenMigrationFails(t *testing.T) {
	directory := t.TempDir()
	config, err := NewConfigManager(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	legacyPath := filepath.Join(directory, "state.json")
	if err := os.WriteFile(legacyPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write invalid legacy state: %v", err)
	}
	if _, err := NewStore(legacyPath, config); err == nil {
		t.Fatal("NewStore accepted an invalid legacy state")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy state was removed after failed migration: %v", err)
	}
}
