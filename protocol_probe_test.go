package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountProtocolProbeReportsChatAndResponsesSeparately(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode probe request: %v", err)
		}
		if request["model"] != "upstream-model" || request["stream"] != false {
			t.Errorf("probe request = %#v", request)
		}
		switch r.URL.Path {
		case "/v1/chat/completions":
			if _, exists := request["messages"]; !exists {
				t.Errorf("Chat probe has no messages: %#v", request)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-probe","choices":[{"message":{"content":"OK"}}]}`))
		case "/v1/responses":
			if _, exists := request["input"]; !exists {
				t.Errorf("Responses probe has no input: %#v", request)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unsupported responses protocol"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	account := addTestAccount(t, store, upstream.URL)
	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	api := NewAPI(store, nil, nil, proxy.router, proxy)
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/"+account.ID+"/probe", strings.NewReader(`{"streaming":false}`))
	request.SetPathValue("id", account.ID)
	response := httptest.NewRecorder()
	api.HandleAccountProtocolProbe(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body accountProtocolProbeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Streaming || len(body.Results) != 1 {
		t.Fatalf("probe response = %#v", body)
	}
	result := body.Results[0]
	if result.Model != "upstream-model" || !result.Chat.Available || result.Chat.Status != http.StatusOK {
		t.Fatalf("Chat probe = %#v", result.Chat)
	}
	if result.Responses.Available || result.Responses.Status != http.StatusBadRequest || !strings.Contains(result.Responses.Error, "unsupported responses") {
		t.Fatalf("Responses probe = %#v", result.Responses)
	}
}

func TestAccountProtocolProbeSupportsStreamingChecks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode probe request: %v", err)
		}
		if request["stream"] != true {
			t.Errorf("streaming probe request = %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch r.URL.Path {
		case "/v1/chat/completions":
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n"))
		case "/v1/responses":
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	account := addTestAccount(t, store, upstream.URL)
	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	api := NewAPI(store, nil, nil, proxy.router, proxy)
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/"+account.ID+"/probe", strings.NewReader(`{"streaming":true}`))
	request.SetPathValue("id", account.ID)
	response := httptest.NewRecorder()
	api.HandleAccountProtocolProbe(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body accountProtocolProbeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Streaming || len(body.Results) != 1 || !body.Results[0].Chat.Available || !body.Results[0].Responses.Available {
		t.Fatalf("streaming probe response = %#v", body)
	}
}

func TestResponsesChatCompatibilityDefaultsOnlyForGLM(t *testing.T) {
	if !defaultResponsesChatCompat("GLM-5.2") {
		t.Fatal("GLM-5.2 compatibility should default to enabled")
	}
	for _, model := range []string{"deepseek-v4-flash", "GLM-5", "gpt-5"} {
		if defaultResponsesChatCompat(model) {
			t.Fatalf("compatibility unexpectedly enabled for %s", model)
		}
	}
}

func TestLegacyGLMModelSettingDefaultsCompatibilityOn(t *testing.T) {
	var legacy ModelSetting
	if err := json.Unmarshal([]byte(`{"upstream":"GLM-5.2","alias":"GLM-5.2","enabled":true}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if !legacy.ResponsesChatCompat {
		t.Fatal("legacy GLM-5.2 setting should enable compatibility")
	}
	var disabled ModelSetting
	if err := json.Unmarshal([]byte(`{"upstream":"GLM-5.2","alias":"GLM-5.2","enabled":true,"responses_chat_compat":false}`), &disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.ResponsesChatCompat {
		t.Fatal("explicitly disabled GLM-5.2 compatibility should stay disabled")
	}
}
