package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Proxy struct {
	config *ConfigManager
	store  *Store
	router *ModelRouter
	oauth  *OAuthManager
	client *http.Client
}

type tokenUsage struct {
	Input     int64
	Output    int64
	Cached    int64
	Reasoning int64
}

type auditResponseWriter struct {
	http.ResponseWriter
	status      int
	captureBody bool
	body        bytes.Buffer
}

func (w *auditResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(data)
	if written > 0 && w.captureBody {
		_, _ = w.body.Write(data[:written])
	}
	return written, err
}

func (w *auditResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *auditResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func NewProxy(config *ConfigManager, store *Store, router *ModelRouter, oauth *OAuthManager) *Proxy {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &Proxy{
		config: config, store: store, router: router, oauth: oauth,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("upstream redirects are disabled")
			},
		},
	}
}

func (p *Proxy) HandleModels(w http.ResponseWriter, _ *http.Request, _ APIKey) {
	models := p.router.Catalog()
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		if !model.Enabled || model.AccountCount == 0 {
			continue
		}
		data = append(data, map[string]any{
			"id": model.Alias, "object": "model", "created": 0, "owned_by": "atom2api",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (p *Proxy) HandleRequest(w http.ResponseWriter, r *http.Request, key APIKey) {
	started := time.Now()
	debugEnabled := p.config.Snapshot().AuditDebugEnabled
	captured := &auditResponseWriter{ResponseWriter: w, captureBody: debugEnabled}
	w = captured
	audit := UsageRecord{
		Timestamp: started.UTC(), Method: r.Method, Path: r.URL.Path, APIKeyID: key.ID,
	}
	if debugEnabled {
		audit.RequestHeaders = auditHeaders(r.Header)
	}
	defer func() {
		audit.LatencyMS = time.Since(started).Milliseconds()
		audit.Status = captured.status
		if audit.Status == 0 {
			audit.Status = http.StatusInternalServerError
		}
		if captured.captureBody {
			audit.ResponseBody = captured.body.String()
		}
		if debugEnabled && len(audit.ResponseHeaders) == 0 {
			audit.ResponseHeaders = auditHeaders(captured.Header())
		}
		p.record(audit)
	}()
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	body, err := io.ReadAll(r.Body)
	if debugEnabled {
		audit.RequestBody = string(body)
	}
	if err != nil {
		audit.Error = "request body is too large or unreadable"
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "request body is too large or unreadable")
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		audit.Error = "request body must be a JSON object"
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "request body must be a JSON object")
		return
	}
	var requestedModel string
	if raw := payload["model"]; raw != nil {
		_ = json.Unmarshal(raw, &requestedModel)
	}
	audit.Model = requestedModel
	route, err := p.router.Resolve(requestedModel, key)
	if err != nil {
		status := http.StatusBadRequest
		code := "model_not_found"
		if strings.Contains(err.Error(), "no active account") {
			status = http.StatusTooManyRequests
			code = "insufficient_quota"
		}
		audit.Error = err.Error()
		writeOpenAIError(w, status, code, audit.Error)
		return
	}
	audit.UpstreamModel = route.Upstream
	audit.AccountID = route.Account.ID
	if p.oauth != nil {
		if refreshed, refreshErr := p.oauth.Refresh(r.Context(), route.Account.ID); refreshErr == nil {
			route.Token = refreshed
		} else {
			audit.Error = refreshErr.Error()
			writeOpenAIError(w, http.StatusBadGateway, "upstream_auth_error", audit.Error)
			return
		}
	}
	modelJSON, _ := json.Marshal(route.Upstream)
	payload["model"] = modelJSON
	streaming := false
	if raw := payload["stream"]; raw != nil {
		_ = json.Unmarshal(raw, &streaming)
	}
	audit.Streaming = streaming
	if streaming && r.URL.Path == "/v1/chat/completions" {
		var streamOptions map[string]any
		if raw := payload["stream_options"]; raw != nil {
			_ = json.Unmarshal(raw, &streamOptions)
		}
		if streamOptions == nil {
			streamOptions = map[string]any{}
		}
		streamOptions["include_usage"] = true
		payload["stream_options"], _ = json.Marshal(streamOptions)
	}
	body, err = json.Marshal(payload)
	if err != nil {
		audit.Error = "could not encode upstream request"
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "could not encode upstream request")
		return
	}
	upstreamURL, err := joinUpstreamURL(route.Model.BaseURL, r.URL.Path)
	if err != nil {
		audit.Error = err.Error()
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	timeout := time.Duration(p.config.Snapshot().RequestTimeoutSecs) * time.Second
	requestContext, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		audit.Error = err.Error()
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	request.Header.Set("Authorization", "Bearer "+route.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if streaming {
		request.Header.Set("Accept", "text/event-stream")
	}
	request.Header.Set("User-Agent", p.config.Snapshot().UserAgent)
	request.Header.Set("X-Request-Id", randomID("req"))
	if err := p.applySignature(r.Context(), request, body, route); err != nil {
		audit.Error = err.Error()
		writeOpenAIError(w, http.StatusBadGateway, "signing_error", audit.Error)
		return
	}
	if debugEnabled {
		audit.RequestHeaders = auditHeaders(request.Header)
	}
	response, err := p.client.Do(request)
	if err != nil {
		message := "upstream request failed: " + err.Error()
		audit.Error = message
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", message)
		return
	}
	defer response.Body.Close()
	if debugEnabled || response.StatusCode != http.StatusOK {
		captured.captureBody = true
		audit.ResponseHeaders = auditHeaders(response.Header)
	}
	copyUpstreamHeaders(w.Header(), response.Header)
	w.Header().Set("X-Atom2api-Account", route.Account.ID)
	if streaming && response.StatusCode >= 200 && response.StatusCode < 300 {
		usage, errorText := p.streamResponse(w, response, route)
		audit.InputTokens, audit.OutputTokens = usage.Input, usage.Output
		audit.CachedTokens, audit.ReasoningTokens = usage.Cached, usage.Reasoning
		audit.Error = errorText
		return
	}
	usage, errorText := p.bufferedResponse(w, response, route)
	audit.InputTokens, audit.OutputTokens = usage.Input, usage.Output
	audit.CachedTokens, audit.ReasoningTokens = usage.Cached, usage.Reasoning
	audit.Error = errorText
}

func (p *Proxy) bufferedResponse(w http.ResponseWriter, response *http.Response, route ModelRoute) (tokenUsage, string) {
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "could not read upstream response")
		return tokenUsage{}, "could not read upstream response"
	}
	usage := extractUsage(body)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		body = rewriteResponseModel(body, route.Requested)
	}
	w.Header().Del("Content-Length")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
	errorText := ""
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		errorText = compactError(body)
	}
	return usage, errorText
}

