package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemPromptDefaultsAndSettings(t *testing.T) {
	manager, err := NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	snapshot := manager.Snapshot()
	if snapshot.SystemPromptEnabled {
		t.Fatal("system prompt is enabled by default")
	}
	if snapshot.SystemPrompt != defaultSystemPrompt {
		t.Fatalf("default system prompt = %q", snapshot.SystemPrompt)
	}
}

func TestApplySystemPromptToChatRequest(t *testing.T) {
	payload := map[string]json.RawMessage{
		"messages": json.RawMessage(`[{"role":"user","content":"hello"}]`),
	}
	if err := applySystemPrompt(payload, "/v1/chat/completions", "gpt-test", "AtomCode on {model}"); err != nil {
		t.Fatalf("applySystemPrompt() error = %v", err)
	}
	var messages []map[string]any
	if err := json.Unmarshal(payload["messages"], &messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(messages) != 2 || messages[0]["role"] != "system" || messages[0]["content"] != "AtomCode on gpt-test" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestApplySystemPromptToResponsesInstructions(t *testing.T) {
	payload := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"Be concise"`),
	}
	if err := applySystemPrompt(payload, "/v1/responses", "responses-model", "Use {model}"); err != nil {
		t.Fatalf("applySystemPrompt() error = %v", err)
	}
	var instructions string
	if err := json.Unmarshal(payload["instructions"], &instructions); err != nil {
		t.Fatalf("decode instructions: %v", err)
	}
	if instructions != "Use responses-model\n\nBe concise" {
		t.Fatalf("instructions = %q", instructions)
	}
}

func TestApplySystemPromptRejectsInvalidInstructions(t *testing.T) {
	payload := map[string]json.RawMessage{"instructions": json.RawMessage(`{"role":"system"}`)}
	err := applySystemPrompt(payload, "/v1/responses", "gpt-test", "prompt")
	if err == nil || !strings.Contains(err.Error(), "instructions must be a string") {
		t.Fatalf("error = %v", err)
	}
}

func TestNormalizeChatMessagesMovesSystemToFront(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "hello"},
		map[string]any{"role": "assistant", "content": "hi"},
		map[string]any{"role": "system", "content": "be brief"},
		map[string]any{"role": "tool", "content": "ok", "tool_call_id": "call-1"},
	}
	normalized := normalizeChatMessages(messages)
	if len(normalized) != 4 {
		t.Fatalf("len = %d", len(normalized))
	}
	first := normalized[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be brief" {
		t.Fatalf("first = %#v", first)
	}
	if normalized[1].(map[string]any)["role"] != "user" || normalized[2].(map[string]any)["role"] != "assistant" || normalized[3].(map[string]any)["role"] != "tool" {
		t.Fatalf("order = %#v", normalized)
	}
}

func TestNormalizeChatMessagesMergesSystemAndDeveloper(t *testing.T) {
	messages := []any{
		map[string]any{"role": "developer", "content": "dev rule"},
		map[string]any{"role": "user", "content": "hello"},
		map[string]any{"role": "system", "content": "sys rule"},
	}
	normalized := normalizeChatMessages(messages)
	if len(normalized) != 2 {
		t.Fatalf("len = %d, messages = %#v", len(normalized), normalized)
	}
	first := normalized[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "dev rule\n\nsys rule" {
		t.Fatalf("merged = %#v", first)
	}
	if normalized[1].(map[string]any)["content"] != "hello" {
		t.Fatalf("rest = %#v", normalized[1])
	}
}

func TestNormalizeChatMessagesLeavesPlainChatUnchanged(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "hello"},
		map[string]any{"role": "assistant", "content": "hi"},
	}
	normalized := normalizeChatMessages(messages)
	if len(normalized) != 2 || normalized[0].(map[string]any)["role"] != "user" {
		t.Fatalf("unchanged = %#v", normalized)
	}
}

func TestResponsesCompatMovesLateDeveloperToFront(t *testing.T) {
	payload := map[string]json.RawMessage{
		"model": json.RawMessage(`"qwen3.8-27b"`),
		"input": json.RawMessage(`[{"role":"user","content":"hello"},{"role":"developer","content":"stay concise"}]`),
	}
	body, _, err := responsesRequestToChat(payload)
	if err != nil {
		t.Fatalf("responsesRequestToChat: %v", err)
	}
	var chat map[string]any
	if err := json.Unmarshal(body, &chat); err != nil {
		t.Fatalf("decode chat: %v", err)
	}
	messages := chat["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[0].(map[string]any)["content"] != "stay concise" || messages[1].(map[string]any)["content"] != "hello" {
		t.Fatalf("messages = %#v", messages)
	}
}
