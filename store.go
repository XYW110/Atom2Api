package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	stateVersion         = 1
	defaultPlanClaimCron = "0 10 * * *"
	maxPlanClaimLogs     = 1000
)

type UserInfo struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name,omitempty"`
	Email     string `json:"email,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type OAuthCredentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type PlanInfo struct {
	PlanName      string `json:"plan_name"`
	Status        int    `json:"status"`
	ClaimedAt     string `json:"claimed_at"`
	ExpiresAt     string `json:"expires_at"`
	RemainingDays int    `json:"remaining_days"`
	TotalDays     int    `json:"total_days"`
	ApplyID       int64  `json:"apply_id"`
}

type UsageInfo struct {
	Placeholder       bool    `json:"placeholder"`
	WindowTokenLimit  int64   `json:"window_token_limit"`
	WindowTokensUsed  int64   `json:"window_tokens_used"`
	UsagePercent      float64 `json:"usage_percent"`
	WindowHours       int     `json:"window_hours"`
	ResetAt           string  `json:"reset_at"`
	ResetAtDisplay    string  `json:"reset_at_display"`
	SecondsUntilReset int64   `json:"seconds_until_reset"`
	ResetLabel        string  `json:"reset_label"`
	UsageStatusDesc   string  `json:"usage_status_desc"`
}

type RateLimitWindow struct {
	RuleIndex         int     `json:"rule_index"`
	ShowEnable        int     `json:"show_enable"`
	WindowSizeSeconds int64   `json:"window_size_seconds"`
	WindowHours       int     `json:"window_hours"`
	CallLimit         int64   `json:"call_limit"`
	CallsUsed         int64   `json:"calls_used"`
	UsagePercent      float64 `json:"usage_percent"`
	QuotaExhausted    bool    `json:"quota_exhausted"`
	ResetAt           string  `json:"reset_at"`
	ResetAtDisplay    string  `json:"reset_at_display"`
	SecondsUntilReset int64   `json:"seconds_until_reset"`
	ResetLabel        string  `json:"reset_label"`
	UsageStatusDesc   string  `json:"usage_status_desc"`
}

type CodingPlanStatus struct {
	Plan                 *PlanInfo         `json:"codingplan_free,omitempty"`
	CurrentUsage         *UsageInfo        `json:"current_usage,omitempty"`
	AuditStatus          int               `json:"audit_status"`
	ExpiresAt            string            `json:"expires_at,omitempty"`
	WindowQuotaExhausted bool              `json:"window_quota_exhausted"`
	WindowQuotaHint      string            `json:"window_quota_hint,omitempty"`
	RateLimitWindows     []RateLimitWindow `json:"rate_limit_windows,omitempty"`
}

type CodingPlanModel struct {
	ID                  int64  `json:"id"`
	DisplayModelName    string `json:"display_model_name"`
	BaseURL             string `json:"base_url,omitempty"`
	ProviderType        string `json:"type,omitempty"`
	ContextWindow       int    `json:"context_window,omitempty"`
	PlanAvailable       bool   `json:"plan_available"`
	IsInfinity          int    `json:"is_infinity,omitempty"`
	IsAtomcodeExclusive int    `json:"is_atomcode_exclusive,omitempty"`
	CapableModel        *int64 `json:"capable_model,omitempty"`
}

type ProviderUsageRow struct {
	Date        string           `json:"date"`
	ModelCounts map[string]int64 `json:"model_counts"`
	ModelTokens map[string]int64 `json:"model_tokens"`
	TotalCounts int64            `json:"total_counts"`
	TotalTokens int64            `json:"total_tokens"`
}

type ProviderUsage struct {
	Days        int                `json:"days"`
	StartDate   string             `json:"start_date"`
	EndDate     string             `json:"end_date"`
	Models      []string           `json:"models"`
	Rows        []ProviderUsageRow `json:"rows"`
	ModelTokens map[string]int64   `json:"model_tokens"`
	ModelCounts map[string]int64   `json:"model_counts"`
	TotalTokens int64              `json:"total_tokens"`
	TotalCounts int64              `json:"total_counts"`
}

type PlanClaimSchedule struct {
	Enabled bool   `json:"enabled"`
	Cron    string `json:"cron"`
}

func defaultPlanClaimSchedule() PlanClaimSchedule {
	return PlanClaimSchedule{Enabled: true, Cron: defaultPlanClaimCron}
}

type CodingPlanClaimAttempt struct {
	PlanType   string `json:"plan_type"`
	HTTPStatus int    `json:"http_status"`
	Response   string `json:"response"`
	Success    bool   `json:"success"`
	Duplicate  bool   `json:"duplicate"`
	Message    string `json:"message,omitempty"`
}

type PlanClaimLog struct {
	ID          string                   `json:"id"`
	AccountID   string                   `json:"account_id"`
	AccountName string                   `json:"account_name"`
	Trigger     string                   `json:"trigger"`
	Cron        string                   `json:"cron,omitempty"`
	Status      string                   `json:"status"`
	PlanName    string                   `json:"plan_name,omitempty"`
	Message     string                   `json:"message,omitempty"`
	Attempts    []CodingPlanClaimAttempt `json:"attempts,omitempty"`
	StartedAt   time.Time                `json:"started_at"`
	FinishedAt  *time.Time               `json:"finished_at,omitempty"`
}

type Account struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	Note           string             `json:"note,omitempty"`
	Status         string             `json:"status"`
	Enabled        bool               `json:"enabled"`
	Credentials    OAuthCredentials   `json:"credentials"`
	User           UserInfo           `json:"user"`
	Plan           CodingPlanStatus   `json:"plan"`
	Models         []CodingPlanModel  `json:"models"`
	ProviderUsage  *ProviderUsage     `json:"provider_usage,omitempty"`
	ClaimSchedule  *PlanClaimSchedule `json:"plan_claim_schedule,omitempty"`
	RequestCount   int64              `json:"request_count"`
	InputTokens    int64              `json:"input_tokens"`
	OutputTokens   int64              `json:"output_tokens"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	LastSyncAt     *time.Time         `json:"last_sync_at,omitempty"`
	LastUsedAt     *time.Time         `json:"last_used_at,omitempty"`
	LastError      string             `json:"last_error,omitempty"`
	ConsecutiveErr int                `json:"consecutive_errors,omitempty"`
}

