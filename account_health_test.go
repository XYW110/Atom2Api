package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func addAccountForHealthTest(t *testing.T, store *Store, userID, status string) AccountView {
	t.Helper()
	view, err := store.UpsertAccount(Account{
		Name: userID, Status: status, Enabled: status == "active",
		User: UserInfo{ID: userID, Username: userID},
	}, "access-"+userID, "refresh-"+userID)
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	return view
}

func TestDeleteErrorAccountsHandlerOnlyDeletesErrors(t *testing.T) {
	_, store := newTestStore(t)
	addAccountForHealthTest(t, store, "error-1", "error")
	addAccountForHealthTest(t, store, "active-1", "active")
	addAccountForHealthTest(t, store, "error-2", "error")

	response := httptest.NewRecorder()
	(&API{store: store}).HandleErrorAccountsDelete(response, httptest.NewRequest(http.MethodDelete, "/api/accounts/errors", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2", result.Deleted)
	}
	accounts := store.Accounts()
	if len(accounts) != 1 || accounts[0].User.ID != "active-1" {
		t.Fatalf("remaining accounts = %#v", accounts)
	}
}

func TestProxyDisablesAccountOnUpstreamAuthenticationFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"message":"invalid credentials"}}`))
			}))
			defer upstream.Close()

			config, store := newTestStore(t)
			account := addTestAccount(t, store, upstream.URL)
			if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "gpt-auth", Enabled: true}); err != nil {
				t.Fatalf("SetModelSetting: %v", err)
			}
			proxy := NewProxy(config, store, NewModelRouter(store), nil)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-auth","messages":[]}`))
			proxy.HandleRequest(response, request, APIKey{})
			if response.Code != status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
			}
			stored, _, _, err := store.Account(account.ID)
			if err != nil {
				t.Fatalf("Account: %v", err)
			}
			if stored.Enabled || stored.Status != "error" || stored.LastError == "" {
				t.Fatalf("account was not disabled: %#v", stored.View())
			}
			if _, err := proxy.router.Resolve("gpt-auth", APIKey{}); err == nil || !strings.Contains(err.Error(), "no active account") {
				t.Fatalf("route after disable error = %v", err)
			}
		})
	}
}

func TestProxyOnlyDisablesPermanentOAuthRefreshFailures(t *testing.T) {
	tests := []struct {
		name         string
		brokerStatus int
		disabled     bool
	}{
		{name: "rejected credentials", brokerStatus: http.StatusUnauthorized, disabled: true},
		{name: "temporary broker failure", brokerStatus: http.StatusInternalServerError, disabled: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "refresh failed", test.brokerStatus)
			}))
			defer broker.Close()

			config, store := newTestStore(t)
			updated := config.Snapshot().Config
			updated.PlatformBaseURL = broker.URL
			if err := config.Update(updated); err != nil {
				t.Fatalf("update config: %v", err)
			}
			account := addTestAccount(t, store, "https://upstream.example")
			if _, err := store.UpdateAccount(account.ID, func(stored *Account) error {
				stored.Credentials.ExpiresIn = 3600
				stored.Credentials.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
				return nil
			}); err != nil {
				t.Fatalf("expire credentials: %v", err)
			}
			if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "gpt-refresh", Enabled: true}); err != nil {
				t.Fatalf("SetModelSetting: %v", err)
			}
			oauth := NewOAuthManager(config, store, nil)
			proxy := NewProxy(config, store, NewModelRouter(store), oauth)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-refresh","messages":[]}`))
			proxy.HandleRequest(response, request, APIKey{})
			if response.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			stored, _, _, err := store.Account(account.ID)
			if err != nil {
				t.Fatalf("Account: %v", err)
			}
			if got := !stored.Enabled && stored.Status == "error"; got != test.disabled {
				t.Fatalf("disabled = %t, want %t; account = %#v", got, test.disabled, stored.View())
			}
		})
	}
}
