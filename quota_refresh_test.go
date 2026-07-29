package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type stubQuotaAccounts struct {
	accounts []AccountView
}

func (s *stubQuotaAccounts) Accounts() []AccountView {
	return append([]AccountView(nil), s.accounts...)
}

type stubQuotaSyncer struct {
	mu    sync.Mutex
	calls []string
	err   error
}

type blockingQuotaSyncer struct {
	started chan struct{}
}

func (s *blockingQuotaSyncer) Sync(ctx context.Context, accountID string) (AccountView, error) {
	close(s.started)
	<-ctx.Done()
	return AccountView{ID: accountID}, ctx.Err()
}

func (s *stubQuotaSyncer) Sync(_ context.Context, accountID string) (AccountView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, accountID)
	return AccountView{ID: accountID}, s.err
}

func (s *stubQuotaSyncer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func TestAccountQuotaRefreshDeadlineUsesShortestVisibleWindowAndDelay(t *testing.T) {
	lastSync := time.Date(2026, 7, 30, 2, 40, 0, 0, time.UTC)
	account := AccountView{
		LastSyncAt: &lastSync,
		Plan: CodingPlanStatus{RateLimitWindows: []RateLimitWindow{
			{ShowEnable: 1, WindowSizeSeconds: 18000, ResetAt: "2026/7/30 10:42:00", SecondsUntilReset: 120},
			{ShowEnable: 0, WindowSizeSeconds: 60, ResetAt: "ignored", SecondsUntilReset: 10},
			{ShowEnable: 1, WindowSizeSeconds: 3600, ResetAt: "2026/7/30 10:41:00", SecondsUntilReset: 60},
		}},
	}
	key, deadline, ok := accountQuotaRefreshDeadline(account)
	if !ok {
		t.Fatal("quota refresh deadline was not found")
	}
	if want := lastSync.Add(65 * time.Second); !deadline.Equal(want) {
		t.Fatalf("deadline = %v, want %v", deadline, want)
	}
	if key != "2026/7/30 10:41:00|1785379260" {
		t.Fatalf("reset key = %q", key)
	}
}

func TestAccountQuotaRefreshDeadlineFallsBackToCurrentUsage(t *testing.T) {
	lastSync := time.Date(2026, 7, 30, 2, 40, 0, 0, time.UTC)
	account := AccountView{
		LastSyncAt: &lastSync,
		Plan:       CodingPlanStatus{CurrentUsage: &UsageInfo{ResetAt: "2026/7/30 10:40:30", SecondsUntilReset: 30}},
	}
	_, deadline, ok := accountQuotaRefreshDeadline(account)
	if !ok || !deadline.Equal(lastSync.Add(35*time.Second)) {
		t.Fatalf("deadline = %v, ok = %v", deadline, ok)
	}
}

func TestQuotaRefreshDeadlinePrefersReturnedResetTime(t *testing.T) {
	lastSync := time.Date(2026, 7, 30, 2, 40, 0, 0, time.UTC)
	_, deadline, ok := quotaRefreshDeadline(&lastSync, "2026/7/30 10:42:00", 30)
	if !ok {
		t.Fatal("quota refresh deadline was not found")
	}
	if want := time.Date(2026, 7, 30, 2, 42, 5, 0, time.UTC); !deadline.Equal(want) {
		t.Fatalf("deadline = %v, want %v", deadline, want)
	}
}

func TestQuotaRefreshDeadlineRejectsMissingResetData(t *testing.T) {
	lastSync := time.Date(2026, 7, 30, 2, 40, 0, 0, time.UTC)
	if _, _, ok := quotaRefreshDeadline(&lastSync, "", 0); ok {
		t.Fatal("missing reset data produced a refresh deadline")
	}
}

func TestQuotaRefreshServiceRefreshesAtDeadlineAndRateLimitsRetries(t *testing.T) {
	lastSync := time.Date(2026, 7, 30, 2, 40, 0, 0, time.UTC)
	accounts := &stubQuotaAccounts{accounts: []AccountView{{
		ID: "account-1", LastSyncAt: &lastSync,
		Plan: CodingPlanStatus{RateLimitWindows: []RateLimitWindow{{
			ShowEnable: 1, WindowSizeSeconds: 3600, ResetAt: "2026/7/30 10:40:10", SecondsUntilReset: 10,
		}}},
	}}}
	syncer := &stubQuotaSyncer{err: errors.New("upstream not ready")}
	service := NewQuotaRefreshService(accounts, syncer)

	service.refreshDue(context.Background(), lastSync.Add(14*time.Second))
	if syncer.callCount() != 0 {
		t.Fatalf("calls before delayed deadline = %d", syncer.callCount())
	}
	service.refreshDue(context.Background(), lastSync.Add(15*time.Second))
	service.refreshDue(context.Background(), lastSync.Add(20*time.Second))
	if syncer.callCount() != 1 {
		t.Fatalf("calls during retry interval = %d", syncer.callCount())
	}
	service.refreshDue(context.Background(), lastSync.Add(30*time.Second))
	if syncer.callCount() != 2 {
		t.Fatalf("calls after retry interval = %d", syncer.callCount())
	}
}

func TestQuotaRefreshServiceCloseCancelsInFlightRefresh(t *testing.T) {
	resetAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	accounts := &stubQuotaAccounts{accounts: []AccountView{{
		ID: "account-1",
		Plan: CodingPlanStatus{RateLimitWindows: []RateLimitWindow{{
			ShowEnable: 1, WindowSizeSeconds: 3600, ResetAt: resetAt,
		}}},
	}}}
	syncer := &blockingQuotaSyncer{started: make(chan struct{})}
	service := NewQuotaRefreshService(accounts, syncer)
	service.Start()

	select {
	case <-syncer.started:
	case <-time.After(time.Second):
		t.Fatal("automatic refresh did not start")
	}
	closed := make(chan struct{})
	go func() {
		service.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("automatic refresh did not stop after cancellation")
	}
}

func TestParseCodingPlanResetTimeUsesChinaTimezone(t *testing.T) {
	parsed, ok := parseCodingPlanResetTime("2026/7/30 10:40:05")
	if !ok {
		t.Fatal("reset time was not parsed")
	}
	if want := time.Date(2026, 7, 30, 2, 40, 5, 0, time.UTC); !parsed.Equal(want) {
		t.Fatalf("parsed reset = %v, want %v", parsed, want)
	}
}
