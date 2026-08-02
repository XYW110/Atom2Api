package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultConfigPath       = "config.json"
	defaultUserAgent        = "atomcode/5.0.2"
	defaultListenAddress    = ":8080"
	defaultDataPath         = "data/atom2api.db"
	defaultAdminPassword    = "atom2api"
	defaultPlatformBaseURL  = "https://acs.atomgit.com"
	defaultCodingPlanAPIURL = "https://api.gitcode.com/api/v5"
	defaultGatewayURL       = "https://llm-api.atomgit.com/v1"
)

type Config struct {
	UserAgent          string `json:"user_agent"`
	ListenAddress      string `json:"listen_address"`
	DataPath           string `json:"data_path"`
	AdminPassword      string `json:"admin_password"`
	EncryptionKey      string `json:"encryption_key"`
	PlatformBaseURL    string `json:"platform_base_url"`
	CodingPlanAPIURL   string `json:"codingplan_api_url"`
	GatewayURL         string `json:"gateway_url"`
	SignerURL          string `json:"signer_url,omitempty"`
	SignerToken        string `json:"signer_token,omitempty"`
	AuditDebugEnabled  bool   `json:"audit_debug_enabled"`
	RequestTimeoutSecs int    `json:"request_timeout_seconds"`
}

type ConfigSnapshot struct {
	Config
	LoadedAt time.Time
}

type ConfigManager struct {
	path string

	mu       sync.RWMutex
	config   Config
	fileHash [sha256.Size]byte
	loadedAt time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	done      chan struct{}
}

func NewConfigManager(configPath string) (*ConfigManager, error) {
	if strings.TrimSpace(configPath) == "" {
		configPath = defaultConfigPath
	}

	manager := &ConfigManager{
		path: configPath,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		config, err := defaultConfig()
		if err != nil {
			return nil, err
		}
		if err := manager.store(config); err != nil {
			return nil, fmt.Errorf("create default config: %w", err)
		}
		return manager, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	config, normalized, err := parseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if normalized {
		if err := manager.store(config); err != nil {
			return nil, fmt.Errorf("upgrade config: %w", err)
		}
		return manager, nil
	}
	manager.config = config
	manager.fileHash = sha256.Sum256(data)
	manager.loadedAt = time.Now()
	return manager, nil
}

func defaultConfig() (Config, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return Config{}, fmt.Errorf("generate encryption key: %w", err)
	}
	config := Config{
		UserAgent:          defaultUserAgent,
		ListenAddress:      defaultListenAddress,
		DataPath:           defaultDataPath,
		AdminPassword:      defaultAdminPassword,
		EncryptionKey:      base64.RawURLEncoding.EncodeToString(key),
		PlatformBaseURL:    defaultPlatformBaseURL,
		CodingPlanAPIURL:   defaultCodingPlanAPIURL,
		GatewayURL:         defaultGatewayURL,
		RequestTimeoutSecs: 120,
	}
	if _, err := normalizeConfig(&config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func normalizeConfig(config *Config) (bool, error) {
	changed := false
	setString := func(target *string, value string) {
		if strings.TrimSpace(*target) == "" {
			*target = value
			changed = true
		}
	}
	setString(&config.UserAgent, defaultUserAgent)
	setString(&config.ListenAddress, defaultListenAddress)
	setString(&config.DataPath, defaultDataPath)
	if strings.EqualFold(filepath.Ext(config.DataPath), ".json") {
		config.DataPath = strings.TrimSuffix(config.DataPath, filepath.Ext(config.DataPath)) + ".db"
		changed = true
	}
	setString(&config.AdminPassword, defaultAdminPassword)
	setString(&config.PlatformBaseURL, defaultPlatformBaseURL)
	setString(&config.CodingPlanAPIURL, defaultCodingPlanAPIURL)
	setString(&config.GatewayURL, defaultGatewayURL)
	normalizedPassword, passwordChanged, err := normalizeAdminPassword(config.AdminPassword)
	if err != nil {
		return false, err
	}
	config.AdminPassword = normalizedPassword
	changed = changed || passwordChanged
	if config.RequestTimeoutSecs == 0 {
		config.RequestTimeoutSecs = 120
		changed = true
	}
	if strings.TrimSpace(config.EncryptionKey) == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return false, fmt.Errorf("generate encryption key: %w", err)
		}
		config.EncryptionKey = base64.RawURLEncoding.EncodeToString(key)
		changed = true
	}
	return changed, nil
}

