package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestManualConfiguredAccountProtocols(t *testing.T) {
	if os.Getenv("ATOM2API_LIVE_PROBE") != "1" {
		t.Skip("set ATOM2API_LIVE_PROBE=1 to probe models on configured accounts")
	}
	configPath := strings.TrimSpace(os.Getenv("ATOM2API_CONFIG_PATH"))
	if configPath == "" {
		configPath = "config.json"
	}
	config, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	store, err := NewStore(config.Snapshot().DataPath, config)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	accounts := store.Accounts()
	if len(accounts) == 0 {
		t.Fatal("no configured accounts")
	}
	codingPlan := NewCodingPlanClient(config, store)
	oauth := NewOAuthManager(config, store, codingPlan)
	codingPlan.SetOAuthManager(oauth)
	router := NewModelRouter(store)
	proxy := NewProxy(config, store, router, oauth)
	api := NewAPI(store, oauth, codingPlan, router, proxy)
	streaming := os.Getenv("ATOM2API_LIVE_PROBE_STREAM") == "1"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, account := range accounts {
		result, err := api.probeAccountProtocols(ctx, account.ID, streaming)
		if err != nil {
			t.Errorf("probe account %s: %v", account.Name, err)
			continue
		}
		encoded, _ := json.MarshalIndent(result, "", "  ")
		t.Logf("account protocol probe:\n%s", encoded)
	}
}