type AccountView struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Note           string            `json:"note"`
	Status         string            `json:"status"`
	Enabled        bool              `json:"enabled"`
	User           UserInfo          `json:"user"`
	Plan           CodingPlanStatus  `json:"plan"`
	Models         []CodingPlanModel `json:"models"`
	ProviderUsage  *ProviderUsage    `json:"provider_usage,omitempty"`
	ClaimSchedule  PlanClaimSchedule `json:"plan_claim_schedule"`
	RequestCount   int64             `json:"request_count"`
	InputTokens    int64             `json:"input_tokens"`
	OutputTokens   int64             `json:"output_tokens"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	LastSyncAt     *time.Time        `json:"last_sync_at,omitempty"`
	LastUsedAt     *time.Time        `json:"last_used_at,omitempty"`
	LastError      string            `json:"last_error,omitempty"`
	TokenExpiresAt *time.Time        `json:"token_expires_at,omitempty"`
}

func (a Account) View() AccountView {
	claimSchedule := defaultPlanClaimSchedule()
	if a.ClaimSchedule != nil {
		claimSchedule = *a.ClaimSchedule
	}
	view := AccountView{
		ID: a.ID, Name: a.Name, Note: a.Note, Status: a.Status, Enabled: a.Enabled, User: a.User,
		Plan: a.Plan, Models: append([]CodingPlanModel(nil), a.Models...), ProviderUsage: a.ProviderUsage,
		ClaimSchedule: claimSchedule,
		RequestCount:  a.RequestCount, InputTokens: a.InputTokens, OutputTokens: a.OutputTokens,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, LastSyncAt: a.LastSyncAt,
		LastUsedAt: a.LastUsedAt, LastError: a.LastError,
	}
	if a.Credentials.ExpiresIn > 0 && !a.Credentials.CreatedAt.IsZero() {
		expires := a.Credentials.CreatedAt.Add(time.Duration(a.Credentials.ExpiresIn) * time.Second)
		view.TokenExpiresAt = &expires
	}
	return view
}

type APIKey struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Prefix           string     `json:"prefix"`
	Hash             string     `json:"hash"`
	Enabled          bool       `json:"enabled"`
	AllowedModels    []string   `json:"allowed_models,omitempty"`
	RPMLimit         int        `json:"rpm_limit"`
	ConcurrencyLimit int        `json:"concurrency_limit"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	RequestCount     int64      `json:"request_count"`
	InputTokens      int64      `json:"input_tokens"`
	OutputTokens     int64      `json:"output_tokens"`
}