func (m *ConfigManager) Start(interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	m.startOnce.Do(func() {
		go func() {
			defer close(m.done)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			lastError := ""
			for {
				select {
				case <-ticker.C:
					changed, err := m.Reload()
					if err != nil {
						if message := err.Error(); message != lastError {
							log.Printf("reload config: %v", err)
							lastError = message
						}
						continue
					}
					lastError = ""
					if changed {
						log.Printf("configuration reloaded from %s", m.path)
					}
				case <-m.stop:
					return
				}
			}
		}()
	})
}

func (m *ConfigManager) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
	}
}

func (m *ConfigManager) Snapshot() ConfigSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return ConfigSnapshot{Config: m.config, LoadedAt: m.loadedAt}
}

func (m *ConfigManager) Reload() (bool, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", m.path, err)
	}

	hash := sha256.Sum256(data)
	m.mu.RLock()
	unchanged := hash == m.fileHash
	m.mu.RUnlock()
	if unchanged {
		return false, nil
	}

	config, normalized, err := parseConfig(data)
	if err != nil {
		return false, err
	}
	if normalized {
		if err := m.store(config); err != nil {
			return false, err
		}
		return true, nil
	}

	m.mu.Lock()
	m.config = config
	m.fileHash = hash
	m.loadedAt = time.Now()
	m.mu.Unlock()
	return true, nil
}

func (m *ConfigManager) Update(config Config) error {
	if strings.TrimSpace(config.UserAgent) == "" {
		return errors.New("user_agent must not be empty")
	}
	if _, err := normalizeConfig(&config); err != nil {
		return err
	}
	if err := validateConfig(config); err != nil {
		return err
	}
	return m.store(config)
}

func (m *ConfigManager) store(config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	directory := filepath.Dir(m.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".atom2api-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := replaceFile(temporaryPath, m.path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}

	m.mu.Lock()
	m.config = config
	m.fileHash = sha256.Sum256(data)
	m.loadedAt = time.Now()
	m.mu.Unlock()
	return nil
}

func parseConfig(data []byte) (Config, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, false, fmt.Errorf("decode config: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Config{}, false, fmt.Errorf("decode config: %w", err)
	}
	if strings.TrimSpace(config.UserAgent) == "" {
		return Config{}, false, errors.New("user_agent must not be empty")
	}
	changed, err := normalizeConfig(&config)
	if err != nil {
		return Config{}, false, err
	}
	if err := validateConfig(config); err != nil {
		return Config{}, false, err
	}
	return config, changed, nil
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.UserAgent) == "" {
		return errors.New("user_agent must not be empty")
	}
	if len(config.UserAgent) > 512 {
		return errors.New("user_agent must not exceed 512 bytes")
	}
	for _, character := range config.UserAgent {
		if character < 0x20 || character == 0x7f {
			return errors.New("user_agent contains a control character")
		}
	}
	if !adminPasswordIsHash(config.AdminPassword) {
		return errors.New("admin_password must be a bcrypt hash")
	}
	if config.RequestTimeoutSecs < 5 || config.RequestTimeoutSecs > 600 {
		return errors.New("request_timeout_seconds must be between 5 and 600")
	}
	if _, err := encryptionKey(config.EncryptionKey); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"platform_base_url":  config.PlatformBaseURL,
		"codingplan_api_url": config.CodingPlanAPIURL,
		"gateway_url":        config.GatewayURL,
	} {
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute URL", name)
		}
	}
	if config.SignerURL != "" {
		parsed, err := url.ParseRequestURI(config.SignerURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return errors.New("signer_url must be an absolute URL")
		}
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
			return errors.New("signer_url must use HTTPS unless it targets a loopback address")
		}
	}
	return nil
}

func encryptionKey(encoded string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("encryption_key must be a base64url-encoded 32-byte key")
	}
	return key, nil
}

func replaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}
