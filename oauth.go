package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type platformLoginResponse struct {
	LoginURL string `json:"login_url"`
	State    string `json:"state"`
}

type platformCheckResponse struct {
	Valid bool `json:"valid"`
}

type platformTokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int64    `json:"expires_in"`
	User         UserInfo `json:"user"`
}

type oauthBrokerResponseError struct {
	status int
	body   string
}

func (e *oauthBrokerResponseError) Error() string {
	return fmt.Sprintf("broker returned %d: %s", e.status, e.body)
}

type oauthCredentialsUnavailableError struct {
	err error
}

func (e *oauthCredentialsUnavailableError) Error() string { return e.err.Error() }
func (e *oauthCredentialsUnavailableError) Unwrap() error { return e.err }

func oauthCredentialsUnavailable(err error) bool {
	var unavailable *oauthCredentialsUnavailableError
	return errors.As(err, &unavailable)
}

type pendingOAuth struct {
	ID        string
	State     string
	LoginURL  string
	CreatedAt time.Time
	Complete  bool
	Finishing bool
	AccountID string
}

type OAuthStart struct {
	ID        string    `json:"id"`
	LoginURL  string    `json:"login_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OAuthPollResult struct {
	Status  string       `json:"status"`
	Account *AccountView `json:"account,omitempty"`
}

type OAuthManager struct {
	config     *ConfigManager
	store      *Store
	codingPlan *CodingPlanClient
	planClaims *PlanClaimService
	client     *http.Client
	mu         sync.Mutex
	pending    map[string]*pendingOAuth
}

func NewOAuthManager(config *ConfigManager, store *Store, codingPlan *CodingPlanClient) *OAuthManager {
	return &OAuthManager{
		config: config, store: store, codingPlan: codingPlan,
		client: &http.Client{Timeout: 15 * time.Second}, pending: map[string]*pendingOAuth{},
	}
}

func (m *OAuthManager) SetPlanClaimService(planClaims *PlanClaimService) {
	m.planClaims = planClaims
}

func (m *OAuthManager) Start(ctx context.Context) (OAuthStart, error) {
	endpoint := strings.TrimRight(m.config.Snapshot().PlatformBaseURL, "/") + "/auth/login"
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return OAuthStart{}, err
	}
	query := parsed.Query()
	query.Set("provider", "atomgit")
	parsed.RawQuery = query.Encode()
	var response platformLoginResponse
	if err := m.doJSON(ctx, http.MethodGet, parsed.String(), nil, &response); err != nil {
		return OAuthStart{}, fmt.Errorf("start AtomGit OAuth: %w", err)
	}
	if response.State == "" || response.LoginURL == "" {
		return OAuthStart{}, errors.New("OAuth broker returned an incomplete login session")
	}
	response.LoginURL = stripForceLogin(response.LoginURL)
	now := time.Now().UTC()
	pending := &pendingOAuth{
		ID: randomID("oauth"), State: response.State, LoginURL: response.LoginURL, CreatedAt: now,
	}
	m.mu.Lock()
	m.pruneLocked(now)
	m.pending[pending.ID] = pending
	m.mu.Unlock()
	return OAuthStart{ID: pending.ID, LoginURL: pending.LoginURL, ExpiresAt: now.Add(10 * time.Minute)}, nil
}

func (m *OAuthManager) Poll(ctx context.Context, id string) (OAuthPollResult, error) {
	m.mu.Lock()
	pending := m.pending[id]
	if pending == nil {
		m.mu.Unlock()
		return OAuthPollResult{}, osErrNotExist("OAuth session")
	}
	if time.Since(pending.CreatedAt) > 10*time.Minute {
		delete(m.pending, id)
		m.mu.Unlock()
		return OAuthPollResult{Status: "expired"}, nil
	}
	if pending.Complete {
		accountID := pending.AccountID
		m.mu.Unlock()
		view, err := m.accountView(accountID)
		if err != nil {
			return OAuthPollResult{}, err
		}
		return OAuthPollResult{Status: "complete", Account: &view}, nil
	}
	if pending.Finishing {
		m.mu.Unlock()
		return OAuthPollResult{Status: "pending"}, nil
	}
	pending.Finishing = true
	state := pending.State
	m.mu.Unlock()
	finished := false
	defer func() {
		if finished {
			return
		}
		m.mu.Lock()
		if current := m.pending[id]; current != nil {
			current.Finishing = false
		}
		m.mu.Unlock()
	}()

	base := strings.TrimRight(m.config.Snapshot().PlatformBaseURL, "/")
	checkURL := base + "/auth/check?state=" + url.QueryEscape(state)
	var check platformCheckResponse
	if err := m.doJSON(ctx, http.MethodGet, checkURL, nil, &check); err != nil {
		return OAuthPollResult{}, fmt.Errorf("check AtomGit OAuth: %w", err)
	}
	if !check.Valid {
		m.mu.Lock()
		if current := m.pending[id]; current != nil {
			current.Finishing = false
		}
		m.mu.Unlock()
		finished = true
		return OAuthPollResult{Status: "pending"}, nil
	}

	var token platformTokenResponse
	if err := m.doJSON(ctx, http.MethodGet, base+"/auth/token?state="+url.QueryEscape(state), nil, &token); err != nil {
		return OAuthPollResult{}, fmt.Errorf("finish AtomGit OAuth: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.User.ID) == "" {
		return OAuthPollResult{}, errors.New("OAuth broker returned incomplete credentials")
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	now := time.Now().UTC()
	account := Account{
		Name: token.User.Name, Status: "syncing", Enabled: true, User: token.User,
		Credentials: OAuthCredentials{TokenType: token.TokenType, ExpiresIn: token.ExpiresIn, CreatedAt: now},
	}
	view, err := m.store.UpsertAccount(account, token.AccessToken, token.RefreshToken)
	if err != nil {
		return OAuthPollResult{}, err
	}
	var claimErr error
	if m.planClaims != nil {
		_, claimErr = m.planClaims.Claim(ctx, view.ID, planClaimTriggerManual)
	} else {
		_, claimErr = m.codingPlan.ClaimAndSync(ctx, view.ID)
	}
	if claimErr != nil {
		_, _ = m.store.UpdateAccount(view.ID, func(account *Account) error {
			account.Status = "error"
			account.LastError = claimErr.Error()
			return nil
		})
	}
	view, err = m.accountView(view.ID)
	if err != nil {
		return OAuthPollResult{}, err
	}
	m.mu.Lock()
	if current := m.pending[id]; current != nil {
		current.Complete = true
		current.Finishing = false
		current.AccountID = view.ID
	}
	m.mu.Unlock()
	finished = true
	return OAuthPollResult{Status: "complete", Account: &view}, nil
}

func (m *OAuthManager) Refresh(ctx context.Context, accountID string) (string, error) {
	account, access, refresh, err := m.store.Account(accountID)
	if err != nil {
		return "", err
	}
	if account.Credentials.ExpiresIn <= 0 || account.Credentials.CreatedAt.Add(time.Duration(account.Credentials.ExpiresIn)*time.Second).After(time.Now().Add(5*time.Minute)) {
		return access, nil
	}
	if refresh == "" {
		return "", &oauthCredentialsUnavailableError{err: errors.New("OAuth token expired and no refresh token is available")}
	}
	request := map[string]string{"refresh_token": refresh}
	var response platformTokenResponse
	endpoint := strings.TrimRight(m.config.Snapshot().PlatformBaseURL, "/") + "/oauth/refresh"
	if err := m.doJSON(ctx, http.MethodPost, endpoint, request, &response); err != nil {
		refreshErr := fmt.Errorf("refresh OAuth token: %w", err)
		var responseErr *oauthBrokerResponseError
		if errors.As(err, &responseErr) && (responseErr.status == http.StatusBadRequest || responseErr.status == http.StatusUnauthorized || responseErr.status == http.StatusForbidden) {
			return "", &oauthCredentialsUnavailableError{err: refreshErr}
		}
		return "", refreshErr
	}
	if response.AccessToken == "" {
		return "", &oauthCredentialsUnavailableError{err: errors.New("OAuth refresh returned an empty access token")}
	}
	if response.RefreshToken == "" {
		response.RefreshToken = refresh
	}
	if response.ExpiresIn == 0 {
		response.ExpiresIn = account.Credentials.ExpiresIn
	}
	if err := m.store.UpdateAccountCredentials(accountID, response.AccessToken, response.RefreshToken, response.ExpiresIn, time.Now().UTC()); err != nil {
		return "", err
	}
	return response.AccessToken, nil
}

func (m *OAuthManager) accountView(id string) (AccountView, error) {
	for _, account := range m.store.Accounts() {
		if account.ID == id {
			return account, nil
		}
	}
	return AccountView{}, osErrNotExist("account")
}

func (m *OAuthManager) doJSON(ctx context.Context, method, endpoint string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", m.config.Snapshot().UserAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &oauthBrokerResponseError{status: response.StatusCode, body: compactError(data)}
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode broker response: %w", err)
	}
	return nil
}

func (m *OAuthManager) pruneLocked(now time.Time) {
	for id, pending := range m.pending {
		if now.Sub(pending.CreatedAt) > 15*time.Minute {
			delete(m.pending, id)
		}
	}
}

func stripForceLogin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.ReplaceAll(raw, "&force_login=true", "")
	}
	query := parsed.Query()
	query.Del("force_login")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func secureNonce(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return randomID("nonce")
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func osErrNotExist(resource string) error {
	return fmt.Errorf("%s not found: %w", resource, errors.New("not found"))
}