type APIKeyView struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Prefix           string     `json:"prefix"`
	Enabled          bool       `json:"enabled"`
	AllowedModels    []string   `json:"allowed_models,omitempty"`
	RPMLimit         int        `json:"rpm_limit"`
	ConcurrencyLimit int        `json:"concurrency_limit"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	RequestCount     int64      `json:"request_count"`
	InputTokens      int64      `json:"input_tokens"`
	OutputTokens     int64      `json:"output_tokens"`
}

func (key APIKey) View() APIKeyView {
	return APIKeyView{
		ID: key.ID, Name: key.Name, Prefix: key.Prefix, Enabled: key.Enabled,
		AllowedModels: append([]string(nil), key.AllowedModels...), RPMLimit: key.RPMLimit,
		ConcurrencyLimit: key.ConcurrencyLimit, CreatedAt: key.CreatedAt,
		ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt, RequestCount: key.RequestCount,
		InputTokens: key.InputTokens, OutputTokens: key.OutputTokens,
	}
}

type ModelSetting struct {
	Upstream string `json:"upstream"`
	Alias    string `json:"alias"`
	Enabled  bool   `json:"enabled"`
	Manual   bool   `json:"manual,omitempty"`
}

type UsageRecord struct {
	ID              string              `json:"id"`
	Timestamp       time.Time           `json:"timestamp"`
	Method          string              `json:"method,omitempty"`
	Path            string              `json:"path"`
	Model           string              `json:"model"`
	UpstreamModel   string              `json:"upstream_model"`
	AccountID       string              `json:"account_id,omitempty"`
	APIKeyID        string              `json:"api_key_id,omitempty"`
	Status          int                 `json:"status"`
	LatencyMS       int64               `json:"latency_ms"`
	InputTokens     int64               `json:"input_tokens"`
	OutputTokens    int64               `json:"output_tokens"`
	CachedTokens    int64               `json:"cached_tokens,omitempty"`
	ReasoningTokens int64               `json:"reasoning_tokens,omitempty"`
	Streaming       bool                `json:"streaming"`
	Error           string              `json:"error,omitempty"`
	RequestBody     string              `json:"request_body,omitempty"`
	ResponseBody    string              `json:"response_body,omitempty"`
	RequestHeaders  map[string][]string `json:"request_headers,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
}

type persistedState struct {
	Version       int                     `json:"version"`
	Accounts      []Account               `json:"accounts"`
	APIKeys       []APIKey                `json:"api_keys"`
	ModelSettings map[string]ModelSetting `json:"model_settings"`
	PlanClaimLogs []PlanClaimLog          `json:"plan_claim_logs,omitempty"`
	Usage         []UsageRecord           `json:"-"`
}

type Store struct {
	path       string
	legacyPath string
	usagePath  string
	db         *sql.DB
	config     *ConfigManager
	mu         sync.RWMutex
	state      persistedState
}

func NewStore(path string, config *ConfigManager) (*Store, error) {
	return newSQLiteStore(path, config)
}

