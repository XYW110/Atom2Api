package main

import (
	"encoding/json"
	"errors"
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
	result, err := a.planClaims.Claim(r.Context(), r.PathValue("id"), planClaimTriggerManual)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeStoreError(w, err)
			return
		}
		status := http.StatusBadGateway
		if errors.Is(err, errPlanClaimInProgress) {
			status = http.StatusConflict
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
	view, secret, err := a.store.CreateAPIKeyWithLimits(request.Name, request.AllowedModels, expires, request.RPMLimit, request.ConcurrencyLimit)
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
	view, err := a.store.UpdateAPIKeyWithLimits(r.PathValue("id"), request.Name, request.Enabled, request.AllowedModels, nil, request.RPMLimit, request.ConcurrencyLimit)
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

type dashboardSummary struct {
	Requests       int64   `json:"requests"`
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

func (a *API) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	rangeName := r.URL.Query().Get("range")
	duration, bucket := dashboardRange(rangeName)
	if rangeName == "" {
		rangeName = "24h"
	}
	now := time.Now().UTC()
	start := now.Add(-duration)
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
	records := a.store.UsageRecords()
	for i := len(records) - 1; i >= 0 && len(response.RecentRequests) < 20; i-- {
		record := records[i]
		response.RecentRequests = append(response.RecentRequests, auditItem(record, accounts[record.AccountID], keys[record.APIKeyID]))
	}
	for _, record := range records {
		if record.Timestamp.Before(start) || record.Timestamp.After(now.Add(time.Minute)) {
			continue
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
