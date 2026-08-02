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

func TestProxyAuditForcesUpstreamErrorResponseWhenDebugDisabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	if record.RequestBody != "" || len(record.RequestHeaders) != 0 {
		t.Fatalf("request details were recorded while debug was disabled: %#v", record)
	}
	if record.ResponseBody != response.Body.String() || !strings.Contains(record.ResponseBody, "quota exceeded") {
		t.Fatalf("forced response body = %q", record.ResponseBody)
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
