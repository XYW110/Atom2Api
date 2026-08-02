package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountNoteCanBeUpdatedAndSurvivesUpsert(t *testing.T) {
	_, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	api := NewAPI(store, nil, nil, nil, nil)

	request := httptest.NewRequest(http.MethodPatch, "/api/accounts/"+account.ID, strings.NewReader(`{"note":"  主力生产账号  "}`))
	request.SetPathValue("id", account.ID)
	response := httptest.NewRecorder()
	api.HandleAccountUpdate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", response.Code, response.Body.String())
	}

	var updated AccountView
	if err := json.NewDecoder(response.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated account: %v", err)
	}
	if updated.Note != "主力生产账号" {
		t.Fatalf("updated note = %q", updated.Note)
	}

	resynced, err := store.UpsertAccount(Account{
		Name: "Synced Account", Status: "active", Enabled: true,
		User: account.User, Plan: account.Plan, Models: account.Models,
	}, "new-access-token", "new-refresh-token")
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if resynced.Note != "主力生产账号" {
		t.Fatalf("note after upsert = %q", resynced.Note)
	}

	request = httptest.NewRequest(http.MethodPatch, "/api/accounts/"+account.ID, strings.NewReader(`{"note":"   "}`))
	request.SetPathValue("id", account.ID)
	response = httptest.NewRecorder()
	api.HandleAccountUpdate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&updated); err != nil {
		t.Fatalf("decode cleared account: %v", err)
	}
	if updated.Note != "" {
		t.Fatalf("cleared note = %q", updated.Note)
	}
}
