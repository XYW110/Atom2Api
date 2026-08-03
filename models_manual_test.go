package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManualModelAppearsInCatalogAndRoutesThroughAvailableAccount(t *testing.T) {
	_, store := newTestStore(t)
	account := addTestAccount(t, store, "https://example.com")
	if err := store.SetModelSetting(ModelSetting{
		Upstream: "manual-model", Alias: "custom-model", Enabled: true, Manual: true, ResponsesChatCompat: true,
	}); err != nil {
		t.Fatalf("SetModelSetting: %v", err)
	}

	router := NewModelRouter(store)
	var manual *ModelView
	for _, model := range router.Catalog() {
		if model.Upstream == "manual-model" {
			copy := model
			manual = &copy
			break
		}
	}
	if manual == nil || !manual.Manual || manual.Alias != "custom-model" || manual.AccountCount != 1 || !manual.ResponsesChatCompat {
		t.Fatalf("manual catalog model = %#v", manual)
	}
	if manual.BaseURL != defaultGatewayURL || manual.ProviderType != "openai" {
		t.Fatalf("manual model metadata = %#v", manual)
	}

	route, err := router.Resolve("custom-model", APIKey{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if route.Upstream != "manual-model" || route.Account.ID != account.ID || route.Model.DisplayModelName != "manual-model" || route.Model.BaseURL != defaultGatewayURL || !route.ResponsesChatCompat {
		t.Fatalf("manual route = %#v", route)
	}
}

func TestManualModelHandlersCreateUpdateAndDelete(t *testing.T) {
	_, store := newTestStore(t)
	router := NewModelRouter(store)
	api := NewAPI(store, nil, nil, router, nil)

	createRequest := httptest.NewRequest(http.MethodPost, "/api/models", strings.NewReader(`{"upstream":"manual-model","alias":"custom-model"}`))
	createResponse := httptest.NewRecorder()
	api.HandleCreateModel(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var created ModelView
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil || !created.Manual || created.Alias != "custom-model" {
		t.Fatalf("created model = %#v, error = %v", created, err)
	}
	if created.Accounts == nil || created.Plans == nil {
		t.Fatalf("created model arrays must not be nil: %#v", created)
	}

	updateRequest := httptest.NewRequest(http.MethodPut, "/api/models/settings", strings.NewReader(`{"upstream":"manual-model","alias":"renamed-model","enabled":false,"responses_chat_compat":true}`))
	updateResponse := httptest.NewRecorder()
	api.HandleModelSetting(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	setting := store.ModelSettings()["manual-model"]
	if !setting.Manual || setting.Alias != "renamed-model" || setting.Enabled || !setting.ResponsesChatCompat {
		t.Fatalf("updated setting = %#v", setting)
	}

	duplicateRequest := httptest.NewRequest(http.MethodPost, "/api/models", strings.NewReader(`{"upstream":"another-model","alias":"renamed-model"}`))
	duplicateResponse := httptest.NewRecorder()
	api.HandleCreateModel(duplicateResponse, duplicateRequest)
	if duplicateResponse.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, body = %s", duplicateResponse.Code, duplicateResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/models/settings", strings.NewReader(`{"upstream":"manual-model"}`))
	deleteResponse := httptest.NewRecorder()
	api.HandleDeleteModel(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, exists := store.ModelSettings()["manual-model"]; exists {
		t.Fatal("manual model setting still exists after deletion")
	}
}
