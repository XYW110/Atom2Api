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
	mu       sync.Mutex
	view     AccountView
	err      error
	attempts []CodingPlanClaimAttempt
	planName string
	message  string
	calls    int
}

func (s *stubPlanClaimer) ClaimAndSyncDetailed(context.Context, string) (CodingPlanClaimOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return CodingPlanClaimOutcome{
		Account: s.view, Attempts: append([]CodingPlanClaimAttempt(nil), s.attempts...),
		PlanName: s.planName, Message: s.message,
	}, s.err
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
	outcome, err := NewCodingPlanClient(config, store).ClaimAndSyncDetailed(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("ClaimAndSyncDetailed: %v", err)
	}
	if claimCalls != 1 {
		t.Fatalf("claim calls = %d, want 1", claimCalls)
	}
	if len(outcome.Attempts) != 1 || !outcome.Attempts[0].Duplicate || outcome.Attempts[0].PlanType != "Max" {
		t.Fatalf("claim attempts = %#v", outcome.Attempts)
	}
}

func TestCodingPlanClaimRecordsAllTierResponses(t *testing.T) {
	config, store := newTestStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body["plan_type"] {
		case "Max":
			_, _ = w.Write([]byte(`{"success":false,"duplicate":false,"message":"暂无有效合入 PR，暂不可领取 Max 套餐","plan_type":"Lite","plan_name":"CodingPlan Lite"}`))
		case "Pro":
			_, _ = w.Write([]byte(`{"success":false,"duplicate":false,"message":"未到领取时间，请稍后重试","plan_type":"Lite","plan_name":"CodingPlan Lite"}`))
		case "Lite":
			_, _ = w.Write([]byte(`{"success":true,"duplicate":false,"message":"领取成功","plan_type":"Lite","plan_name":"CodingPlan Lite"}`))
		default:
			http.Error(w, "unexpected tier", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	updated := config.Snapshot().Config
	updated.CodingPlanAPIURL = server.URL
	if err := config.Update(updated); err != nil {
		t.Fatalf("update config: %v", err)
	}
	response, attempts, err := NewCodingPlanClient(config, store).claim(context.Background(), "token")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !response.Success || response.PlanType != "Lite" || len(attempts) != 3 {
		t.Fatalf("response = %#v, attempts = %#v", response, attempts)
	}
	wantMessages := []string{"暂无有效合入 PR，暂不可领取 Max 套餐", "未到领取时间，请稍后重试", "领取成功"}
	for i, tier := range []string{"Max", "Pro", "Lite"} {
		if attempts[i].PlanType != tier || attempts[i].HTTPStatus != http.StatusOK || attempts[i].Message != wantMessages[i] || attempts[i].Response == "" {
			t.Fatalf("attempt %d = %#v", i, attempts[i])
		}
	}
}

func TestPlanClaimScheduleAndLogs(t *testing.T) {
	_, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	if !account.ClaimSchedule.Enabled || account.ClaimSchedule.Cron != defaultPlanClaimCron {
		t.Fatalf("default schedule = %#v", account.ClaimSchedule)
	}

	claimer := &stubPlanClaimer{
		view: account, message: "领取成功",
		attempts: []CodingPlanClaimAttempt{{PlanType: "Max", HTTPStatus: http.StatusOK, Response: `{"success":true,"message":"领取成功"}`, Success: true, Message: "领取成功"}},
	}
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
	if manual.Log.Message != "领取成功" || len(manual.Log.Attempts) != 1 || manual.Log.Attempts[0].Message != "领取成功" {
		t.Fatalf("manual log details = %#v", manual.Log)
	}

	claimer.err = errors.New("claim unavailable")
	claimer.attempts = []CodingPlanClaimAttempt{
		{PlanType: "Max", HTTPStatus: http.StatusOK, Response: `{"success":false}`, Message: "Max unavailable"},
		{PlanType: "Pro", HTTPStatus: http.StatusOK, Response: `{"success":false}`, Message: "Pro unavailable"},
		{PlanType: "Lite", HTTPStatus: http.StatusOK, Response: `{"success":false}`, Message: "Lite unavailable"},
	}
	if _, err := service.Claim(context.Background(), account.ID, planClaimTriggerScheduled); err == nil {
		t.Fatal("scheduled claim succeeded")
	}
	logs := store.PlanClaimLogs(account.ID, 10)
	if len(logs) != 2 || logs[0].Trigger != planClaimTriggerScheduled || logs[0].Status != "failed" || logs[1].Status != "success" {
		t.Fatalf("claim logs = %#v", logs)
	}
	if len(logs[0].Attempts) != 3 || logs[0].Attempts[1].Message != "Pro unavailable" {
		t.Fatalf("failed claim attempts = %#v", logs[0].Attempts)
	}
}

func TestAccountScheduleAPIValidationAndClaimLogFilter(t *testing.T) {
	_, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	service := NewPlanClaimService(store, &stubPlanClaimer{
		view: account, message: "领取成功",
		attempts: []CodingPlanClaimAttempt{{
			PlanType: "Lite", HTTPStatus: http.StatusOK, Response: `{"success":true,"message":"领取成功"}`,
			Success: true, Message: "领取成功",
		}},
	})
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
	if logsResponse.Code != http.StatusOK {
		t.Fatalf("logs status = %d, body = %s", logsResponse.Code, logsResponse.Body.String())
	}
	var logsPayload struct {
		Data []PlanClaimLog `json:"data"`
	}
	if err := json.Unmarshal(logsResponse.Body.Bytes(), &logsPayload); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if len(logsPayload.Data) != 1 || logsPayload.Data[0].Trigger != planClaimTriggerManual || len(logsPayload.Data[0].Attempts) != 1 {
		t.Fatalf("logs payload = %#v", logsPayload.Data)
	}
	attempt := logsPayload.Data[0].Attempts[0]
	if attempt.Message != "领取成功" || !strings.Contains(attempt.Response, `"success":true`) {
		t.Fatalf("log attempt = %#v", attempt)
	}
}
