package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type API struct {
	store      *Store
	oauth      *OAuthManager
	codingPlan *CodingPlanClient
	planClaims *PlanClaimService
	models     *ModelRouter
	proxy      *Proxy
	keyLimiter *apiKeyLimiter
}

func NewAPI(store *Store, oauth *OAuthManager, codingPlan *CodingPlanClient, models *ModelRouter, proxy *Proxy) *API {
	api := &API{
		store: store, oauth: oauth, codingPlan: codingPlan, models: models, proxy: proxy,
		keyLimiter: newAPIKeyLimiter(),
	}
	if codingPlan != nil {
		api.planClaims = NewPlanClaimService(store, codingPlan)
	}
	return api
}

func (a *API) HandleAccounts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.store.Accounts()})
}

const (
	accountCredentialBundleVersion = 1
	maxAccountCredentialBundleSize = 4 << 20
	maxImportedAccounts            = 100
)

type accountCredentialBundle struct {
	Version    int                           `json:"version"`
	ExportedAt time.Time                     `json:"exported_at"`
	Accounts   []accountCredentialBundleItem `json:"accounts"`
}

type accountCredentialBundleItem struct {
	Name        string           `json:"name"`
	Note        string           `json:"note,omitempty"`
	Enabled     bool             `json:"enabled"`
	User        UserInfo         `json:"user"`
	Credentials OAuthCredentials `json:"credentials"`
}

type accountCredentialImportError struct {
	Name   string `json:"name,omitempty"`
	UserID string `json:"user_id,omitempty"`
	Error  string `json:"error"`
}

type accountCredentialImportResponse struct {
	Data     []AccountView                  `json:"data"`
	Imported int                            `json:"imported"`
	Errors   []accountCredentialImportError `json:"errors,omitempty"`
}

