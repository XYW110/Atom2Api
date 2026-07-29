package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type codingPlanClaimResponse struct {
	Success   bool   `json:"success"`
	Duplicate bool   `json:"duplicate"`
	Message   string `json:"message"`
	PlanName  string `json:"plan_name"`
	PlanType  string `json:"plan_type"`
}

type CodingPlanClient struct {
	config *ConfigManager
	store  *Store
	client *http.Client
	oauth  *OAuthManager
}

func NewCodingPlanClient(config *ConfigManager, store *Store) *CodingPlanClient {
	return &CodingPlanClient{config: config, store: store, client: &http.Client{Timeout: 20 * time.Second}}
}

func (c *CodingPlanClient) SetOAuthManager(oauth *OAuthManager) {
	c.oauth = oauth
}

func (c *CodingPlanClient) ClaimAndSync(ctx context.Context, accountID string) (AccountView, error) {
	account, token, _, err := c.store.Account(accountID)
	if err != nil {
		return AccountView{}, err
	}
	if c.oauth != nil {
		if refreshed, refreshErr := c.oauth.Refresh(ctx, accountID); refreshErr == nil {
			token = refreshed
		}
	}

	status, statusErr := c.fetchStatus(ctx, token)
	if statusErr == nil && status.Plan == nil {
		_, err = c.claim(ctx, token)
		if err != nil {
			return AccountView{}, err
		}
		status, statusErr = c.fetchStatus(ctx, token)
	}
	if statusErr != nil {
		return AccountView{}, statusErr
	}
	tier := planTier(status.Plan)
	models, err := c.fetchModels(ctx, token, tier)
	if err != nil {
		return AccountView{}, err
	}
	usage, usageErr := c.fetchUsage(ctx, token)
	now := time.Now().UTC()
	view, err := c.store.UpdateAccount(account.ID, func(stored *Account) error {
		stored.Plan = status
		stored.Models = models
		stored.ProviderUsage = usage
		stored.LastSyncAt = &now
		stored.Status = "active"
		stored.LastError = ""
		if usageErr != nil {
			stored.LastError = "usage sync: " + usageErr.Error()
		}
		return nil
	})
	return view, err
}

func (c *CodingPlanClient) Sync(ctx context.Context, accountID string) (AccountView, error) {
	account, token, _, err := c.store.Account(accountID)
	if err != nil {
		return AccountView{}, err
	}
	if c.oauth != nil {
		token, err = c.oauth.Refresh(ctx, accountID)
		if err != nil {
			return AccountView{}, err
		}
	}
	status, err := c.fetchStatus(ctx, token)
	if err != nil {
		return AccountView{}, err
	}
	models, err := c.fetchModels(ctx, token, planTier(status.Plan))
	if err != nil {
		return AccountView{}, err
	}
	usage, usageErr := c.fetchUsage(ctx, token)
	now := time.Now().UTC()
	return c.store.UpdateAccount(account.ID, func(stored *Account) error {
		stored.Plan = status
		stored.Models = models
		stored.ProviderUsage = usage
		stored.LastSyncAt = &now
		stored.Status = "active"
		stored.LastError = ""
		if usageErr != nil {
			stored.LastError = "usage sync: " + usageErr.Error()
		}
		return nil
	})
}

func (c *CodingPlanClient) claim(ctx context.Context, token string) (codingPlanClaimResponse, error) {
	var last string
	for _, tier := range []string{"Max", "Pro", "Lite"} {
		var response codingPlanClaimResponse
		err := c.request(ctx, http.MethodPost, "/coding-plan/claim-v2", token, map[string]string{"plan_type": tier}, &response)
		if err != nil {
			last = err.Error()
			continue
		}
		if response.Success || response.Duplicate {
			return response, nil
		}
		if response.Message != "" {
			last = response.Message
		}
	}
	if last == "" {
		last = "no Coding Plan tier is currently available"
	}
	return codingPlanClaimResponse{}, errors.New(last)
}

func (c *CodingPlanClient) fetchStatus(ctx context.Context, token string) (CodingPlanStatus, error) {
	var status CodingPlanStatus
	err := c.request(ctx, http.MethodGet, "/coding-plan/status-v2", token, nil, &status)
	return status, err
}

func (c *CodingPlanClient) fetchModels(ctx context.Context, token, tier string) ([]CodingPlanModel, error) {
	var models []CodingPlanModel
	path := "/coding-plan/models-v2?plan_type=" + url.QueryEscape(tier)
	if err := c.request(ctx, http.MethodGet, path, token, nil, &models); err != nil {
		return nil, err
	}
	config := c.config.Snapshot()
	available := make([]CodingPlanModel, 0, len(models))
	for _, model := range models {
		if model.DisplayModelName == "" || !model.PlanAvailable {
			continue
		}
		if model.BaseURL == "" {
			model.BaseURL = config.GatewayURL
		}
		if model.ProviderType == "" {
			model.ProviderType = "openai"
		}
		if model.ContextWindow <= 0 {
			model.ContextWindow = 64000
		}
		available = append(available, model)
	}
	return available, nil
}

func (c *CodingPlanClient) fetchUsage(ctx context.Context, token string) (*ProviderUsage, error) {
	var usage ProviderUsage
	if err := c.request(ctx, http.MethodGet, "/coding-plan/usage", token, nil, &usage); err != nil {
		return nil, err
	}
	return &usage, nil
}

func (c *CodingPlanClient) request(ctx context.Context, method, path, token string, body, result any) error {
	base := strings.TrimRight(c.config.Snapshot().CodingPlanAPIURL, "/")
	var encodedBody []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		encodedBody = encoded
	}
	var response *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var reader io.Reader
		if encodedBody != nil {
			reader = bytes.NewReader(encodedBody)
		}
		request, requestErr := http.NewRequestWithContext(ctx, method, base+path, reader)
		if requestErr != nil {
			return requestErr
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", c.config.Snapshot().UserAgent)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err = c.client.Do(request)
		if err == nil {
			break
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 150 * time.Millisecond)
		}
	}
	if err != nil {
		return fmt.Errorf("Coding Plan request failed: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("Coding Plan authentication failed (%d); reconnect the account", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Coding Plan returned %d: %s", response.StatusCode, compactError(data))
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode Coding Plan response: %w", err)
	}
	return nil
}

func planTier(plan *PlanInfo) string {
	if plan == nil {
		return "Lite"
	}
	name := strings.ToLower(plan.PlanName)
	switch {
	case strings.Contains(name, "max"):
		return "Max"
	case strings.Contains(name, "pro"):
		return "Pro"
	default:
		return "Lite"
	}
}

func compactError(data []byte) string {
	value := strings.TrimSpace(string(data))
	if len(value) > 500 {
		value = value[:500] + "..."
	}
	if value == "" {
		return "empty response"
	}
	return value
}
