package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAuditCleanupHandlersClearDetailsAndDeleteRecords(t *testing.T) {
	_, store := newTestStore(t)
	now := time.Now().UTC()
	records := []UsageRecord{
		{
			ID: "req_old_detail", Timestamp: now.AddDate(0, 0, -40), Method: http.MethodPost,
			Path: "/v1/chat/completions", Model: "old-model", Status: http.StatusBadGateway,
			LatencyMS: 42, RetryCount: 1, Error: "old upstream error",
			RequestBody: `{"secret":"old-request"}`, ResponseBody: `{"secret":"old-response"}`,
			RequestHeaders:  map[string][]string{"X-Trace": {"old-request-header"}},
			ResponseHeaders: map[string][]string{"X-Trace": {"old-response-header"}},
			Attempts:        []RequestAttempt{{Attempt: 1, Status: http.StatusBadGateway, Error: "old attempt error", ResponseBody: "old-attempt-body"}},
		},
		{
			ID: "req_old_summary", Timestamp: now.AddDate(0, 0, -35), Method: http.MethodPost,
			Path: "/v1/responses", Model: "summary-model", Status: http.StatusOK,
		},
		{
			ID: "req_recent_detail", Timestamp: now.AddDate(0, 0, -5), Method: http.MethodPost,
			Path: "/v1/responses", Model: "recent-model", Status: http.StatusOK,
			RequestBody: `{"secret":"recent-request"}`, ResponseBody: `{"secret":"recent-response"}`,
		},
	}
	for _, record := range records {
		if err := store.RecordUsage(record); err != nil {
			t.Fatalf("RecordUsage(%s): %v", record.ID, err)
		}
	}

	api := &API{store: store}
	detailRequest := httptest.NewRequest(http.MethodPost, "/api/audit/cleanup/details", strings.NewReader(`{"days":30}`))
	detailResponse := httptest.NewRecorder()
	api.HandleAuditDetailCleanup(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail cleanup status = %d, body = %s", detailResponse.Code, detailResponse.Body.String())
	}
	var detailResult auditCleanupResponse
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detailResult); err != nil {
		t.Fatalf("decode detail cleanup: %v", err)
	}
	if detailResult.Affected != 1 {
		t.Fatalf("detail cleanup affected = %d, want 1", detailResult.Affected)
	}

	stored := store.UsageRecords()
	if len(stored) != 3 {
		t.Fatalf("usage records after detail cleanup = %#v", stored)
	}
	if stored[0].Model != "old-model" || stored[0].Status != http.StatusBadGateway || stored[0].RetryCount != 1 {
		t.Fatalf("old audit summary changed = %#v", stored[0])
	}
	if usageRecordHasDetails(stored[0]) {
		t.Fatalf("old audit details remain = %#v", stored[0])
	}
	if !usageRecordHasDetails(stored[2]) {
		t.Fatalf("recent audit details were cleared = %#v", stored[2])
	}
	data := storedUsageData(t, store)
	if bytes.Contains(data, []byte("old-request")) || bytes.Contains(data, []byte("old-attempt-body")) {
		t.Fatal("SQLite still contains cleared audit details")
	}
	if !bytes.Contains(data, []byte("recent-request")) {
		t.Fatal("SQLite lost recent audit details")
	}

	recordRequest := httptest.NewRequest(http.MethodPost, "/api/audit/cleanup/records", strings.NewReader(`{"days":30}`))
	recordResponse := httptest.NewRecorder()
	api.HandleAuditRecordCleanup(recordResponse, recordRequest)
	if recordResponse.Code != http.StatusOK {
		t.Fatalf("record cleanup status = %d, body = %s", recordResponse.Code, recordResponse.Body.String())
	}
	var recordResult auditCleanupResponse
	if err := json.Unmarshal(recordResponse.Body.Bytes(), &recordResult); err != nil {
		t.Fatalf("decode record cleanup: %v", err)
	}
	if recordResult.Affected != 2 {
		t.Fatalf("record cleanup affected = %d, want 2", recordResult.Affected)
	}
	remaining := store.UsageRecords()
	if len(remaining) != 1 || remaining[0].ID != "req_recent_detail" {
		t.Fatalf("usage records after record cleanup = %#v", remaining)
	}
	data = storedUsageData(t, store)
	if bytes.Contains(data, []byte("req_old_detail")) || bytes.Contains(data, []byte("req_old_summary")) {
		t.Fatal("SQLite still contains deleted audit records")
	}
}

func TestAuditCleanupHandlersRejectInvalidDays(t *testing.T) {
	_, store := newTestStore(t)
	api := &API{store: store}
	for _, days := range []int{0, maxAuditRetention + 1} {
		request := httptest.NewRequest(http.MethodPost, "/api/audit/cleanup/records", strings.NewReader(`{"days":`+strconv.Itoa(days)+`}`))
		response := httptest.NewRecorder()
		api.HandleAuditRecordCleanup(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("days %d status = %d, want %d", days, response.Code, http.StatusBadRequest)
		}
	}
}