func (a *API) HandleAccountCredentialExport(w http.ResponseWriter, r *http.Request) {
	bundle := accountCredentialBundle{
		Version:    accountCredentialBundleVersion,
		ExportedAt: time.Now().UTC(),
		Accounts:   make([]accountCredentialBundleItem, 0),
	}
	accountID := strings.TrimSpace(r.PathValue("id"))
	views := a.store.Accounts()
	if accountID != "" {
		found := false
		for _, view := range views {
			if view.ID == accountID {
				found = true
				break
			}
		}
		if !found {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "账号不存在"})
			return
		}
		views = []AccountView{{ID: accountID}}
	}
	for _, view := range views {
		account, accessToken, refreshToken, err := a.store.Account(view.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "无法读取账号凭据"})
			return
		}
		account.Credentials.AccessToken = accessToken
		account.Credentials.RefreshToken = refreshToken
		bundle.Accounts = append(bundle.Accounts, accountCredentialBundleItem{
			Name: account.Name, Note: account.Note, Enabled: account.Enabled, User: account.User, Credentials: account.Credentials,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	filename := "atom2api-credentials-v1.json"
	if accountID != "" {
		filename = fmt.Sprintf("atom2api-credentials-%s-v1.json", accountID)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(bundle); err != nil {
		return
	}
}

func (a *API) HandleAccountCredentialImport(w http.ResponseWriter, r *http.Request) {
	var bundle accountCredentialBundle
	if !decodeJSONBody(w, r, &bundle, maxAccountCredentialBundleSize) {
		return
	}
	if bundle.Version != accountCredentialBundleVersion {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "不支持的凭据包版本"})
		return
	}
	if len(bundle.Accounts) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "凭据包不包含账号"})
		return
	}
	if len(bundle.Accounts) > maxImportedAccounts {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("凭据包最多包含 %d 个账号", maxImportedAccounts)})
		return
	}
	result := accountCredentialImportResponse{Data: make([]AccountView, 0, len(bundle.Accounts))}
	seenUsers := make(map[string]struct{}, len(bundle.Accounts))
	for _, item := range bundle.Accounts {
		item.Name = strings.TrimSpace(item.Name)
		item.Note = strings.TrimSpace(item.Note)
		item.User.ID = strings.TrimSpace(item.User.ID)
		item.User.Username = strings.TrimSpace(item.User.Username)
		item.Credentials.AccessToken = strings.TrimSpace(item.Credentials.AccessToken)
		item.Credentials.RefreshToken = strings.TrimSpace(item.Credentials.RefreshToken)
		if item.User.ID == "" || item.Credentials.AccessToken == "" {
			result.Errors = append(result.Errors, accountCredentialImportError{Name: item.Name, UserID: item.User.ID, Error: "缺少用户 ID 或访问令牌"})
			continue
		}
		if _, exists := seenUsers[item.User.ID]; exists {
			result.Errors = append(result.Errors, accountCredentialImportError{Name: item.Name, UserID: item.User.ID, Error: "凭据包中存在重复账号"})
			continue
		}
		seenUsers[item.User.ID] = struct{}{}
		account := Account{
			Name: item.Name, Note: item.Note, Status: "syncing", Enabled: item.Enabled, User: item.User,
			Credentials: item.Credentials,
		}
		view, err := a.store.ImportAccountCredentials(account, item.Credentials.AccessToken, item.Credentials.RefreshToken)
		if err != nil {
			result.Errors = append(result.Errors, accountCredentialImportError{Name: item.Name, UserID: item.User.ID, Error: "保存凭据失败"})
			continue
		}
		if a.codingPlan != nil {
			synced, syncErr := a.codingPlan.Sync(r.Context(), view.ID)
			if syncErr != nil {
				view, _ = a.store.UpdateAccount(view.ID, func(stored *Account) error {
					stored.Status = "error"
					stored.LastError = syncErr.Error()
					return nil
				})
				result.Errors = append(result.Errors, accountCredentialImportError{Name: item.Name, UserID: item.User.ID, Error: "凭据已保存，但账号同步失败"})
			} else {
				view = synced
			}
		} else {
			view, _ = a.store.UpdateAccount(view.ID, func(stored *Account) error {
				stored.Status = "active"
				return nil
			})
		}
		result.Data = append(result.Data, view)
		result.Imported++
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) HandleAccountUpdate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name          *string            `json:"name"`
		Note          *string            `json:"note"`
		Enabled       *bool              `json:"enabled"`
		ClaimSchedule *PlanClaimSchedule `json:"plan_claim_schedule"`
	}
	if !decodeJSONBody(w, r, &request, 8<<10) {
		return
	}
	if request.ClaimSchedule != nil {
		request.ClaimSchedule.Cron = strings.TrimSpace(request.ClaimSchedule.Cron)
		if request.ClaimSchedule.Cron == "" {
			request.ClaimSchedule.Cron = defaultPlanClaimCron
		}
		if request.ClaimSchedule.Enabled && a.planClaims != nil {
			if err := a.planClaims.ValidateCron(request.ClaimSchedule.Cron); err != nil {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
				return
			}
		}
	}
	accountID := r.PathValue("id")
	view, err := a.store.UpdateAccount(accountID, func(account *Account) error {
		if request.Name != nil && strings.TrimSpace(*request.Name) != "" {
			account.Name = strings.TrimSpace(*request.Name)
		}
		if request.Note != nil {
			account.Note = strings.TrimSpace(*request.Note)
		}
		if request.Enabled != nil {
			account.Enabled = *request.Enabled
			if *request.Enabled && account.Status == "paused" {
				account.Status = "active"
			}
			if !*request.Enabled {
				account.Status = "paused"
			}
		}
		if request.ClaimSchedule != nil {
			schedule := *request.ClaimSchedule
			account.ClaimSchedule = &schedule
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if request.ClaimSchedule != nil && a.planClaims != nil {
		if err := a.planClaims.Reschedule(accountID); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) HandleAccountDelete(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	if err := a.store.DeleteAccount(accountID); err != nil {
		writeStoreError(w, err)
		return
	}
	if a.planClaims != nil {
		a.planClaims.Unschedule(accountID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) HandleErrorAccountsDelete(w http.ResponseWriter, _ *http.Request) {
	accountIDs, err := a.store.DeleteErrorAccounts()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if a.planClaims != nil {
		for _, accountID := range accountIDs {
			a.planClaims.Unschedule(accountID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": len(accountIDs)})
}

func (a *API) HandleAccountSync(w http.ResponseWriter, r *http.Request) {
	view, err := a.codingPlan.Sync(r.Context(), r.PathValue("id"))
	if err != nil {
		_, _ = a.store.UpdateAccount(r.PathValue("id"), func(account *Account) error {
			account.Status = "error"
			account.LastError = err.Error()
			return nil
		})
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) HandleAccountClaim(w http.ResponseWriter, r *http.Request) {
	if a.planClaims == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "Coding Plan claim service is unavailable"})
		return
	}
	// Bound the external Coding Plan calls so the origin never goes silent past
	// Cloudflare's timeout and turns the request into an upstream 502.
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result, err := a.planClaims.Claim(ctx, r.PathValue("id"), planClaimTriggerManual)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeStoreError(w, err)
			return
		}
		status := http.StatusBadGateway
		if errors.Is(err, errPlanClaimInProgress) {
			status = http.StatusConflict
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		writeJSON(w, status, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) HandlePlanClaimLogs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 200 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be between 1 and 200"})
			return
		}
		limit = parsed
	}
	logs := a.store.PlanClaimLogs(strings.TrimSpace(r.URL.Query().Get("account_id")), limit)
	writeJSON(w, http.StatusOK, map[string]any{"data": logs})
}

func (a *API) HandleOAuthStart(w http.ResponseWriter, r *http.Request) {
	started, err := a.oauth.Start(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, started)
}

func (a *API) HandleOAuthPoll(w http.ResponseWriter, r *http.Request) {
	result, err := a.oauth.Poll(r.Context(), r.PathValue("id"))
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) HandleKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.store.APIKeys()})
}

func (a *API) HandleCreateKey(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name             string   `json:"name"`
		AllowedModels    []string `json:"allowed_models"`
		ExpiresAt        string   `json:"expires_at"`
		RPMLimit         int      `json:"rpm_limit"`
		ConcurrencyLimit int      `json:"concurrency_limit"`
		RouteStrategy    string   `json:"route_strategy"`
	}
	if !decodeJSONBody(w, r, &request, 16<<10) {
		return
	}
	var expires *time.Time
	if strings.TrimSpace(request.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, request.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "expires_at must use RFC 3339 format"})
			return
		}
		expires = &parsed
	}
	if request.RPMLimit < 0 || request.ConcurrencyLimit < 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "API key limits cannot be negative"})
		return
	}
	if request.RouteStrategy != "" && !validRouteStrategy(request.RouteStrategy) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "route_strategy must be fill or round_robin"})
		return
	}
	view, secret, err := a.store.CreateAPIKeyWithRouting(request.Name, request.AllowedModels, expires, request.RPMLimit, request.ConcurrencyLimit, request.RouteStrategy)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": view, "secret": secret})
}