func (p *Proxy) streamResponse(w http.ResponseWriter, response *http.Response, route ModelRoute) (tokenUsage, string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "streaming is not supported by this server")
		return tokenUsage{}, "streaming is not supported by this server"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Del("Content-Length")
	w.WriteHeader(response.StatusCode)
	reader := bufio.NewReader(response.Body)
	var usage tokenUsage
	errorText := ""
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if bytes.HasPrefix(trimmed, []byte("data:")) {
				data := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
				if len(data) > 0 && !bytes.Equal(data, []byte("[DONE]")) {
					chunkUsage := extractUsage(data)
					if chunkUsage.Input > 0 || chunkUsage.Output > 0 {
						usage = chunkUsage
					}
					rewritten := rewriteResponseModel(data, route.Requested)
					line = append(append([]byte("data: "), rewritten...), '\n')
				}
			}
			if _, writeErr := w.Write(line); writeErr != nil {
				errorText = "client disconnected"
				break
			}
			flusher.Flush()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				errorText = err.Error()
			}
			break
		}
	}
	return usage, errorText
}

func (p *Proxy) applySignature(ctx context.Context, request *http.Request, body []byte, route ModelRoute) error {
	if !isAtomGitGateway(request.URL) {
		return nil
	}
	config := p.config.Snapshot()
	if strings.TrimSpace(config.SignerURL) == "" {
		version := strings.TrimSpace(strings.TrimPrefix(config.UserAgent, "atomcode/"))
		if version == "" || version == config.UserAgent {
			return errors.New("user_agent must use the atomcode/<version> format for built-in signing")
		}
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return fmt.Errorf("generate signing nonce: %w", err)
		}
		headers, err := signAtomGitRequest(request.Method, request.URL.EscapedPath(), body, route.Token, route.Account.User.ID, version, time.Now().Unix(), nonce, "")
		if err != nil {
			return err
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		return nil
	}
	payload := map[string]any{
		"method": request.Method, "path": request.URL.EscapedPath(), "body": base64.RawStdEncoding.EncodeToString(body),
		"oauth_token": route.Token, "user_id": route.Account.User.ID,
		"timestamp_unix": time.Now().Unix(), "nonce": secureNonce(16), "client_version": strings.TrimPrefix(config.UserAgent, "atomcode/"),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	signRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, config.SignerURL, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	signRequest.Header.Set("Content-Type", "application/json")
	if config.SignerToken != "" {
		signRequest.Header.Set("Authorization", "Bearer "+config.SignerToken)
	}
	response, err := p.client.Do(signRequest)
	if err != nil {
		return fmt.Errorf("request signer failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("request signer returned %d: %s", response.StatusCode, compactError(responseBody))
	}
	var signed struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(responseBody, &signed); err != nil {
		return fmt.Errorf("decode signer response: %w", err)
	}
	if len(signed.Headers) == 0 {
		return errors.New("request signer returned no headers")
	}
	for name, value := range signed.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "" || canonical == "Authorization" || canonical == "Host" || canonical == "Content-Length" || isHopByHop(canonical) {
			continue
		}
		request.Header.Set(canonical, value)
	}
	return nil
}

// atomcode-signing-v1 was independently documented by the MIT-licensed
// atomgit-opencode-bridge project. Keep the primitive isolated so the master
// key can be replaced without changing proxy or credential storage code.
func signAtomGitRequest(method, path string, body []byte, oauthToken, userID, clientVersion string, timestamp int64, nonce []byte, masterKeyHex string) (map[string]string, error) {
	if oauthToken == "" || userID == "" {
		return nil, errors.New("AtomGit signing requires an OAuth token and user id")
	}
	if len(nonce) != 16 {
		return nil, errors.New("AtomGit signing nonce must be 16 bytes")
	}
	if masterKeyHex == "" {
		masterKeyHex = "e97250f05303162c8ecd68c688b2f55c1d81e508d243d88466472e7f54637123"
	}
	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(masterKey) != 32 {
		return nil, errors.New("AtomGit signing master key must be 32-byte hex")
	}
	tokenHash := sha256.Sum256([]byte(oauthToken))
	versionHash := sha256.Sum256([]byte(clientVersion))
	salt := make([]byte, 0, len(userID)+1+8+sha256.Size*2)
	salt = append(salt, []byte(userID)...)
	salt = append(salt, 1)
	timeBucket := make([]byte, 8)
	binary.LittleEndian.PutUint64(timeBucket, uint64(timestamp/3600))
	salt = append(salt, timeBucket...)
	salt = append(salt, tokenHash[:]...)
	salt = append(salt, versionHash[:]...)

	// HKDF-SHA256 extract + one expand block (the requested output is 32 bytes).
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(masterKey)
	pseudorandomKey := extract.Sum(nil)
	expand := hmac.New(sha256.New, pseudorandomKey)
	_, _ = expand.Write([]byte("atomcode-signing-v1"))
	_, _ = expand.Write([]byte{1})
	signingKey := expand.Sum(nil)

	bodyHash := sha256.Sum256(body)
	nonceHex := hex.EncodeToString(nonce)
	canonical := strings.Join([]string{
		"v1", strings.ToUpper(method), path, fmt.Sprintf("%d", timestamp), nonceHex, hex.EncodeToString(bodyHash[:]),
	}, "\n")
	signer := hmac.New(sha256.New, signingKey)
	_, _ = signer.Write([]byte(canonical))
	signature := hex.EncodeToString(signer.Sum(nil))
	return map[string]string{
		"X-AtomCode-Sig":   "v1:" + signature,
		"X-AtomCode-Ts":    fmt.Sprintf("%d", timestamp),
		"X-AtomCode-Nonce": nonceHex,
		"X-AtomCode-Alg":   "1",
		"X-AtomCode-Ver":   clientVersion,
	}, nil
}

func extractUsage(data []byte) tokenUsage {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return tokenUsage{}
	}
	if raw := envelope["usage"]; raw != nil {
		return parseUsageObject(raw)
	}
	if raw := envelope["response"]; raw != nil {
		var response map[string]json.RawMessage
		if json.Unmarshal(raw, &response) == nil && response["usage"] != nil {
			return parseUsageObject(response["usage"])
		}
	}
	return tokenUsage{}
}

