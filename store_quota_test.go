package main

import (
	"net/http"
	"testing"
)

func TestRecordUsageUpdatesAccountRateLimitWindowsLocally(t *testing.T) {
	config, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	_, err := store.UpdateAccount(account.ID, func(stored *Account) error {
		stored.Plan.RateLimitWindows = []RateLimitWindow{
			{ShowEnable: 1, CallLimit: 10, CallsUsed: 9, UsagePercent: 90},
			{ShowEnable: 1, CallLimit: 100, CallsUsed: 49, UsagePercent: 49},
			{ShowEnable: 0, CallLimit: 5, CallsUsed: 2, UsagePercent: 40},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}

	if err := store.RecordUsage(UsageRecord{AccountID: account.ID, Status: http.StatusOK}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	reloaded, err := NewStore(store.path, config)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	stored, _, _, err := reloaded.Account(account.ID)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	windows := stored.Plan.RateLimitWindows
	if windows[0].CallsUsed != 10 || windows[0].UsagePercent != 100 || !windows[0].QuotaExhausted {
		t.Fatalf("exhausted window = %#v", windows[0])
	}
	if windows[1].CallsUsed != 50 || windows[1].UsagePercent != 50 || windows[1].QuotaExhausted {
		t.Fatalf("long window = %#v", windows[1])
	}
	if windows[2].CallsUsed != 2 || windows[2].UsagePercent != 40 {
		t.Fatalf("hidden window was updated: %#v", windows[2])
	}
}

func TestRecordUsageDoesNotUpdateRateLimitWindowsForFailedRequest(t *testing.T) {
	_, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	_, err := store.UpdateAccount(account.ID, func(stored *Account) error {
		stored.Plan.RateLimitWindows = []RateLimitWindow{{ShowEnable: 1, CallLimit: 10, CallsUsed: 4, UsagePercent: 40}}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}

	if err := store.RecordUsage(UsageRecord{AccountID: account.ID, Status: http.StatusBadGateway, Error: "upstream failed"}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	stored, _, _, err := store.Account(account.ID)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	window := stored.Plan.RateLimitWindows[0]
	if window.CallsUsed != 4 || window.UsagePercent != 40 || window.QuotaExhausted {
		t.Fatalf("failed request updated window: %#v", window)
	}
}
