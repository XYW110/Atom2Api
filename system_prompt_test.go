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
