package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsHandlers(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	manager, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getResponse := httptest.NewRecorder()
	handleGetSettings(manager).ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getResponse.Code)
	}

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"user_agent":"api-client/3.0","admin_password":"updated-secret","audit_debug_enabled":true,"system_prompt_enabled":true,"system_prompt":"Run {model} as AtomCode","request_retry_count":3,"request_retry_status_codes":"500-502,503,429,500","audit_retention_days":45,"audit_detail_retention_days":15}`))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	handleUpdateSettings(manager).ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}

	var response settingsResponse
	if err := json.Unmarshal(updateResponse.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if response.UserAgent != "api-client/3.0" || !response.AuditDebugEnabled || !response.SystemPromptEnabled || response.SystemPrompt != "Run {model} as AtomCode" || response.RequestRetryCount != 3 || response.RetryStatusCodes != "429,500-503" || response.AuditRetentionDays != 45 || response.AuditDetailRetentionDays != 15 {
		t.Fatalf("PUT response = %#v", response)
	}
	if got := manager.Snapshot().UserAgent; got != "api-client/3.0" {
		t.Fatalf("runtime UserAgent = %q", got)
	}
	if !manager.Snapshot().AuditDebugEnabled {
		t.Fatal("runtime AuditDebugEnabled = false")
	}
	if snapshot := manager.Snapshot(); !snapshot.SystemPromptEnabled || snapshot.SystemPrompt != "Run {model} as AtomCode" {
		t.Fatalf("runtime system prompt settings = (%v, %q)", snapshot.SystemPromptEnabled, snapshot.SystemPrompt)
	}
	if snapshot := manager.Snapshot(); snapshot.RequestRetryCount != 3 || snapshot.RetryStatusCodes != "429,500-503" {
		t.Fatalf("runtime retry settings = (%d, %q)", snapshot.RequestRetryCount, snapshot.RetryStatusCodes)
	}
	if snapshot := manager.Snapshot(); snapshot.AuditRetentionDays != 45 || snapshot.AuditDetailRetentionDays != 15 {
		t.Fatalf("runtime audit retention settings = (%d, %d)", snapshot.AuditRetentionDays, snapshot.AuditDetailRetentionDays)
	}
	if password := manager.Snapshot().AdminPassword; password == "updated-secret" || !adminPasswordMatches(password, "updated-secret") {
		t.Fatal("runtime admin password is not stored as a matching bcrypt hash")
	}
	reloaded, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if snapshot := reloaded.Snapshot(); snapshot.AuditRetentionDays != 45 || snapshot.AuditDetailRetentionDays != 15 {
		t.Fatalf("persisted audit retention settings = (%d, %d)", snapshot.AuditRetentionDays, snapshot.AuditDetailRetentionDays)
	}
}

func TestUpdateSettingsRejectsInvalidUserAgent(t *testing.T) {
	manager, err := NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"user_agent":""}`))
	response := httptest.NewRecorder()
	handleUpdateSettings(manager).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if got := manager.Snapshot().UserAgent; got != defaultUserAgent {
		t.Fatalf("runtime UserAgent = %q after rejected update", got)
	}
}
