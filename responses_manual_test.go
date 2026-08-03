package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestManualGLMResponsesChatCompatibility(t *testing.T) {
	if os.Getenv("ATOM2API_LIVE_GLM") != "1" {
		t.Skip("set ATOM2API_LIVE_GLM=1 to run against the configured AtomGit account")
	}
	configPath := strings.TrimSpace(os.Getenv("ATOM2API_CONFIG_PATH"))
	if configPath == "" {
		configPath = "config.json"
	}
	config, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	store, err := NewStore(config.Snapshot().DataPath, config)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	router := NewModelRouter(store)
	route, err := router.Resolve("GLM-5.2", APIKey{})
	if err != nil {
		t.Fatalf("GLM-5.2 is not routable: %v", err)
	}
	if !route.ResponsesChatCompat {
		t.Fatal("enable Responses-to-Chat compatibility for GLM-5.2 in model settings before running this test")
	}
	codingPlan := NewCodingPlanClient(config, store)
	oauth := NewOAuthManager(config, store, codingPlan)
	codingPlan.SetOAuthManager(oauth)
	proxy := NewProxy(config, store, router, oauth)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"GLM-5.2",
		"input":"Reply with a short greeting.",
		"max_output_tokens":64,
		"store":false
	}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	proxy.HandleRequest(response, request, APIKey{})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get(responsesFallbackHeader) != "chat-completions" {
		t.Fatalf("compatibility header = %q", response.Header().Get(responsesFallbackHeader))
	}
	var body struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode Responses result: %v", err)
	}
	if body.Object != "response" || body.Model != "GLM-5.2" {
		t.Fatalf("Responses identity = %#v", body)
	}
	if len(body.Output) == 0 || len(body.Output[0].Content) == 0 || strings.TrimSpace(body.Output[0].Content[0].Text) == "" {
		t.Fatalf("Responses output is empty: %s", response.Body.String())
	}

	streamRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"GLM-5.2",
		"input":"Reply with one short sentence.",
		"max_output_tokens":64,
		"store":false,
		"stream":true
	}`)).WithContext(ctx)
	streamResponse := httptest.NewRecorder()
	proxy.HandleRequest(streamResponse, streamRequest, APIKey{})
	if streamResponse.Code != http.StatusOK {
		t.Fatalf("stream status = %d, body = %s", streamResponse.Code, streamResponse.Body.String())
	}
	if streamResponse.Header().Get(responsesFallbackHeader) != "chat-completions" {
		t.Fatalf("stream compatibility header = %q", streamResponse.Header().Get(responsesFallbackHeader))
	}
	streamBody := streamResponse.Body.String()
	for _, expected := range []string{"event: response.created", `"type":"response.output_text.delta"`, `"type":"response.completed"`, `"usage"`} {
		if !strings.Contains(streamBody, expected) {
			t.Fatalf("GLM-5.2 stream does not contain %q: %s", expected, streamBody)
		}
	}

	toolRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"GLM-5.2",
		"input":"Look up the weather for Hong Kong.",
		"store":false,
		"tools":[{
			"type":"function",
			"name":"get_weather",
			"description":"Get the weather for a city",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}
		}],
		"tool_choice":{"type":"function","name":"get_weather"}
	}`)).WithContext(ctx)
	toolResponse := httptest.NewRecorder()
	proxy.HandleRequest(toolResponse, toolRequest, APIKey{})
	if toolResponse.Code != http.StatusOK {
		t.Fatalf("tool status = %d, body = %s", toolResponse.Code, toolResponse.Body.String())
	}
	var toolBody struct {
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(toolResponse.Body.Bytes(), &toolBody); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(toolBody.Output) == 0 || toolBody.Output[0].Type != "function_call" || toolBody.Output[0].Name != "get_weather" || toolBody.Output[0].CallID == "" || toolBody.Output[0].Arguments == "" {
		t.Fatalf("GLM-5.2 tool output = %s", toolResponse.Body.String())
	}

	python, err := exec.LookPath("python")
	if err != nil {
		t.Log("python is unavailable; skipping OpenAI SDK compatibility check")
		return
	}
	if err := exec.CommandContext(ctx, python, "-c", "import openai").Run(); err != nil {
		t.Log("OpenAI Python SDK is unavailable; skipping SDK compatibility check")
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.HandleRequest(w, r, APIKey{})
	}))
	defer server.Close()
	script := `
import sys
from openai import OpenAI

client = OpenAI(base_url=sys.argv[1] + "/v1", api_key="live-test", timeout=90)
response = client.responses.create(
    model="GLM-5.2",
    input="Reply with a short greeting.",
    max_output_tokens=64,
    store=False,
)
assert response.model == "GLM-5.2"
assert response.output_text.strip()

deltas = []
with client.responses.stream(
    model="GLM-5.2",
    input="Reply with one short sentence.",
    max_output_tokens=64,
    store=False,
) as stream:
    for event in stream:
        if event.type == "response.output_text.delta":
            deltas.append(event.delta)
    final = stream.get_final_response()
assert "".join(deltas).strip()
assert final.status == "completed"

tool_response = client.responses.create(
    model="GLM-5.2",
    input="Look up the weather for Hong Kong.",
    store=False,
    tools=[{
        "type": "function",
        "name": "get_weather",
        "description": "Get the weather for a city",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"],
        },
    }],
    tool_choice={"type": "function", "name": "get_weather"},
)
tool_call = next(item for item in tool_response.output if item.type == "function_call")
assert tool_call.name == "get_weather"
assert tool_call.call_id
assert tool_call.arguments
`
	command := exec.CommandContext(ctx, python, "-c", script, server.URL)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("OpenAI Python SDK compatibility check: %v\n%s", err, output)
	}
}