func (a *API) HandleUpdateKey(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name             *string   `json:"name"`
		Enabled          *bool     `json:"enabled"`
		AllowedModels    *[]string `json:"allowed_models"`
		RPMLimit         *int      `json:"rpm_limit"`
		ConcurrencyLimit *int      `json:"concurrency_limit"`
		RouteStrategy    *string   `json:"route_strategy"`
	}
	if !decodeJSONBody(w, r, &request, 16<<10) {
		return
	}
	if request.Name != nil && strings.TrimSpace(*request.Name) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "API key name cannot be empty"})
		return
	}
	if (request.RPMLimit != nil && *request.RPMLimit < 0) || (request.ConcurrencyLimit != nil && *request.ConcurrencyLimit < 0) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "API key limits cannot be negative"})
		return
	}
	if request.RouteStrategy != nil && !validRouteStrategy(*request.RouteStrategy) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "route_strategy must be fill or round_robin"})
		return
	}
	view, err := a.store.UpdateAPIKeyWithRouting(r.PathValue("id"), request.Name, request.Enabled, request.AllowedModels, nil, request.RPMLimit, request.ConcurrencyLimit, request.RouteStrategy)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) HandleDeleteKey(w http.ResponseWriter, r *http.Request) {
	keyID := r.PathValue("id")
	if err := a.store.DeleteAPIKey(keyID); err != nil {
		writeStoreError(w, err)
		return
	}
	a.keyLimiter.forget(keyID)
	if a.models != nil {
		a.models.ForgetAPIKeyRoutes(keyID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) HandleModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.models.Catalog()})
}

