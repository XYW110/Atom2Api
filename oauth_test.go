package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOAuthCompletionClaimsCodingPlanOnce(t *testing.T) {
	config, store := newTestStore(t)
	claimCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			writeJSON(w, http.StatusOK, platformLoginResponse{LoginURL: "https://example.com/login", State: "oauth-state"})
		case "/auth/check":
			writeJSON(w, http.StatusOK, platformCheckResponse{Valid: true})
		case "/auth/token":
			writeJSON(w, http.StatusOK, platformTokenResponse{
				AccessToken: "access-token", RefreshToken: "refresh-token", TokenType: "Bearer", ExpiresIn: 3600,
				User: UserInfo{ID: "oauth-user", Username: "oauth-user", Name: "OAuth User"},
			})
		case "/coding-plan/claim-v2":
			claimCalls++
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode claim request: %v", err)
			}
			writeJSON(w, http.StatusOK, codingPlanClaimResponse{Success: true, PlanName: "CodingPlan Max", PlanType: request["plan_type"], Message: "claimed"})
		case "/coding-plan/status-v2":
			_, _ = w.Write([]byte(`{"codingplan_free":{"plan_name":"CodingPlan Max","status":1},"rate_limit_windows":[]}`))
		case "/coding-plan/models-v2":
			_, _ = w.Write([]byte(`[]`))
		case "/coding-plan/usage":
			_, _ = w.Write([]byte(`{"days":60,"rows":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	updated := config.Snapshot().Config
	updated.PlatformBaseURL = server.URL
	updated.CodingPlanAPIURL = server.URL
	if err := config.Update(updated); err != nil {
		t.Fatalf("update config: %v", err)
	}

	codingPlan := NewCodingPlanClient(config, store)
	oauth := NewOAuthManager(config, store, codingPlan)
	codingPlan.SetOAuthManager(oauth)
	oauth.SetPlanClaimService(NewPlanClaimService(store, codingPlan))

	started, err := oauth.Start(context.Background())
	if err != nil {
		t.Fatalf("start OAuth: %v", err)
	}
	result, err := oauth.Poll(context.Background(), started.ID)
	if err != nil {
		t.Fatalf("complete OAuth: %v", err)
	}
	if result.Status != "complete" || result.Account == nil || result.Account.Status != "active" {
		t.Fatalf("OAuth result = %#v", result)
	}
	if claimCalls != 1 {
		t.Fatalf("claim calls = %d, want 1", claimCalls)
	}
	logs := store.PlanClaimLogs(result.Account.ID, 10)
	if len(logs) != 1 || logs[0].Trigger != planClaimTriggerManual || logs[0].Status != "success" {
		t.Fatalf("claim logs = %#v", logs)
	}

	result, err = oauth.Poll(context.Background(), started.ID)
	if err != nil || result.Status != "complete" {
		t.Fatalf("repeat OAuth poll result = %#v, error = %v", result, err)
	}
	if claimCalls != 1 || len(store.PlanClaimLogs(result.Account.ID, 10)) != 1 {
		t.Fatalf("repeat poll created another claim: calls = %d, logs = %#v", claimCalls, store.PlanClaimLogs(result.Account.ID, 10))
	}
}
