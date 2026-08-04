package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyAuditDebugRecordsBodiesAndSanitizedHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-access-secret" {
			t.Errorf("upstream Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Debug", "present")
		w.Header().Set("Set-Cookie", "upstream-session=secret")
		_, _ = w.Write([]byte(`{"model":"upstream-model","choices":[]}`))
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	addTestAccount(t, store, upstream.URL)
	if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "gpt-debug", Enabled: true}); err != nil {
		t.Fatalf("SetModelSetting: %v", err)
	}
	updated := config.Snapshot().Config
	updated.AuditDebugEnabled = true
	if err := config.Update(updated); err != nil {
		t.Fatalf("enable audit debug: %v", err)
	}
	_, secret, err := store.CreateAPIKey("debug", nil, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	api := NewAPI(store, nil, nil, proxy.router, proxy)
	requestBody := `{"model":"gpt-debug","messages":[{"role":"user","content":"trace me"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	api.RequireAPIKey(proxy.HandleRequest).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	records := store.UsageRecords()
	if len(records) != 1 {
		t.Fatalf("usage records = %#v", records)
	}
	record := records[0]
	if record.RequestBody != requestBody || record.ResponseBody != response.Body.String() {
		t.Fatalf("audit bodies = %#v", record)
	}
	if got := record.RequestHeaders["Authorization"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("request Authorization = %#v", got)
	}
	if got := record.RequestHeaders["Accept"]; len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("request Accept = %#v", got)
	}
	if got := record.ResponseHeaders["X-Upstream-Debug"]; len(got) != 1 || got[0] != "present" {
		t.Fatalf("response X-Upstream-Debug = %#v", got)
	}
	if got := record.ResponseHeaders["Set-Cookie"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("response Set-Cookie = %#v", got)
	}
	data := storedUsageData(t, store)
	for _, secretValue := range [][]byte{[]byte("oauth-access-secret"), []byte("upstream-session=secret"), []byte(secret)} {
		if bytes.Contains(data, secretValue) {
			t.Fatalf("usage log contains secret %q", secretValue)
		}
	}
}

func TestProxySanitizesUpstreamErrorAndKeepsAuditDetail(t *testing.T) {
	var upstreamRequestID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequestID = r.Header.Get("X-Request-Id")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.Header().Set("Set-Cookie", "error-session=secret")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	addTestAccount(t, store, upstream.URL)
	if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "gpt-error", Enabled: true}); err != nil {
		t.Fatalf("SetModelSetting: %v", err)
	}
	_, secret, err := store.CreateAPIKey("error", nil, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	api := NewAPI(store, nil, nil, proxy.router, proxy)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-error","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	api.RequireAPIKey(proxy.HandleRequest).ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	records := store.UsageRecords()
	if len(records) != 1 {
		t.Fatalf("usage records = %#v", records)
	}
	record := records[0]
	requestID := response.Header().Get("X-Request-Id")
	if requestID == "" || record.ID != requestID || upstreamRequestID != requestID {
		t.Fatalf("request ids = response %q, audit %q, upstream %q", requestID, record.ID, upstreamRequestID)
	}
	if record.RequestBody != "" {
		t.Fatalf("request body was recorded while debug was disabled: %#v", record)
	}
	if got := record.RequestHeaders["Authorization"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("request Authorization = %#v", got)
	}
	if got := record.RequestHeaders["Accept"]; len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("request Accept = %#v", got)
	}
	expectedMessage := "status_code=429,upstream request failed. request_id=" + requestID
	if record.ResponseBody != response.Body.String() || !strings.Contains(response.Body.String(), expectedMessage) {
		t.Fatalf("sanitized response body = %q", record.ResponseBody)
	}
	if strings.Contains(response.Body.String(), "quota exceeded") {
		t.Fatalf("response exposed upstream error: %s", response.Body.String())
	}
	if !strings.Contains(record.Error, "quota exceeded") {
		t.Fatalf("audit error = %q", record.Error)
	}
	if got := record.ResponseHeaders["Retry-After"]; len(got) != 1 || got[0] != "30" {
		t.Fatalf("response Retry-After = %#v", got)
	}
	if got := record.ResponseHeaders["Set-Cookie"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("response Set-Cookie = %#v", got)
	}
	data := storedUsageData(t, store)
	if bytes.Contains(data, []byte("error-session=secret")) {
		t.Fatal("usage log contains upstream Set-Cookie value")
	}
}

func TestProxyLocalFailureKeepsSanitizedAuditDetails(t *testing.T) {
	config, store := newTestStore(t)
	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":`))
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("X-Client-Trace", "trace-123")
	response := httptest.NewRecorder()

	proxy.HandleRequest(response, request, APIKey{ID: "key_failure"})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	records := store.UsageRecords()
	if len(records) != 1 {
		t.Fatalf("usage records = %#v", records)
	}
	record := records[0]
	if record.RequestBody != "" {
		t.Fatalf("request body was recorded while debug was disabled: %q", record.RequestBody)
	}
	if got := record.RequestHeaders["Authorization"]; len(got) != 1 || got[0] != "[REDACTED]" {
		t.Fatalf("request Authorization = %#v", got)
	}
	if got := record.RequestHeaders["X-Client-Trace"]; len(got) != 1 || got[0] != "trace-123" {
		t.Fatalf("request trace = %#v", got)
	}
	if record.ResponseBody != response.Body.String() || !strings.Contains(record.ResponseBody, "request body must be a JSON object") {
		t.Fatalf("response body = %q", record.ResponseBody)
	}
	if got := record.ResponseHeaders["Content-Type"]; len(got) != 1 || got[0] != "application/json; charset=utf-8" {
		t.Fatalf("response Content-Type = %#v", got)
	}
	data := storedUsageData(t, store)
	if bytes.Contains(data, []byte("client-secret")) {
		t.Fatal("usage log contains client authorization secret")
	}
}

func TestAuditHeadersRedactsAuthenticationMaterial(t *testing.T) {
	headers := auditHeaders(http.Header{
		"X-API-Key":      {"api-secret"},
		"X-AtomCode-Sig": {"signature-secret"},
		"X-Access-Key":   {"access-secret"},
		"X-Request-ID":   {"request-123"},
	})
	if headers["X-Api-Key"][0] != "[REDACTED]" || headers["X-Atomcode-Sig"][0] != "[REDACTED]" || headers["X-Access-Key"][0] != "[REDACTED]" {
		t.Fatalf("sensitive headers = %#v", headers)
	}
	if headers["X-Request-Id"][0] != "request-123" {
		t.Fatalf("request id = %#v", headers["X-Request-Id"])
	}
}
