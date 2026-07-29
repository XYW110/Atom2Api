package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	planClaimTriggerManual    = "manual"
	planClaimTriggerScheduled = "scheduled"
)

var errPlanClaimInProgress = errors.New("Coding Plan claim is already in progress for this account")

type codingPlanClaimer interface {
	ClaimAndSyncDetailed(context.Context, string) (CodingPlanClaimOutcome, error)
}

type PlanClaimResult struct {
	Account AccountView  `json:"account"`
	Log     PlanClaimLog `json:"log"`
}

type PlanClaimService struct {
	store   *Store
	claimer codingPlanClaimer
	parser  cron.Parser
	cron    *cron.Cron

	mu      sync.Mutex
	entries map[string]cron.EntryID
	running map[string]struct{}
}

func NewPlanClaimService(store *Store, claimer codingPlanClaimer) *PlanClaimService {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return &PlanClaimService{
		store: store, claimer: claimer, parser: parser,
		cron:    cron.New(cron.WithParser(parser), cron.WithLocation(time.Local)),
		entries: map[string]cron.EntryID{}, running: map[string]struct{}{},
	}
}

func (s *PlanClaimService) Start() error {
	for _, account := range s.store.Accounts() {
		if err := s.Reschedule(account.ID); err != nil {
			return fmt.Errorf("schedule Coding Plan claim for %s: %w", account.ID, err)
		}
	}
	s.cron.Start()
	return nil
}

func (s *PlanClaimService) Close() {
	<-s.cron.Stop().Done()
}

func (s *PlanClaimService) ValidateCron(expression string) error {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return errors.New("Coding Plan claim cron is required when scheduling is enabled")
	}
	if _, err := s.parser.Parse(expression); err != nil {
		return fmt.Errorf("invalid Coding Plan claim cron: %w", err)
	}
	return nil
}

func (s *PlanClaimService) Reschedule(accountID string) error {
	account, _, _, err := s.store.Account(accountID)
	if err != nil {
		return err
	}
	schedule := account.View().ClaimSchedule
	if schedule.Enabled {
		if err := s.ValidateCron(schedule.Cron); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if entryID, exists := s.entries[accountID]; exists {
		s.cron.Remove(entryID)
		delete(s.entries, accountID)
	}
	if !schedule.Enabled {
		return nil
	}
	entryID, err := s.cron.AddFunc(schedule.Cron, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = s.Claim(ctx, accountID, planClaimTriggerScheduled)
	})
	if err != nil {
		return err
	}
	s.entries[accountID] = entryID
	return nil
}

func (s *PlanClaimService) Unschedule(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entryID, exists := s.entries[accountID]; exists {
		s.cron.Remove(entryID)
		delete(s.entries, accountID)
	}
}

func (s *PlanClaimService) Claim(ctx context.Context, accountID, trigger string) (PlanClaimResult, error) {
	if trigger != planClaimTriggerManual && trigger != planClaimTriggerScheduled {
		return PlanClaimResult{}, errors.New("invalid Coding Plan claim trigger")
	}
	account, _, _, err := s.store.Account(accountID)
	if err != nil {
		return PlanClaimResult{}, err
	}
	if !s.begin(accountID) {
		return PlanClaimResult{}, errPlanClaimInProgress
	}
	defer s.finish(accountID)

	schedule := account.View().ClaimSchedule
	claimLog, err := s.store.StartPlanClaimLog(accountID, account.Name, trigger, schedule.Cron)
	if err != nil {
		return PlanClaimResult{}, err
	}
	outcome, claimErr := s.claimer.ClaimAndSyncDetailed(ctx, accountID)
	view := outcome.Account
	status := "success"
	message := outcome.Message
	if message == "" {
		message = "Coding Plan claimed and synchronized"
	}
	planName := outcome.PlanName
	if view.Plan.Plan != nil {
		planName = view.Plan.Plan.PlanName
	}
	if claimErr != nil {
		status = "failed"
		message = claimErr.Error()
	}
	claimLog, logErr := s.store.FinishPlanClaimLog(claimLog.ID, status, planName, message, outcome.Attempts)
	result := PlanClaimResult{Account: view, Log: claimLog}
	if claimErr != nil {
		if logErr != nil {
			return result, fmt.Errorf("%v; record claim log: %w", claimErr, logErr)
		}
		return result, claimErr
	}
	if logErr != nil {
		return result, fmt.Errorf("record claim log: %w", logErr)
	}
	return result, nil
}

func (s *PlanClaimService) begin(accountID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.running[accountID]; exists {
		return false
	}
	s.running[accountID] = struct{}{}
	return true
}

func (s *PlanClaimService) finish(accountID string) {
	s.mu.Lock()
	delete(s.running, accountID)
	s.mu.Unlock()
}

func (s *Store) StartPlanClaimLog(accountID, accountName, trigger, expression string) (PlanClaimLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claimLog := PlanClaimLog{
		ID: randomID("claim"), AccountID: accountID, AccountName: accountName,
		Trigger: trigger, Cron: expression, Status: "running", StartedAt: time.Now().UTC(),
	}
	s.state.PlanClaimLogs = append(s.state.PlanClaimLogs, claimLog)
	if len(s.state.PlanClaimLogs) > maxPlanClaimLogs {
		s.state.PlanClaimLogs = append([]PlanClaimLog(nil), s.state.PlanClaimLogs[len(s.state.PlanClaimLogs)-maxPlanClaimLogs:]...)
	}
	if err := s.saveLocked(); err != nil {
		return PlanClaimLog{}, err
	}
	return claimLog, nil
}

func (s *Store) FinishPlanClaimLog(id, status, planName, message string, attempts []CodingPlanClaimAttempt) (PlanClaimLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.PlanClaimLogs {
		if s.state.PlanClaimLogs[i].ID != id {
			continue
		}
		now := time.Now().UTC()
		s.state.PlanClaimLogs[i].Status = status
		s.state.PlanClaimLogs[i].PlanName = planName
		s.state.PlanClaimLogs[i].Message = message
		s.state.PlanClaimLogs[i].Attempts = append([]CodingPlanClaimAttempt(nil), attempts...)
		s.state.PlanClaimLogs[i].FinishedAt = &now
		if err := s.saveLocked(); err != nil {
			return PlanClaimLog{}, err
		}
		return s.state.PlanClaimLogs[i], nil
	}
	return PlanClaimLog{}, osErrNotExist("Coding Plan claim log")
}

func (s *Store) PlanClaimLogs(accountID string, limit int) []PlanClaimLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	logs := make([]PlanClaimLog, 0, limit)
	for i := len(s.state.PlanClaimLogs) - 1; i >= 0 && len(logs) < limit; i-- {
		claimLog := s.state.PlanClaimLogs[i]
		if accountID == "" || claimLog.AccountID == accountID {
			logs = append(logs, claimLog)
		}
	}
	sort.SliceStable(logs, func(i, j int) bool { return logs[i].StartedAt.After(logs[j].StartedAt) })
	return logs
}
