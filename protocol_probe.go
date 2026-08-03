package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const protocolProbeBodyLimit = 2 << 20

type protocolProbeResult struct {
	Available bool   `json:"available"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type modelProtocolProbe struct {
	Model     string              `json:"model"`
	Chat      protocolProbeResult `json:"chat"`
	Responses protocolProbeResult `json:"responses"`
}

type accountProtocolProbeResponse struct {
	AccountID   string               `json:"account_id"`
	AccountName string               `json:"account_name"`
	Streaming   bool                 `json:"streaming"`
	Results     []modelProtocolProbe `json:"results"`
}

func (a *API) HandleAccountProtocolProbe(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Streaming bool `json:"streaming"`
	}
	if !decodeJSONBody(w, r, &request, 8<<10) {
		return
	}
	result, err := a.probeAccountProtocols(r.Context(), r.PathValue("id"), request.Streaming)
	if err != nil {
		if strings.Contains(err.Error(), "no available models") {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) probeAccountProtocols(ctx context.Context, accountID string, streaming bool) (accountProtocolProbeResponse, error) {
	if a.proxy == nil {
		return accountProtocolProbeResponse{}, errors.New("protocol probe is unavailable")
	}
	account, token, _, err := a.store.Account(accountID)
	if err != nil {
		return accountProtocolProbeResponse{}, err
	}
	if a.oauth != nil {
		if refreshed, refreshErr := a.oauth.Refresh(ctx, accountID); refreshErr == nil {
			token = refreshed
		} else {
			return accountProtocolProbeResponse{}, fmt.Errorf("refresh account token: %w", refreshErr)
		}
	}

	modelsByName := map[string]CodingPlanModel{}
	for _, model := range account.Models {
		name := strings.TrimSpace(model.DisplayModelName)
		if !model.PlanAvailable || name == "" {
			continue
		}
		if strings.TrimSpace(model.BaseURL) == "" {
			model.BaseURL = a.proxy.config.Snapshot().GatewayURL
		}
		modelsByName[name] = model
	}
	modelNames := make([]string, 0, len(modelsByName))
	for name := range modelsByName {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)
	if len(modelNames) == 0 {
		return accountProtocolProbeResponse{}, errors.New("account has no available models")
	}

	result := accountProtocolProbeResponse{
		AccountID: account.ID, AccountName: account.Name, Streaming: streaming,
		Results: make([]modelProtocolProbe, 0, len(modelNames)),
	}
	for _, name := range modelNames {
		model := modelsByName[name]
		result.Results = append(result.Results, modelProtocolProbe{
			Model:     name,
			Chat:      a.probeModelProtocol(ctx, account, token, model, "chat", streaming),
			Responses: a.probeModelProtocol(ctx, account, token, model, "responses", streaming),
		})
	}
	return result, nil
}

func (a *API) probeModelProtocol(ctx context.Context, account Account, token string, model CodingPlanModel, protocol string, streaming bool) protocolProbeResult {
	path := "/v1/chat/completions"
	payload := map[string]any{
		"model":      model.DisplayModelName,
		"messages":   []any{map[string]any{"role": "user", "content": "Reply with OK only."}},
		"max_tokens": 16,
		"stream":     streaming,
	}
	if streaming {
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	if protocol == "responses" {
		path = "/v1/responses"
		payload = map[string]any{
			"model": model.DisplayModelName, "input": "Reply with OK only.",
			"max_output_tokens": 16, "store": false, "stream": streaming,
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return protocolProbeResult{Error: err.Error()}
	}

	timeout := 30 * time.Second
	if configured := time.Duration(a.proxy.config.Snapshot().RequestTimeoutSecs) * time.Second; configured > 0 && configured < timeout {
		timeout = configured
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	route := ModelRoute{
		Requested: model.DisplayModelName, Upstream: model.DisplayModelName, Alias: model.DisplayModelName,
		Model: model, Account: account, Token: token,
	}
	_, response, err := a.proxy.doUpstreamRequest(requestContext, route, path, body, streaming)
	if err != nil {
		return protocolProbeResult{LatencyMS: time.Since(started).Milliseconds(), Error: err.Error()}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, protocolProbeBodyLimit+1))
	result := protocolProbeResult{Status: response.StatusCode, LatencyMS: time.Since(started).Milliseconds()}
	if readErr != nil {
		result.Error = fmt.Sprintf("read response: %v", readErr)
		return result
	}
	if len(data) > protocolProbeBodyLimit {
		result.Error = "response exceeds probe limit"
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = compactError(data)
		return result
	}
	if validationErr := validateProtocolProbeResponse(protocol, streaming, data); validationErr != nil {
		result.Error = validationErr.Error()
		return result
	}
	result.Available = true
	return result
}

func validateProtocolProbeResponse(protocol string, streaming bool, data []byte) error {
	if !streaming {
		if !json.Valid(data) {
			return errors.New("upstream returned invalid JSON")
		}
		var envelope map[string]any
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		if value, exists := envelope["error"]; exists && value != nil {
			return fmt.Errorf("upstream returned an error: %s", compactError(data))
		}
		return nil
	}
	if !bytes.Contains(data, []byte("data:")) {
		return errors.New("upstream returned no SSE data events")
	}
	if protocol == "chat" {
		if !bytes.Contains(data, []byte("[DONE]")) {
			return errors.New("Chat stream ended without [DONE]")
		}
		return nil
	}
	if bytes.Contains(data, []byte(`"type":"response.failed"`)) {
		return fmt.Errorf("Responses stream failed: %s", compactError(data))
	}
	if !bytes.Contains(data, []byte("response.completed")) {
		return errors.New("Responses stream ended without response.completed")
	}
	return nil
}
