package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyRetriesConfiguredStatusesInSingleAuditRecord(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Attempt", string(rune('0'+calls)))
		switch calls {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"temporarily unavailable"}}`))
		default:
			_, _ = w.Write([]byte(`{"model":"upstream-model","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
		}
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	account := addTestAccount(t, store, upstream.URL)
	if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "gpt-retry", Enabled: true}); err != nil {
		t.Fatalf("SetModelSetting: %v", err)
	}
	updated := config.Snapshot().Config
	updated.RequestRetryCount = 2
	updated.RetryStatusCodes = "429,500-503"
	if err := config.Update(updated); err != nil {
		t.Fatalf("configure retries: %v", err)
	}
	_, secret, err := store.CreateAPIKey("retry", nil, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	key, ok := store.AuthenticateAPIKey(secret)
	if !ok {
		t.Fatal("AuthenticateAPIKey rejected the created key")
	}

	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-retry","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.HandleRequest(response, request, key)

	if response.Code != http.StatusOK || calls != 3 {
		t.Fatalf("response = (%d, %s), upstream calls = %d", response.Code, response.Body.String(), calls)
	}
	records := store.UsageRecords()
	if len(records) != 1 {
		t.Fatalf("usage records = %#v", records)
	}
	record := records[0]
	if record.RetryCount != 2 || len(record.Attempts) != 3 || record.Status != http.StatusOK {
		t.Fatalf("retry audit = %#v", record)
	}
	wantStatuses := []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK}
	for index, attempt := range record.Attempts {
		if attempt.Attempt != index+1 || attempt.Status != wantStatuses[index] {
			t.Fatalf("attempt %d = %#v", index, attempt)
		}
		if got := attempt.ResponseHeaders["X-Upstream-Attempt"]; len(got) != 1 || got[0] != string(rune('1'+index)) {
			t.Fatalf("attempt %d headers = %#v", index, attempt.ResponseHeaders)
		}
		if attempt.ResponseBody == "" {
			t.Fatalf("attempt %d has no response body", index)
		}
	}
	if !strings.Contains(record.Attempts[0].ResponseBody, "rate limited") || !strings.Contains(record.Attempts[1].ResponseBody, "temporarily unavailable") || !strings.Contains(record.Attempts[2].ResponseBody, "upstream-model") {
		t.Fatalf("attempt response bodies = %#v", record.Attempts)
	}
	accounts := store.Accounts()
	for _, storedAccount := range accounts {
		if storedAccount.ID == account.ID && storedAccount.RequestCount != 1 {
			t.Fatalf("account request count = %d, want 1", storedAccount.RequestCount)
		}
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/audit/"+record.ID, nil)
	detailRequest.SetPathValue("id", record.ID)
	detailResponse := httptest.NewRecorder()
	(&API{store: store}).HandleAuditDetail(detailResponse, detailRequest)
	var detail auditDetailResponse
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil || detail.RetryCount != 2 || len(detail.Attempts) != 3 {
		t.Fatalf("audit detail = %#v, error = %v", detail, err)
	}
}

func TestProxyRetriesBeforeForwardingStreamingResponse(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"try again"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\"upstream-model\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	addTestAccount(t, store, upstream.URL)
	if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "gpt-retry-stream", Enabled: true}); err != nil {
		t.Fatalf("SetModelSetting: %v", err)
	}
	updated := config.Snapshot().Config
	updated.RequestRetryCount = 1
	updated.RetryStatusCodes = "503"
	if err := config.Update(updated); err != nil {
		t.Fatalf("configure retries: %v", err)
	}
	_, secret, err := store.CreateAPIKey("retry-stream", nil, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	key, ok := store.AuthenticateAPIKey(secret)
	if !ok {
		t.Fatal("AuthenticateAPIKey rejected the created key")
	}

	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-retry-stream","stream":true,"messages":[]}`))
	response := httptest.NewRecorder()
	proxy.HandleRequest(response, request, key)

	if response.Code != http.StatusOK || calls != 2 || !strings.Contains(response.Body.String(), `"model":"gpt-retry-stream"`) {
		t.Fatalf("stream response = (%d, %s), upstream calls = %d", response.Code, response.Body.String(), calls)
	}
	records := store.UsageRecords()
	if len(records) != 1 || records[0].RetryCount != 1 || len(records[0].Attempts) != 2 ||
		!strings.Contains(records[0].Attempts[1].ResponseBody, `"model":"upstream-model"`) {
		t.Fatalf("stream retry audit = %#v", records)
	}
}
