package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserAgentCheckerReadsWorkspacePackageVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/plain" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "Atom2Api/user-agent-check" {
			t.Errorf("User-Agent = %q", got)
		}
		_, _ = w.Write([]byte(`[package]
version = "9.9.9"

[workspace.package]
version = "5.0.3"
`))
	}))
	defer server.Close()

	checker := newUserAgentChecker(server.URL, server.Client())
	response := httptest.NewRecorder()
	checker.HandleCheck(response, httptest.NewRequest(http.MethodPost, "/api/settings/user-agent/check", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result userAgentCheckResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Version != "5.0.3" || result.UserAgent != "atomcode/5.0.3" {
		t.Fatalf("result = %#v", result)
	}
}

func TestUserAgentCheckerRejectsInvalidManifestVersion(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
	}{
		{name: "missing workspace package", manifest: `[package]
version = "5.0.3"
`},
		{name: "invalid semantic version", manifest: `[workspace.package]
version = "latest"
`},
		{name: "invalid TOML", manifest: `[workspace.package
version = "5.0.3"
`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.manifest))
			}))
			defer server.Close()

			checker := newUserAgentChecker(server.URL, server.Client())
			if _, err := checker.Check(context.Background()); err == nil {
				t.Fatal("Check() error = nil")
			}
		})
	}
}

func TestUserAgentCheckerReportsUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	checker := newUserAgentChecker(server.URL, server.Client())
	response := httptest.NewRecorder()
	checker.HandleCheck(response, httptest.NewRequest(http.MethodPost, "/api/settings/user-agent/check", nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "503 Service Unavailable") {
		t.Fatalf("body = %s", response.Body.String())
	}
}