func (s *Store) encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := encryptionKey(s.config.Snapshot().EncryptionKey)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), []byte("atom2api-oauth-v1"))
	return "v1:" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Store) decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, "v1:") {
		return "", errors.New("unsupported credential encoding")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, "v1:"))
	if err != nil {
		return "", errors.New("invalid encrypted credential")
	}
	key, err := encryptionKey(s.config.Snapshot().EncryptionKey)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte("atom2api-oauth-v1"))
	if err != nil {
		return "", errors.New("cannot decrypt credential; check encryption_key")
	}
	return string(plain), nil
}

func (s *Store) UpsertAccount(account Account, accessToken, refreshToken string) (AccountView, error) {
	encryptedAccess, err := s.encrypt(accessToken)
	if err != nil {
		return AccountView{}, err
	}
	encryptedRefresh, err := s.encrypt(refreshToken)
	if err != nil {
		return AccountView{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i := range s.state.Accounts {
		if s.state.Accounts[i].User.ID == account.User.ID || (account.ID != "" && s.state.Accounts[i].ID == account.ID) {
			index = i
			break
		}
	}
	account.Credentials.AccessToken = encryptedAccess
	account.Credentials.RefreshToken = encryptedRefresh
	account.UpdatedAt = now
	if index >= 0 {
		existing := s.state.Accounts[index]
		account.ID = existing.ID
		account.CreatedAt = existing.CreatedAt
		account.RequestCount = existing.RequestCount
		account.InputTokens = existing.InputTokens
		account.OutputTokens = existing.OutputTokens
		account.LastUsedAt = existing.LastUsedAt
		account.ClaimSchedule = existing.ClaimSchedule
		account.Note = existing.Note
		if strings.TrimSpace(account.Name) == "" {
			account.Name = existing.Name
		}
		s.state.Accounts[index] = account
	} else {
		if account.ID == "" {
			account.ID = randomID("acc")
		}
		if strings.TrimSpace(account.Name) == "" {
			account.Name = account.User.Name
			if account.Name == "" {
				account.Name = account.User.Username
			}
		}
		account.CreatedAt = now
		if account.ClaimSchedule == nil {
			claimSchedule := defaultPlanClaimSchedule()
			account.ClaimSchedule = &claimSchedule
		}
		s.state.Accounts = append(s.state.Accounts, account)
		index = len(s.state.Accounts) - 1
	}
	if err := s.saveLocked(); err != nil {
		return AccountView{}, err
	}
	return s.state.Accounts[index].View(), nil
}

func (s *Store) Accounts() []AccountView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	views := make([]AccountView, 0, len(s.state.Accounts))
	for _, account := range s.state.Accounts {
		views = append(views, account.View())
	}
	sort.Slice(views, func(i, j int) bool { return views[i].CreatedAt.Before(views[j].CreatedAt) })
	return views
}

func (s *Store) Account(id string) (Account, string, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.state.Accounts {
		if account.ID != id {
			continue
		}
		access, err := s.decrypt(account.Credentials.AccessToken)
		if err != nil {
			return Account{}, "", "", err
		}
		refresh, err := s.decrypt(account.Credentials.RefreshToken)
		if err != nil {
			return Account{}, "", "", err
		}
		return account, access, refresh, nil
	}
	return Account{}, "", "", os.ErrNotExist
}

func (s *Store) UpdateAccount(id string, update func(*Account) error) (AccountView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Accounts {
		if s.state.Accounts[i].ID != id {
			continue
		}
		if err := update(&s.state.Accounts[i]); err != nil {
			return AccountView{}, err
		}
		s.state.Accounts[i].UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(); err != nil {
			return AccountView{}, err
		}
		return s.state.Accounts[i].View(), nil
	}
	return AccountView{}, os.ErrNotExist
}

func (s *Store) UpdateAccountCredentials(id, accessToken, refreshToken string, expiresIn int64, createdAt time.Time) error {
	access, err := s.encrypt(accessToken)
	if err != nil {
		return err
	}
	refresh, err := s.encrypt(refreshToken)
	if err != nil {
		return err
	}
	_, err = s.UpdateAccount(id, func(account *Account) error {
		account.Credentials.AccessToken = access
		if refresh != "" {
			account.Credentials.RefreshToken = refresh
		}
		account.Credentials.ExpiresIn = expiresIn
		account.Credentials.CreatedAt = createdAt
		return nil
	})
	return err
}

func (s *Store) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Accounts {
		if s.state.Accounts[i].ID == id {
			s.state.Accounts = append(s.state.Accounts[:i], s.state.Accounts[i+1:]...)
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) CreateAPIKey(name string, allowedModels []string, expiresAt *time.Time) (APIKeyView, string, error) {
	return s.CreateAPIKeyWithLimits(name, allowedModels, expiresAt, 0, 0)
}

func (s *Store) CreateAPIKeyWithLimits(name string, allowedModels []string, expiresAt *time.Time, rpmLimit, concurrencyLimit int) (APIKeyView, string, error) {
	if rpmLimit < 0 || concurrencyLimit < 0 {
		return APIKeyView{}, "", errors.New("API key limits cannot be negative")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return APIKeyView{}, "", err
	}
	secret := "sk-atom2-" + base64.RawURLEncoding.EncodeToString(random)
	digest := sha256.Sum256([]byte(secret))
	now := time.Now().UTC()
	key := APIKey{
		ID: randomID("key"), Name: strings.TrimSpace(name), Prefix: secret[:18] + "...",
		Hash: hex.EncodeToString(digest[:]), Enabled: true, AllowedModels: uniqueStrings(allowedModels),
		RPMLimit: rpmLimit, ConcurrencyLimit: concurrencyLimit, CreatedAt: now, ExpiresAt: expiresAt,
	}
	if key.Name == "" {
		key.Name = "API Key"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.APIKeys = append(s.state.APIKeys, key)
	if err := s.saveLocked(); err != nil {
		return APIKeyView{}, "", err
	}
	return key.View(), secret, nil
}

func (s *Store) APIKeys() []APIKeyView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	views := make([]APIKeyView, 0, len(s.state.APIKeys))
	for _, key := range s.state.APIKeys {
		views = append(views, key.View())
	}
	sort.Slice(views, func(i, j int) bool { return views[i].CreatedAt.After(views[j].CreatedAt) })
	return views
}

func (s *Store) AuthenticateAPIKey(secret string) (APIKey, bool) {
	digest := sha256.Sum256([]byte(secret))
	digestHex := hex.EncodeToString(digest[:])
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range s.state.APIKeys {
		if subtle.ConstantTimeCompare([]byte(key.Hash), []byte(digestHex)) != 1 {
			continue
		}
		if !key.Enabled || (key.ExpiresAt != nil && now.After(*key.ExpiresAt)) {
			return APIKey{}, false
		}
		return key, true
	}
	return APIKey{}, false
}

func (s *Store) UpdateAPIKey(id string, name *string, enabled *bool, allowedModels *[]string, expiresAt **time.Time) (APIKeyView, error) {
	return s.UpdateAPIKeyWithLimits(id, name, enabled, allowedModels, expiresAt, nil, nil)
}

func (s *Store) UpdateAPIKeyWithLimits(id string, name *string, enabled *bool, allowedModels *[]string, expiresAt **time.Time, rpmLimit, concurrencyLimit *int) (APIKeyView, error) {
	if (rpmLimit != nil && *rpmLimit < 0) || (concurrencyLimit != nil && *concurrencyLimit < 0) {
		return APIKeyView{}, errors.New("API key limits cannot be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.APIKeys {
		key := &s.state.APIKeys[i]
		if key.ID != id {
			continue
		}
		if name != nil && strings.TrimSpace(*name) != "" {
			key.Name = strings.TrimSpace(*name)
		}
		if enabled != nil {
			key.Enabled = *enabled
		}
		if allowedModels != nil {
			key.AllowedModels = uniqueStrings(*allowedModels)
		}
		if rpmLimit != nil {
			key.RPMLimit = *rpmLimit
		}
		if concurrencyLimit != nil {
			key.ConcurrencyLimit = *concurrencyLimit
		}
		if expiresAt != nil {
			key.ExpiresAt = *expiresAt
		}
		if err := s.saveLocked(); err != nil {
			return APIKeyView{}, err
		}
		return key.View(), nil
	}
	return APIKeyView{}, os.ErrNotExist
}

func (s *Store) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.APIKeys {
		if s.state.APIKeys[i].ID == id {
			s.state.APIKeys = append(s.state.APIKeys[:i], s.state.APIKeys[i+1:]...)
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) ModelSettings() map[string]ModelSetting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings := make(map[string]ModelSetting, len(s.state.ModelSettings))
	for key, value := range s.state.ModelSettings {
		settings[key] = value
	}
	return settings
}

func (s *Store) SetModelSetting(setting ModelSetting) error {
	setting.Upstream = strings.TrimSpace(setting.Upstream)
	setting.Alias = strings.TrimSpace(setting.Alias)
	if setting.Upstream == "" {
		return errors.New("upstream model is required")
	}
	if setting.Alias == "" {
		setting.Alias = setting.Upstream
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.ModelSettings[setting.Upstream] = setting
	return s.saveLocked()
}

func (s *Store) DeleteModelSetting(upstream string) error {
	upstream = strings.TrimSpace(upstream)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.state.ModelSettings[upstream]; !exists {
		return os.ErrNotExist
	}
	delete(s.state.ModelSettings, upstream)
	return s.saveLocked()
}

func (s *Store) UsageRecords() []UsageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]UsageRecord(nil), s.state.Usage...)
}

func (s *Store) RecordUsage(record UsageRecord) error {
	if record.ID == "" {
		record.ID = randomID("req")
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Usage = append(s.state.Usage, record)
	compact := false
	if len(s.state.Usage) > maxUsageRecords {
		s.state.Usage = append([]UsageRecord(nil), s.state.Usage[len(s.state.Usage)-maxUsageRecords:]...)
		compact = true
	}
	for i := range s.state.Accounts {
		if s.state.Accounts[i].ID == record.AccountID {
			account := &s.state.Accounts[i]
			account.RequestCount++
			account.InputTokens += record.InputTokens
			account.OutputTokens += record.OutputTokens
			account.LastUsedAt = &record.Timestamp
			if record.Status >= 200 && record.Status < 400 {
				account.ConsecutiveErr = 0
				account.LastError = ""
			} else {
				account.ConsecutiveErr++
				account.LastError = record.Error
			}
			if record.Status >= 200 && record.Status < 300 {
				incrementRateLimitWindows(account)
			}
			break
		}
	}
	for i := range s.state.APIKeys {
		if s.state.APIKeys[i].ID == record.APIKeyID {
			s.state.APIKeys[i].RequestCount++
			s.state.APIKeys[i].InputTokens += record.InputTokens
			s.state.APIKeys[i].OutputTokens += record.OutputTokens
			s.state.APIKeys[i].LastUsedAt = &record.Timestamp
			break
		}
	}
	return s.saveUsageLocked(record, compact)
}

func incrementRateLimitWindows(account *Account) {
	for i := range account.Plan.RateLimitWindows {
		window := &account.Plan.RateLimitWindows[i]
		if window.ShowEnable != 1 {
			continue
		}
		window.CallsUsed++
		if window.CallLimit <= 0 {
			continue
		}
		window.UsagePercent = float64(window.CallsUsed) / float64(window.CallLimit) * 100
		if window.UsagePercent > 100 {
			window.UsagePercent = 100
		}
		window.QuotaExhausted = window.CallsUsed >= window.CallLimit
	}
}

func randomID(prefix string) string {
	data := make([]byte, 9)
	if _, err := rand.Read(data); err != nil {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())))
		data = sum[:9]
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(data)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
