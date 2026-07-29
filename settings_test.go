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
	manager, err := NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getResponse := httptest.NewRecorder()
	handleGetSettings(manager).ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getResponse.Code)
	}

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"user_agent":"api-client/3.0","audit_debug_enabled":true}`))
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
	if response.UserAgent != "api-client/3.0" || !response.AuditDebugEnabled {
		t.Fatalf("PUT response = %#v", response)
	}
	if got := manager.Snapshot().UserAgent; got != "api-client/3.0" {
		t.Fatalf("runtime UserAgent = %q", got)
	}
	if !manager.Snapshot().AuditDebugEnabled {
		t.Fatal("runtime AuditDebugEnabled = false")
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
