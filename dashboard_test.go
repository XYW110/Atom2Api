package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDashboardRPMUsesTenMinuteAverageAcrossRanges(t *testing.T) {
	_, store := newTestStore(t)
	now := time.Now().UTC()
	for i := 0; i < 15; i++ {
		if err := store.RecordUsage(UsageRecord{
			ID: fmt.Sprintf("req_recent_%d", i), Timestamp: now.Add(-5 * time.Minute),
			Path: "/v1/responses", Model: "gpt-test", Status: http.StatusOK,
		}); err != nil {
			t.Fatalf("RecordUsage recent request %d: %v", i, err)
		}
	}
	for i := 0; i < 4; i++ {
		if err := store.RecordUsage(UsageRecord{
			ID: fmt.Sprintf("req_old_%d", i), Timestamp: now.Add(-11 * time.Minute),
			Path: "/v1/responses", Model: "gpt-test", Status: http.StatusOK,
		}); err != nil {
			t.Fatalf("RecordUsage old request %d: %v", i, err)
		}
	}

	api := &API{store: store}
	for _, rangeName := range []string{"24h", "7d", "30d"} {
		t.Run(rangeName, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/dashboard?range="+rangeName, nil)
			response := httptest.NewRecorder()
			api.HandleDashboard(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var dashboard dashboardResponse
			if err := json.Unmarshal(response.Body.Bytes(), &dashboard); err != nil {
				t.Fatalf("decode dashboard: %v", err)
			}
			if dashboard.Summary.RPM != 1.5 {
				t.Fatalf("RPM = %v, want 1.5", dashboard.Summary.RPM)
			}
		})
	}
}
