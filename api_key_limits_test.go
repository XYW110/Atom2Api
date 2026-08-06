package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAPIKeyLimitsAreStoredAndEditable(t *testing.T) {
	_, store := newTestStore(t)
	key, _, err := store.CreateAPIKeyWithLimits("production", []string{"gpt-test"}, nil, 60, 3)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithLimits: %v", err)
	}
	if key.RPMLimit != 60 || key.ConcurrencyLimit != 3 {
		t.Fatalf("created limits = (%d, %d)", key.RPMLimit, key.ConcurrencyLimit)
	}

	api := NewAPI(store, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPatch, "/api/keys/"+key.ID, strings.NewReader(`{"name":"renamed","rpm_limit":120,"concurrency_limit":5}`))
	request.SetPathValue("id", key.ID)
	response := httptest.NewRecorder()
	api.HandleUpdateKey(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var updated APIKeyView
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.Name != "renamed" || updated.RPMLimit != 120 || updated.ConcurrencyLimit != 5 {
		t.Fatalf("updated key = %#v", updated)
	}

	defaultKey, _, err := store.CreateAPIKey("default", nil, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if defaultKey.RPMLimit != 0 || defaultKey.ConcurrencyLimit != 0 {
		t.Fatalf("default limits = (%d, %d)", defaultKey.RPMLimit, defaultKey.ConcurrencyLimit)
	}
}

func TestAPIKeyLimitValidation(t *testing.T) {
	_, store := newTestStore(t)
	api := NewAPI(store, nil, nil, nil, nil)

	createRequest := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"invalid","rpm_limit":-1}`))
	createResponse := httptest.NewRecorder()
	api.HandleCreateKey(createResponse, createRequest)
	if createResponse.Code != http.StatusBadRequest {
		t.Fatalf("negative limit status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	key, _, err := store.CreateAPIKey("valid", nil, nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	updateRequest := httptest.NewRequest(http.MethodPatch, "/api/keys/"+key.ID, strings.NewReader(`{"name":" "}`))
	updateRequest.SetPathValue("id", key.ID)
	updateResponse := httptest.NewRecorder()
	api.HandleUpdateKey(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusBadRequest {
		t.Fatalf("blank name status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
}

func TestAPIKeyRouteStrategyIsStoredAndValidated(t *testing.T) {
	_, store := newTestStore(t)
	api := NewAPI(store, nil, nil, nil, nil)

	createRequest := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"random","route_strategy":"round_robin"}`))
	createResponse := httptest.NewRecorder()
	api.HandleCreateKey(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		Key APIKeyView `json:"key"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Key.RouteStrategy != RouteStrategyRoundRobin {
		t.Fatalf("created route strategy = %q", created.Key.RouteStrategy)
	}

	updateRequest := httptest.NewRequest(http.MethodPatch, "/api/keys/"+created.Key.ID, strings.NewReader(`{"route_strategy":"fill"}`))
	updateRequest.SetPathValue("id", created.Key.ID)
	updateResponse := httptest.NewRecorder()
	api.HandleUpdateKey(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated APIKeyView
	if err := json.Unmarshal(updateResponse.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.RouteStrategy != RouteStrategyFill {
		t.Fatalf("updated route strategy = %q", updated.RouteStrategy)
	}

	invalidRequest := httptest.NewRequest(http.MethodPatch, "/api/keys/"+created.Key.ID, strings.NewReader(`{"route_strategy":"invalid"}`))
	invalidRequest.SetPathValue("id", created.Key.ID)
	invalidResponse := httptest.NewRecorder()
	api.HandleUpdateKey(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid route strategy status = %d", invalidResponse.Code)
	}
}

func TestAPIKeyLimiterEnforcesRollingRPM(t *testing.T) {
	limiter := newAPIKeyLimiter()
	now := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	key := APIKey{ID: "key-rpm", RPMLimit: 2}

	for i := 0; i < 2; i++ {
		release, _, allowed := limiter.acquire(key)
		if !allowed {
			t.Fatalf("request %d was unexpectedly rejected", i+1)
		}
		release()
	}
	if _, rejection, allowed := limiter.acquire(key); allowed || rejection.retryAfter != 60 {
		t.Fatalf("third request = (allowed %v, retry_after %d)", allowed, rejection.retryAfter)
	}

	now = now.Add(time.Minute + time.Millisecond)
	release, _, allowed := limiter.acquire(key)
	if !allowed {
		t.Fatal("request was not allowed after the rolling window elapsed")
	}
	release()
}

func TestRequireAPIKeyReturnsRPMError(t *testing.T) {
	_, store := newTestStore(t)
	_, secret, err := store.CreateAPIKeyWithLimits("rpm", nil, nil, 1, 0)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithLimits: %v", err)
	}
	api := NewAPI(store, nil, nil, nil, nil)
	handler := api.RequireAPIKey(func(w http.ResponseWriter, _ *http.Request, _ APIKey) {
		w.WriteHeader(http.StatusNoContent)
	})

	request := func() *http.Request {
		result := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		result.Header.Set("Authorization", "Bearer "+secret)
		return result
	}
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, request())
	if firstResponse.Code != http.StatusNoContent {
		t.Fatalf("first response status = %d", firstResponse.Code)
	}

	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, request())
	if secondResponse.Code != http.StatusTooManyRequests || secondResponse.Header().Get("Retry-After") == "" {
		t.Fatalf("second response = %d, headers = %#v, body = %s", secondResponse.Code, secondResponse.Header(), secondResponse.Body.String())
	}
	if !strings.Contains(secondResponse.Body.String(), `"code":"rate_limit_exceeded"`) {
		t.Fatalf("second response body = %s", secondResponse.Body.String())
	}
}

func TestRequireAPIKeyEnforcesConcurrency(t *testing.T) {
	_, store := newTestStore(t)
	_, secret, err := store.CreateAPIKeyWithLimits("concurrent", nil, nil, 0, 1)
	if err != nil {
		t.Fatalf("CreateAPIKeyWithLimits: %v", err)
	}
	api := NewAPI(store, nil, nil, nil, nil)
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var first sync.Once
	handler := api.RequireAPIKey(func(w http.ResponseWriter, _ *http.Request, _ APIKey) {
		first.Do(func() {
			close(entered)
			<-unblock
		})
		w.WriteHeader(http.StatusNoContent)
	})

	firstDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		handler.ServeHTTP(response, request)
		firstDone <- response.Code
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter the handler")
	}

	secondResponse := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	secondRequest.Header.Set("Authorization", "Bearer "+secret)
	handler.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusTooManyRequests || secondResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf("second response = %d, headers = %#v, body = %s", secondResponse.Code, secondResponse.Header(), secondResponse.Body.String())
	}

	close(unblock)
	select {
	case status := <-firstDone:
		if status != http.StatusNoContent {
			t.Fatalf("first response status = %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}

	thirdResponse := httptest.NewRecorder()
	thirdRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	thirdRequest.Header.Set("Authorization", "Bearer "+secret)
	handler.ServeHTTP(thirdResponse, thirdRequest)
	if thirdResponse.Code != http.StatusNoContent {
		t.Fatalf("third response status = %d, body = %s", thirdResponse.Code, thirdResponse.Body.String())
	}
}