func parseUsageObject(raw json.RawMessage) tokenUsage {
	var usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		InputTokens      int64 `json:"input_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
		PromptDetails    struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		InputDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		CompletionDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		OutputDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	}
	if json.Unmarshal(raw, &usage) != nil {
		return tokenUsage{}
	}
	input := usage.PromptTokens
	if input == 0 {
		input = usage.InputTokens
	}
	output := usage.CompletionTokens
	if output == 0 {
		output = usage.OutputTokens
	}
	cached := usage.PromptDetails.CachedTokens
	if cached == 0 {
		cached = usage.InputDetails.CachedTokens
	}
	reasoning := usage.CompletionDetails.ReasoningTokens
	if reasoning == 0 {
		reasoning = usage.OutputDetails.ReasoningTokens
	}
	return tokenUsage{Input: input, Output: output, Cached: cached, Reasoning: reasoning}
}

func rewriteResponseModel(data []byte, model string) []byte {
	if model == "" {
		return data
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return data
	}
	encodedModel, _ := json.Marshal(model)
	changed := false
	if _, exists := envelope["model"]; exists {
		envelope["model"] = encodedModel
		changed = true
	}
	if raw := envelope["response"]; raw != nil {
		var response map[string]json.RawMessage
		if json.Unmarshal(raw, &response) == nil {
			if _, exists := response["model"]; exists {
				response["model"] = encodedModel
				envelope["response"], _ = json.Marshal(response)
				changed = true
			}
		}
	}
	if !changed {
		return data
	}
	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return data
	}
	return rewritten
}

func joinUpstreamURL(base, requestPath string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("account model has an invalid upstream URL")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	path := requestPath
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	parsed.Path = basePath + "/" + strings.TrimLeft(path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func isAtomGitGateway(endpoint *url.URL) bool {
	if endpoint == nil || endpoint.Scheme != "https" {
		return false
	}
	switch strings.ToLower(endpoint.Hostname()) {
	case "llm-api.atomgit.com", "pre-llm-api-cce.atomgit.com", "api-ai.gitcode.com":
		return true
	default:
		return false
	}
}

func copyUpstreamHeaders(destination, source http.Header) {
	for _, name := range []string{"Content-Type", "Cache-Control", "Retry-After", "OpenAI-Request-ID", "X-Request-ID"} {
		for _, value := range source.Values(name) {
			destination.Add(name, value)
		}
	}
}

func auditHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	sanitized := make(map[string][]string, len(headers))
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "" {
			continue
		}
		if isSensitiveAuditHeader(canonical) {
			sanitized[canonical] = []string{"[REDACTED]"}
			continue
		}
		sanitized[canonical] = append([]string(nil), values...)
	}
	return sanitized
}

func isSensitiveAuditHeader(name string) bool {
	lower := strings.ToLower(name)
	if lower == "authorization" || lower == "proxy-authorization" || lower == "cookie" || lower == "set-cookie" {
		return true
	}
	return strings.Contains(lower, "api-key") || strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "credential") || strings.Contains(lower, "password") ||
		strings.Contains(lower, "access-key") || strings.Contains(lower, "auth-key") ||
		strings.Contains(lower, "private-key") || strings.Contains(lower, "signature") ||
		strings.HasSuffix(lower, "-sig")
}

func isHopByHop(name string) bool {
	switch name {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": message, "type": code, "param": nil, "code": code,
	}})
}

func (p *Proxy) record(record UsageRecord) {
	if err := p.store.RecordUsage(record); err != nil {
		log.Printf("record usage: %v", err)
	}
}
