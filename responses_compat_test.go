package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestResponsesFallsBackToChatAndCachesModelCapability(t *testing.T) {
	var mu sync.Mutex
	responsesRequests := 0
	chatRequests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/responses":
			responsesRequests++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Not Found"}}`))
		case "/v1/chat/completions":
			chatRequests++
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode Chat request: %v", err)
			}
			if request["model"] != "upstream-model" || request["max_tokens"] != float64(64) {
				t.Errorf("Chat request = %#v", request)
			}
			messages, _ := request["messages"].([]any)
			if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[1].(map[string]any)["content"] != "hello" {
				t.Errorf("Chat messages = %#v", messages)
			}
			tools, _ := request["tools"].([]any)
			if len(tools) != 1 || tools[0].(map[string]any)["type"] != "function" {
				t.Errorf("Chat tools = %#v", tools)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-fallback","object":"chat.completion","created":123,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello back"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	addTestAccount(t, store, upstream.URL)
	if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "gpt-responses", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	router := NewModelRouter(store)
	proxy := NewProxy(config, store, router, nil)
	payload := `{"model":"gpt-responses","instructions":"Be concise","input":"hello","max_output_tokens":64,"store":false,"tools":[{"type":"function","name":"weather","description":"Get weather","parameters":{"type":"object"},"strict":true}]}`

	for index := 0; index < 2; index++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
		response := httptest.NewRecorder()
		proxy.HandleRequest(response, request, APIKey{})
		if response.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, body = %s", index, response.Code, response.Body.String())
		}
		if response.Header().Get(responsesFallbackHeader) != "chat-completions" {
			t.Fatalf("request %d compatibility header = %q", index, response.Header().Get(responsesFallbackHeader))
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode Responses body: %v", err)
		}
		if body["object"] != "response" || body["model"] != "gpt-responses" || body["status"] != "completed" {
			t.Fatalf("Responses body = %#v", body)
		}
		output := body["output"].([]any)
		content := output[0].(map[string]any)["content"].([]any)
		if content[0].(map[string]any)["text"] != "hello back" {
			t.Fatalf("Responses output = %#v", output)
		}
		usage := body["usage"].(map[string]any)
		if usage["input_tokens"] != float64(11) || usage["output_tokens"] != float64(4) {
			t.Fatalf("Responses usage = %#v", usage)
		}
	}
	if responsesRequests != 1 || chatRequests != 2 {
		t.Fatalf("upstream request counts: responses=%d chat=%d", responsesRequests, chatRequests)
	}
	records := store.UsageRecords()
	if len(records) != 2 || records[0].InputTokens != 11 || records[0].CachedTokens != 2 {
		t.Fatalf("usage records = %#v", records)
	}
}

func TestResponsesKeepsNativeUpstreamWhenAvailable(t *testing.T) {
	chatRequests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			chatRequests++
		}
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-native","object":"response","model":"upstream-model","status":"completed","output":[]}`))
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	addTestAccount(t, store, upstream.URL)
	if err := store.SetModelSetting(ModelSetting{Upstream: "upstream-model", Alias: "native-alias", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"native-alias","input":"hello"}`))
	response := httptest.NewRecorder()
	proxy.HandleRequest(response, request, APIKey{})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if chatRequests != 0 || response.Header().Get(responsesFallbackHeader) != "" {
		t.Fatalf("native request unexpectedly used Chat fallback")
	}
	if !strings.Contains(response.Body.String(), `"model":"native-alias"`) {
		t.Fatalf("native response model was not rewritten: %s", response.Body.String())
	}
}

func TestResponsesStreamingFallbackRebuildsTextAndToolEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		options, _ := request["stream_options"].(map[string]any)
		if options["include_usage"] != true {
			t.Errorf("stream_options = %#v", options)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, event := range []string{
			`data: {"id":"chatcmpl-stream","created":456,"model":"upstream-model","choices":[{"delta":{"content":"Hi "},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"chatcmpl-stream","created":456,"model":"upstream-model","choices":[{"delta":{"content":"there"},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"chatcmpl-stream","created":456,"model":"upstream-model","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"weather","arguments":"{\"city\":"}}]},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"chatcmpl-stream","created":456,"model":"upstream-model","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Hong Kong\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n",
			`data: {"id":"chatcmpl-stream","created":456,"model":"upstream-model","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":6,"total_tokens":14}}` + "\n\n",
			"data: [DONE]\n\n",
		} {
			_, _ = w.Write([]byte(event))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	addTestAccount(t, store, upstream.URL)
	router := NewModelRouter(store)
	proxy := NewProxy(config, store, router, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"upstream-model","input":"hello","stream":true,"store":false}`))
	response := httptest.NewRecorder()
	proxy.HandleRequest(response, request, APIKey{})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{
		"event: response.created", `"type":"response.output_text.delta"`, `"delta":"Hi "`,
		`"type":"response.function_call_arguments.delta"`, `"arguments":"{\"city\":\"Hong Kong\"}"`,
		`"type":"response.completed"`, `"input_tokens":8`, `"output_tokens":6`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream does not contain %q:\n%s", expected, body)
		}
	}
	records := store.UsageRecords()
	if len(records) != 1 || !records[0].Streaming || records[0].InputTokens != 8 || records[0].OutputTokens != 6 {
		t.Fatalf("stream usage = %#v", records)
	}
}

func TestResponsesFallbackRejectsUnsupportedBuiltInTools(t *testing.T) {
	chatRequests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		chatRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	config, store := newTestStore(t)
	addTestAccount(t, store, upstream.URL)
	proxy := NewProxy(config, store, NewModelRouter(store), nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"upstream-model","input":"hello","tools":[{"type":"web_search_preview"}]}`))
	response := httptest.NewRecorder()
	proxy.HandleRequest(response, request, APIKey{})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unsupported_parameter") {
		t.Fatalf("response (%d) = %s", response.Code, response.Body.String())
	}
	if chatRequests != 0 {
		t.Fatalf("unsupported request reached Chat endpoint %d times", chatRequests)
	}
}

func TestResponsesFunctionCallOutputConvertsToChatMessages(t *testing.T) {
	payload := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(`{
		"model":"GLM-5.2",
		"input":[
			{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"HK\"}"},
			{"type":"function_call_output","call_id":"call_1","output":{"temperature":29}}
		]
	}`), &payload); err != nil {
		t.Fatal(err)
	}
	body, _, err := responsesRequestToChat(payload)
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(body, &chat); err != nil {
		t.Fatal(err)
	}
	messages := chat["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "assistant" || messages[1].(map[string]any)["role"] != "tool" {
		t.Fatalf("Chat messages = %#v", messages)
	}
}