func (a *API) HandleCreateModel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Upstream string `json:"upstream"`
		Alias    string `json:"alias"`
	}
	if !decodeJSONBody(w, r, &request, 8<<10) {
		return
	}
	request.Upstream = strings.TrimSpace(request.Upstream)
	request.Alias = strings.TrimSpace(request.Alias)
	if request.Upstream == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "upstream model is required"})
		return
	}
	if request.Alias == "" {
		request.Alias = request.Upstream
	}
	for _, model := range a.models.Catalog() {
		if model.Upstream == request.Upstream || model.Alias == request.Upstream {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "upstream model is already in use"})
			return
		}
		if model.Upstream == request.Alias || model.Alias == request.Alias {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "model alias is already in use"})
			return
		}
	}
	setting := ModelSetting{
		Upstream: request.Upstream, Alias: request.Alias, Enabled: true, Manual: true,
		ResponsesChatCompat: defaultResponsesChatCompat(request.Upstream),
	}
	if err := a.store.SetModelSetting(setting); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	for _, model := range a.models.Catalog() {
		if model.Upstream == setting.Upstream {
			writeJSON(w, http.StatusCreated, model)
			return
		}
	}
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "model was saved but could not be loaded"})
}

func (a *API) HandleModelSetting(w http.ResponseWriter, r *http.Request) {
	var setting ModelSetting
	if !decodeJSONBody(w, r, &setting, 8<<10) {
		return
	}
	setting.Upstream = strings.TrimSpace(setting.Upstream)
	setting.Alias = strings.TrimSpace(setting.Alias)
	found := false
	for _, model := range a.models.Catalog() {
		if model.Upstream == setting.Upstream {
			found = true
			continue
		}
		if setting.Alias != "" && (model.Alias == setting.Alias || model.Upstream == setting.Alias) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "model alias is already in use"})
			return
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "upstream model not found"})
		return
	}
	if stored, exists := a.store.ModelSettings()[setting.Upstream]; exists {
		setting.Manual = stored.Manual
	}
	if err := a.store.SetModelSetting(setting); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, setting)
}

func (a *API) HandleDeleteModel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Upstream string `json:"upstream"`
	}
	if !decodeJSONBody(w, r, &request, 8<<10) {
		return
	}
	request.Upstream = strings.TrimSpace(request.Upstream)
	setting, exists := a.store.ModelSettings()[request.Upstream]
	if !exists || !setting.Manual {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "manual model not found"})
		return
	}
	if err := a.store.DeleteModelSetting(request.Upstream); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type dashboardResponse struct {
	Range             string              `json:"range"`
	Summary           dashboardSummary    `json:"summary"`
	Trend             []dashboardTrend    `json:"trend"`
	ModelDistribution []modelDistribution `json:"model_distribution"`
	RecentRequests    []auditListItem     `json:"recent_requests"`
}

const dashboardRPMWindow = 10 * time.Minute

type dashboardSummary struct {
	Requests       int64   `json:"requests"`
	RPM            float64 `json:"rpm"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	TotalTokens    int64   `json:"total_tokens"`
	SuccessRate    float64 `json:"success_rate"`
	AverageLatency int64   `json:"average_latency_ms"`
	ActiveAccounts int     `json:"active_accounts"`
	TotalAccounts  int     `json:"total_accounts"`
	ActiveKeys     int     `json:"active_keys"`
}

type dashboardTrend struct {
	Start        time.Time `json:"start"`
	Label        string    `json:"label"`
	Requests     int64     `json:"requests"`
	Errors       int64     `json:"errors"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
}

type modelDistribution struct {
	Model        string `json:"model"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

type auditListItem struct {
	ID                  string    `json:"id"`
	Timestamp           time.Time `json:"timestamp"`
	Method              string    `json:"method"`
	Path                string    `json:"path"`
	Model               string    `json:"model"`
	UpstreamModel       string    `json:"upstream_model"`
	AccountName         string    `json:"account_name"`
	KeyName             string    `json:"key_name"`
	Status              int       `json:"status"`
	LatencyMS           int64     `json:"latency_ms"`
	FirstTokenLatencyMS int64     `json:"first_token_latency_ms,omitempty"`
	CompletionLatencyMS int64     `json:"completion_latency_ms,omitempty"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	CachedTokens        int64     `json:"cached_tokens,omitempty"`
	ReasoningTokens     int64     `json:"reasoning_tokens,omitempty"`
	Streaming           bool      `json:"streaming"`
	HasRequestBody      bool      `json:"has_request_body"`
	HasResponseBody     bool      `json:"has_response_body"`
	RetryCount          int       `json:"retry_count"`
}

