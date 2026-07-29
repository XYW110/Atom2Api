package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"
)

type settingsResponse struct {
	UserAgent          string    `json:"user_agent"`
	ListenAddress      string    `json:"listen_address"`
	DataPath           string    `json:"data_path"`
	PlatformBaseURL    string    `json:"platform_base_url"`
	CodingPlanAPIURL   string    `json:"codingplan_api_url"`
	GatewayURL         string    `json:"gateway_url"`
	SignerURL          string    `json:"signer_url"`
	SignerConfigured   bool      `json:"signer_configured"`
	AuditDebugEnabled  bool      `json:"audit_debug_enabled"`
	RequestTimeoutSecs int       `json:"request_timeout_seconds"`
	DefaultPassword    bool      `json:"default_password"`
	LoadedAt           time.Time `json:"loaded_at"`
}

type updateSettingsRequest struct {
	UserAgent          *string `json:"user_agent"`
	PlatformBaseURL    *string `json:"platform_base_url"`
	CodingPlanAPIURL   *string `json:"codingplan_api_url"`
	GatewayURL         *string `json:"gateway_url"`
	SignerURL          *string `json:"signer_url"`
	SignerToken        *string `json:"signer_token"`
	AdminPassword      *string `json:"admin_password"`
	AuditDebugEnabled  *bool   `json:"audit_debug_enabled"`
	RequestTimeoutSecs *int    `json:"request_timeout_seconds"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func handleGetSettings(config *ConfigManager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeSettingsResponse(w, http.StatusOK, config.Snapshot())
	}
}

func handleUpdateSettings(config *ConfigManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()

		var request updateSettingsRequest
		if err := decoder.Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
		if err := ensureJSONEOF(decoder); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}

		updated := config.Snapshot().Config
		if request.UserAgent != nil {
			updated.UserAgent = *request.UserAgent
		}
		if request.PlatformBaseURL != nil {
			updated.PlatformBaseURL = *request.PlatformBaseURL
		}
		if request.CodingPlanAPIURL != nil {
			updated.CodingPlanAPIURL = *request.CodingPlanAPIURL
		}
		if request.GatewayURL != nil {
			updated.GatewayURL = *request.GatewayURL
		}
		if request.SignerURL != nil {
			updated.SignerURL = *request.SignerURL
		}
		if request.SignerToken != nil {
			updated.SignerToken = *request.SignerToken
		}
		if request.AdminPassword != nil {
			updated.AdminPassword = *request.AdminPassword
		}
		if request.AuditDebugEnabled != nil {
			updated.AuditDebugEnabled = *request.AuditDebugEnabled
		}
		if request.RequestTimeoutSecs != nil {
			updated.RequestTimeoutSecs = *request.RequestTimeoutSecs
		}
		if err := config.Update(updated); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}

		writeSettingsResponse(w, http.StatusOK, config.Snapshot())
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var value any
	err := decoder.Decode(&value)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func writeSettingsResponse(w http.ResponseWriter, status int, snapshot ConfigSnapshot) {
	writeJSON(w, status, settingsResponse{
		UserAgent: snapshot.UserAgent, ListenAddress: snapshot.ListenAddress, DataPath: snapshot.DataPath,
		PlatformBaseURL: snapshot.PlatformBaseURL, CodingPlanAPIURL: snapshot.CodingPlanAPIURL,
		GatewayURL: snapshot.GatewayURL, SignerURL: snapshot.SignerURL,
		SignerConfigured: true, AuditDebugEnabled: snapshot.AuditDebugEnabled,
		RequestTimeoutSecs: snapshot.RequestTimeoutSecs,
		DefaultPassword:    snapshot.AdminPassword == defaultAdminPassword, LoadedAt: snapshot.LoadedAt,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode JSON response: %v", err)
	}
}
