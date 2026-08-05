package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccountCredentialBundleRoundTrip(t *testing.T) {
	_, source := newTestStore(t)
	createdAt := time.Now().UTC().Add(-time.Hour)
	view, err := source.UpsertAccount(Account{
		Name: "跨设备账号", Note: "备用设备", Status: "active", Enabled: false,
		User:        UserInfo{ID: "user-export", Username: "export-user", Name: "Export User"},
		Credentials: OAuthCredentials{TokenType: "Bearer", ExpiresIn: 3600, CreatedAt: createdAt},
	}, "access-export", "refresh-export")
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}

	exportResponse := httptest.NewRecorder()
	(&API{store: source}).HandleAccountCredentialExport(exportResponse, httptest.NewRequest(http.MethodGet, "/api/accounts/export", nil))
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", exportResponse.Code, exportResponse.Body.String())
	}
	if exportResponse.Header().Get("Cache-Control") != "no-store" || exportResponse.Header().Get("Content-Disposition") == "" {
		t.Fatalf("export headers = %#v", exportResponse.Header())
	}
	var bundle accountCredentialBundle
	if err := json.Unmarshal(exportResponse.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if bundle.Version != accountCredentialBundleVersion || len(bundle.Accounts) != 1 {
		t.Fatalf("export bundle = %#v", bundle)
	}
	item := bundle.Accounts[0]
	if item.Credentials.AccessToken != "access-export" || item.Credentials.RefreshToken != "refresh-export" || item.User.ID != "user-export" {
		t.Fatalf("export credentials = %#v", item)
	}
	if bytes.Contains(exportResponse.Body.Bytes(), []byte("v1:")) {
		t.Fatal("export exposed the local encrypted credential")
	}

	singleExportRequest := httptest.NewRequest(http.MethodGet, "/api/accounts/"+view.ID+"/export", nil)
	singleExportRequest.SetPathValue("id", view.ID)
	singleExportResponse := httptest.NewRecorder()
	(&API{store: source}).HandleAccountCredentialExport(singleExportResponse, singleExportRequest)
	if singleExportResponse.Code != http.StatusOK {
		t.Fatalf("single export status = %d, body = %s", singleExportResponse.Code, singleExportResponse.Body.String())
	}
	if want := `atom2api-credentials-` + view.ID + `-v1.json`; !strings.Contains(singleExportResponse.Header().Get("Content-Disposition"), want) {
		t.Fatalf("single export filename = %q, want to contain %q", singleExportResponse.Header().Get("Content-Disposition"), want)
	}
	var singleBundle accountCredentialBundle
	if err := json.Unmarshal(singleExportResponse.Body.Bytes(), &singleBundle); err != nil {
		t.Fatalf("decode single export: %v", err)
	}
	if len(singleBundle.Accounts) != 1 || singleBundle.Accounts[0].User.ID != "user-export" {
		t.Fatalf("single export bundle = %#v", singleBundle)
	}

	_, target := newTestStore(t)
	importResponse := httptest.NewRecorder()
	(&API{store: target}).HandleAccountCredentialImport(importResponse, httptest.NewRequest(http.MethodPost, "/api/accounts/import", bytes.NewReader(exportResponse.Body.Bytes())))
	if importResponse.Code != http.StatusOK {
		t.Fatalf("import status = %d, body = %s", importResponse.Code, importResponse.Body.String())
	}
	var result accountCredentialImportResponse
	if err := json.Unmarshal(importResponse.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	if result.Imported != 1 || len(result.Errors) != 0 || len(result.Data) != 1 {
		t.Fatalf("import result = %#v", result)
	}
	imported, access, refresh, err := target.Account(result.Data[0].ID)
	if err != nil {
		t.Fatalf("load imported account: %v", err)
	}
	if imported.Name != view.Name || imported.Note != "备用设备" || imported.Enabled || access != "access-export" || refresh != "refresh-export" {
		t.Fatalf("imported account = %#v, access=%q, refresh=%q", imported, access, refresh)
	}

	if _, err := target.UpdateAccount(imported.ID, func(account *Account) error {
		account.RequestCount = 12
		return nil
	}); err != nil {
		t.Fatalf("seed imported usage: %v", err)
	}
	secondResponse := httptest.NewRecorder()
	(&API{store: target}).HandleAccountCredentialImport(secondResponse, httptest.NewRequest(http.MethodPost, "/api/accounts/import", bytes.NewReader(exportResponse.Body.Bytes())))
	if secondResponse.Code != http.StatusOK || len(target.Accounts()) != 1 {
		t.Fatalf("duplicate import status=%d accounts=%d body=%s", secondResponse.Code, len(target.Accounts()), secondResponse.Body.String())
	}
	updated, _, _, err := target.Account(imported.ID)
	if err != nil || updated.RequestCount != 12 {
		t.Fatalf("duplicate import changed usage: account=%#v err=%v", updated, err)
	}
}

func TestAccountCredentialExportRejectsUnknownAccount(t *testing.T) {
	_, store := newTestStore(t)
	request := httptest.NewRequest(http.MethodGet, "/api/accounts/unknown/export", nil)
	request.SetPathValue("id", "unknown")
	response := httptest.NewRecorder()
	(&API{store: store}).HandleAccountCredentialExport(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAccountCredentialImportRejectsInvalidBundle(t *testing.T) {
	_, store := newTestStore(t)
	requestBody := `{"version":1,"accounts":[{"name":"invalid","user":{"id":"user-invalid"},"credentials":{}}]}`
	response := httptest.NewRecorder()
	(&API{store: store}).HandleAccountCredentialImport(response, httptest.NewRequest(http.MethodPost, "/api/accounts/import", bytes.NewBufferString(requestBody)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result accountCredentialImportResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode invalid import: %v", err)
	}
	if result.Imported != 0 || len(result.Errors) != 1 {
		t.Fatalf("invalid import result = %#v", result)
	}
	if len(store.Accounts()) != 0 {
		t.Fatalf("invalid import created accounts: %#v", store.Accounts())
	}
}