type auditListResponse struct {
	Items    []auditListItem `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Pages    int             `json:"pages"`
}

type auditDetailResponse struct {
	UsageRecord
	AccountName string `json:"account_name"`
	KeyName     string `json:"key_name"`
}

type auditCleanupRequest struct {
	Days int `json:"days"`
}

type auditCleanupResponse struct {
	Affected int       `json:"affected"`
	Cutoff   time.Time `json:"cutoff"`
}

func auditItem(record UsageRecord, accountName, keyName string) auditListItem {
	method := record.Method
	if method == "" {
		method = http.MethodPost
	}
	return auditListItem{
		ID: record.ID, Timestamp: record.Timestamp, Method: method, Path: record.Path,
		Model: record.Model, UpstreamModel: record.UpstreamModel, AccountName: accountName, KeyName: keyName,
		Status: record.Status, LatencyMS: record.LatencyMS,
		FirstTokenLatencyMS: record.FirstTokenLatencyMS, CompletionLatencyMS: record.CompletionLatencyMS, InputTokens: record.InputTokens,
		OutputTokens: record.OutputTokens, CachedTokens: record.CachedTokens,
		ReasoningTokens: record.ReasoningTokens, Streaming: record.Streaming,
		HasRequestBody: record.RequestBody != "", HasResponseBody: record.ResponseBody != "",
		RetryCount: record.RetryCount,
	}
}

func (a *API) auditNames() (map[string]string, map[string]string) {
	accounts := map[string]string{}
	for _, account := range a.store.Accounts() {
		accounts[account.ID] = account.Name
	}
	keys := map[string]string{}
	for _, key := range a.store.APIKeys() {
		keys[key.ID] = key.Name
	}
	return accounts, keys
}

func (a *API) HandleAudits(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	methodFilter := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("method")))
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	statusCode, statusError := strconv.Atoi(statusFilter)
	accounts, keys := a.auditNames()
	response := auditListResponse{Items: []auditListItem{}, Page: page, PageSize: pageSize}
	start := (page - 1) * pageSize
	records := a.store.UsageRecords()
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		method := record.Method
		if method == "" {
			method = http.MethodPost
		}
		if methodFilter != "" && method != methodFilter {
			continue
		}
		switch statusFilter {
		case "success":
			if record.Status < 200 || record.Status >= 400 {
				continue
			}
		case "error":
			if record.Status >= 200 && record.Status < 400 {
				continue
			}
		default:
			if statusError == nil && record.Status != statusCode {
				continue
			}
		}
		accountName, keyName := accounts[record.AccountID], keys[record.APIKeyID]
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				record.ID, record.Path, record.Model, record.UpstreamModel, accountName, keyName,
			}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		if response.Total >= start && len(response.Items) < pageSize {
			response.Items = append(response.Items, auditItem(record, accountName, keyName))
		}
		response.Total++
	}
	response.Pages = (response.Total + pageSize - 1) / pageSize
	writeJSON(w, http.StatusOK, response)
}

func (a *API) HandleAuditDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	records := a.store.UsageRecords()
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].ID != id {
			continue
		}
		record := records[index]
		if record.Method == "" {
			record.Method = http.MethodPost
		}
		accounts, keys := a.auditNames()
		writeJSON(w, http.StatusOK, auditDetailResponse{
			UsageRecord: record, AccountName: accounts[record.AccountID], KeyName: keys[record.APIKeyID],
		})
		return
	}
	writeJSON(w, http.StatusNotFound, errorResponse{Error: "audit request not found"})
}

func (a *API) HandleAuditRecordCleanup(w http.ResponseWriter, r *http.Request) {
	a.handleAuditCleanup(w, r, false)
}

func (a *API) HandleAuditDetailCleanup(w http.ResponseWriter, r *http.Request) {
	a.handleAuditCleanup(w, r, true)
}

func (a *API) handleAuditCleanup(w http.ResponseWriter, r *http.Request, detailsOnly bool) {
	var request auditCleanupRequest
	if !decodeJSONBody(w, r, &request, 1<<10) {
		return
	}
	if request.Days < 1 || request.Days > maxAuditRetention {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("days must be between 1 and %d", maxAuditRetention)})
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -request.Days)
	var (
		affected int
		err      error
	)
	if detailsOnly {
		affected, err = a.store.ClearUsageDetailsBefore(cutoff)
	} else {
		affected, err = a.store.DeleteUsageRecordsBefore(cutoff)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, auditCleanupResponse{Affected: affected, Cutoff: cutoff})
}

func (a *API) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	rangeName := r.URL.Query().Get("range")
	duration, bucket := dashboardRange(rangeName)
	if rangeName == "" {
		rangeName = "24h"
	}
	now := time.Now().UTC()
	start := now.Add(-duration)
	rpmStart := now.Add(-dashboardRPMWindow)
	bucketCount := int(duration / bucket)
	trend := make([]dashboardTrend, bucketCount)
	for i := 0; i < bucketCount; i++ {
		bucketStart := start.Add(time.Duration(i) * bucket)
		label := bucketStart.Format("15:04")
		if bucket >= 24*time.Hour {
			label = bucketStart.Format("01-02")
		}
		trend[i] = dashboardTrend{Start: bucketStart, Label: label}
	}
	response := dashboardResponse{Range: rangeName, Trend: trend}
	models := map[string]*modelDistribution{}
	accounts := map[string]string{}
	for _, account := range a.store.Accounts() {
		accounts[account.ID] = account.Name
		response.Summary.TotalAccounts++
		if account.Enabled && account.Status == "active" {
			response.Summary.ActiveAccounts++
		}
	}
	keys := map[string]string{}
	for _, key := range a.store.APIKeys() {
		keys[key.ID] = key.Name
		if key.Enabled && (key.ExpiresAt == nil || key.ExpiresAt.After(now)) {
			response.Summary.ActiveKeys++
		}
	}
	var latencyTotal int64
	var successes int64
	var recentRequests int64
	records := a.store.UsageRecords()
	for i := len(records) - 1; i >= 0 && len(response.RecentRequests) < 20; i-- {
		record := records[i]
		response.RecentRequests = append(response.RecentRequests, auditItem(record, accounts[record.AccountID], keys[record.APIKeyID]))
	}
	for _, record := range records {
		if record.Timestamp.Before(start) || record.Timestamp.After(now.Add(time.Minute)) {
			continue
		}
		if !record.Timestamp.Before(rpmStart) && !record.Timestamp.After(now) {
			recentRequests++
		}
		response.Summary.Requests++
		response.Summary.InputTokens += record.InputTokens
		response.Summary.OutputTokens += record.OutputTokens
		latencyTotal += record.LatencyMS
		if record.Status >= 200 && record.Status < 400 {
			successes++
		}
		index := int(record.Timestamp.Sub(start) / bucket)
		if index >= 0 && index < len(response.Trend) {
			point := &response.Trend[index]
			point.Requests++
			point.InputTokens += record.InputTokens
			point.OutputTokens += record.OutputTokens
			if record.Status < 200 || record.Status >= 400 {
				point.Errors++
			}
		}
		entry := models[record.Model]
		if entry == nil {
			entry = &modelDistribution{Model: record.Model}
			models[record.Model] = entry
		}
		entry.Requests++
		entry.InputTokens += record.InputTokens
		entry.OutputTokens += record.OutputTokens
	}
	response.Summary.RPM = float64(recentRequests) / dashboardRPMWindow.Minutes()
	response.Summary.TotalTokens = response.Summary.InputTokens + response.Summary.OutputTokens
	if response.Summary.Requests > 0 {
		response.Summary.SuccessRate = float64(successes) / float64(response.Summary.Requests) * 100
		response.Summary.AverageLatency = latencyTotal / response.Summary.Requests
	}
	for _, entry := range models {
		response.ModelDistribution = append(response.ModelDistribution, *entry)
	}
	sort.Slice(response.ModelDistribution, func(i, j int) bool {
		return response.ModelDistribution[i].Requests > response.ModelDistribution[j].Requests
	})
	writeJSON(w, http.StatusOK, response)
}

func dashboardRange(value string) (time.Duration, time.Duration) {
	switch value {
	case "7d":
		return 7 * 24 * time.Hour, 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour, 24 * time.Hour
	default:
		return 24 * time.Hour, time.Hour
	}
}

func (a *API) RequireAPIKey(next func(http.ResponseWriter, *http.Request, APIKey)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = strings.TrimSpace(token[7:])
		} else {
			token = strings.TrimSpace(r.Header.Get("X-API-Key"))
		}
		key, ok := a.store.AuthenticateAPIKey(token)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Atom2Api"`)
			writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "invalid or expired API key")
			return
		}
		release, rejection, allowed := a.keyLimiter.acquire(key)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(rejection.retryAfter))
			writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", rejection.message)
			return
		}
		defer release()
		next(w, r, key)
	}
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || ensureJSONEOF(decoder) != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "resource not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
}

func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
