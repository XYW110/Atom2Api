package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	quotaRefreshDelay         = 5 * time.Second
	quotaRefreshPollInterval  = time.Second
	quotaRefreshRetryInterval = 15 * time.Second
)

var codingPlanResetLocation = time.FixedZone("UTC+8", 8*60*60)

type quotaAccountSource interface {
	Accounts() []AccountView
}

type quotaAccountSyncer interface {
	Sync(context.Context, string) (AccountView, error)
}

type quotaRefreshAttempt struct {
	resetKey string
	retryAt  time.Time
}

type QuotaRefreshService struct {
	accounts quotaAccountSource
	syncer   quotaAccountSyncer

	pollInterval  time.Duration
	retryInterval time.Duration
	attempts      map[string]quotaRefreshAttempt
	ctx           context.Context
	cancel        context.CancelFunc
	startOnce     sync.Once
	stopOnce      sync.Once
	wg            sync.WaitGroup
}

func NewQuotaRefreshService(accounts quotaAccountSource, syncer quotaAccountSyncer) *QuotaRefreshService {
	ctx, cancel := context.WithCancel(context.Background())
	return &QuotaRefreshService{
		accounts: accounts, syncer: syncer,
		pollInterval: quotaRefreshPollInterval, retryInterval: quotaRefreshRetryInterval,
		attempts: make(map[string]quotaRefreshAttempt), ctx: ctx, cancel: cancel,
	}
}

func (s *QuotaRefreshService) Start() {
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.run()
	})
}

func (s *QuotaRefreshService) Close() {
	s.stopOnce.Do(s.cancel)
	s.wg.Wait()
}

func (s *QuotaRefreshService) run() {
	defer s.wg.Done()
	s.refreshDue(s.ctx, time.Now())
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.refreshDue(s.ctx, now)
		}
	}
}

func (s *QuotaRefreshService) refreshDue(ctx context.Context, now time.Time) {
	accounts := s.accounts.Accounts()
	accountIDs := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		accountIDs[account.ID] = struct{}{}
	}
	for accountID := range s.attempts {
		if _, exists := accountIDs[accountID]; !exists {
			delete(s.attempts, accountID)
		}
	}

	var refreshes sync.WaitGroup
	for _, account := range accounts {
		resetKey, dueAt, ok := accountQuotaRefreshDeadline(account)
		if !ok || now.Before(dueAt) {
			continue
		}
		if attempt, exists := s.attempts[account.ID]; exists && attempt.resetKey == resetKey && now.Before(attempt.retryAt) {
			continue
		}
		s.attempts[account.ID] = quotaRefreshAttempt{resetKey: resetKey, retryAt: now.Add(s.retryInterval)}
		refreshes.Add(1)
		go func(accountID string) {
			defer refreshes.Done()
			if _, err := s.syncer.Sync(ctx, accountID); err != nil && ctx.Err() == nil {
				log.Printf("automatic quota refresh for account %s: %v", accountID, err)
			}
		}(account.ID)
	}
	refreshes.Wait()
}

func accountQuotaRefreshDeadline(account AccountView) (string, time.Time, bool) {
	var selected *RateLimitWindow
	for index := range account.Plan.RateLimitWindows {
		window := &account.Plan.RateLimitWindows[index]
		if window.ShowEnable != 1 {
			continue
		}
		if selected == nil || window.WindowSizeSeconds < selected.WindowSizeSeconds {
			selected = window
		}
	}
	if selected != nil {
		return quotaRefreshDeadline(account.LastSyncAt, selected.ResetAt, selected.SecondsUntilReset)
	}
	if account.Plan.CurrentUsage != nil {
		usage := account.Plan.CurrentUsage
		return quotaRefreshDeadline(account.LastSyncAt, usage.ResetAt, usage.SecondsUntilReset)
	}
	return "", time.Time{}, false
}

func quotaRefreshDeadline(lastSyncAt *time.Time, resetAt string, secondsUntilReset int64) (string, time.Time, bool) {
	var resetTime time.Time
	if parsed, ok := parseCodingPlanResetTime(resetAt); ok {
		resetTime = parsed
	} else if lastSyncAt != nil && secondsUntilReset > 0 {
		resetTime = lastSyncAt.Add(time.Duration(secondsUntilReset) * time.Second)
	} else {
		return "", time.Time{}, false
	}
	resetKey := strings.TrimSpace(resetAt)
	if resetKey == "" {
		resetKey = fmt.Sprintf("%d", resetTime.Unix())
	}
	return fmt.Sprintf("%s|%d", resetKey, resetTime.Unix()), resetTime.Add(quotaRefreshDelay), true
}

func parseCodingPlanResetTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	for _, layout := range []string{"2006/1/2 15:04:05", "2006-1-2 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, codingPlanResetLocation); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
