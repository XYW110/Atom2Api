package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type stubPlanClaimer struct {
	mu    sync.Mutex
	view  AccountView
	err   error
	calls int
}

func (s *stubPlanClaimer) ClaimAndSync(context.Context, string) (AccountView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.view, s.err
}

func TestCodingPlanClaimRunsForExistingPlan(t *testing.T) {
	config, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	claimCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/coding-plan/claim-v2":
			claimCalls++
			_, _ = w.Write([]byte(`{"success":false,"duplicate":true,"plan_name":"CodingPlan Pro"}`))
		case "/coding-plan/status-v2":
			_, _ = w.Write([]byte(`{"codingplan_free":{"plan_name":"CodingPlan Pro","status":1},"rate_limit_windows":[]}`))
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
	updated.CodingPlanAPIURL = server.URL
	if err := config.Update(updated); err != nil {
		t.Fatalf("update config: %v", err)
	}
	if _, err := NewCodingPlanClient(config, store).ClaimAndSync(context.Background(), account.ID); err != nil {
		t.Fatalf("ClaimAndSync: %v", err)
	}
	if claimCalls != 1 {
		t.Fatalf("claim calls = %d, want 1", claimCalls)
	}
}

func TestPlanClaimScheduleAndLogs(t *testing.T) {
	_, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	if !account.ClaimSchedule.Enabled || account.ClaimSchedule.Cron != defaultPlanClaimCron {
		t.Fatalf("default schedule = %#v", account.ClaimSchedule)
	}

	claimer := &stubPlanClaimer{view: account}
	service := NewPlanClaimService(store, claimer)
	if err := service.ValidateCron("15 8 * * 1-5"); err != nil {
		t.Fatalf("valid cron: %v", err)
	}
	for _, expression := range []string{"", "0 10 *", "0 0 10 * * *"} {
		if err := service.ValidateCron(expression); err == nil {
			t.Errorf("ValidateCron(%q) succeeded", expression)
		}
	}

	manual, err := service.Claim(context.Background(), account.ID, planClaimTriggerManual)
	if err != nil {
		t.Fatalf("manual claim: %v", err)
	}
	if manual.Log.Trigger != planClaimTriggerManual || manual.Log.Status != "success" || manual.Log.FinishedAt == nil {
		t.Fatalf("manual log = %#v", manual.Log)
	}

	claimer.err = errors.New("claim unavailable")
	if _, err := service.Claim(context.Background(), account.ID, planClaimTriggerScheduled); err == nil {
		t.Fatal("scheduled claim succeeded")
	}
	logs := store.PlanClaimLogs(account.ID, 10)
	if len(logs) != 2 || logs[0].Trigger != planClaimTriggerScheduled || logs[0].Status != "failed" || logs[1].Status != "success" {
		t.Fatalf("claim logs = %#v", logs)
	}
}

func TestAccountScheduleAPIValidationAndClaimLogFilter(t *testing.T) {
	_, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	service := NewPlanClaimService(store, &stubPlanClaimer{view: account})
	api := &API{store: store, planClaims: service}

	invalidRequest := httptest.NewRequest(http.MethodPatch, "/api/accounts/"+account.ID, strings.NewReader(`{"plan_claim_schedule":{"enabled":true,"cron":"0 10 *"}}`))
	invalidRequest.SetPathValue("id", account.ID)
	invalidResponse := httptest.NewRecorder()
	api.HandleAccountUpdate(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid cron status = %d, body = %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	validRequest := httptest.NewRequest(http.MethodPatch, "/api/accounts/"+account.ID, strings.NewReader(`{"name":"Daily Account","plan_claim_schedule":{"enabled":true,"cron":"30 9 * * 1-5"}}`))
	validRequest.SetPathValue("id", account.ID)
	validResponse := httptest.NewRecorder()
	api.HandleAccountUpdate(validResponse, validRequest)
	if validResponse.Code != http.StatusOK {
		t.Fatalf("valid cron status = %d, body = %s", validResponse.Code, validResponse.Body.String())
	}
	var updated AccountView
	if err := json.Unmarshal(validResponse.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	if updated.Name != "Daily Account" || updated.ClaimSchedule.Cron != "30 9 * * 1-5" {
		t.Fatalf("updated account = %#v", updated)
	}

	if _, err := service.Claim(context.Background(), account.ID, planClaimTriggerManual); err != nil {
		t.Fatalf("claim: %v", err)
	}
	logsRequest := httptest.NewRequest(http.MethodGet, "/api/plan-claims?account_id="+account.ID+"&limit=10", nil)
	logsResponse := httptest.NewRecorder()
	api.HandlePlanClaimLogs(logsResponse, logsRequest)
	if logsResponse.Code != http.StatusOK || !strings.Contains(logsResponse.Body.String(), `"trigger":"manual"`) {
		t.Fatalf("logs status = %d, body = %s", logsResponse.Code, logsResponse.Body.String())
	}
}
