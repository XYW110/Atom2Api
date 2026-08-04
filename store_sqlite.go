package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	sqliteSchemaVersion = 1
	maxUsageRecords     = 50000
	legacyMigrationKey  = "legacy_files_migrated_v1"
	sqliteTimestamp     = "2006-01-02T15:04:05.999999999Z07:00"
)

var sqliteSchema = []string{
	`CREATE TABLE IF NOT EXISTS atom2api_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS atom2api_accounts (
		id TEXT PRIMARY KEY,
		sort_order INTEGER NOT NULL,
		data BLOB NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS atom2api_api_keys (
		id TEXT PRIMARY KEY,
		sort_order INTEGER NOT NULL,
		data BLOB NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS atom2api_model_settings (
		upstream TEXT PRIMARY KEY,
		data BLOB NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS atom2api_plan_claim_logs (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL,
		data BLOB NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS atom2api_usage_records (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL,
		timestamp TEXT NOT NULL,
		data BLOB NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS atom2api_usage_timestamp_idx
		ON atom2api_usage_records(timestamp)`,
}

func newSQLiteStore(path string, config *ConfigManager) (*Store, error) {
	databasePath, legacyPath := storePaths(path)
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open SQLite data store: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{
		path: databasePath, legacyPath: legacyPath, usagePath: legacyPath + ".usage.ndjson",
		db: db, config: config,
		state: persistedState{Version: stateVersion, ModelSettings: map[string]ModelSetting{}},
	}
	if err := store.initializeSQLite(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrateLegacyFiles(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.loadSQLite(); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure SQLite data store: %w", err)
	}
	return store, nil
}

func storePaths(path string) (databasePath, legacyPath string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultDataPath
	}
	if strings.EqualFold(filepath.Ext(path), ".db") {
		return path, strings.TrimSuffix(path, filepath.Ext(path)) + ".json"
	}
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return strings.TrimSuffix(path, filepath.Ext(path)) + ".db", path
	}
	return path + ".db", path
}

func (s *Store) initializeSQLite() error {
	for _, pragma := range []string{
		"PRAGMA journal_mode=DELETE",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := s.db.Exec(pragma); err != nil {
			return fmt.Errorf("configure SQLite data store: %w", err)
		}
	}
	for _, statement := range sqliteSchema {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize SQLite schema: %w", err)
		}
	}
	var version string
	err := s.db.QueryRow(`SELECT value FROM atom2api_meta WHERE key = 'schema_version'`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.Exec(`INSERT INTO atom2api_meta(key, value) VALUES ('schema_version', ?)`, strconv.Itoa(sqliteSchemaVersion))
		if err != nil {
			return fmt.Errorf("record SQLite schema version: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read SQLite schema version: %w", err)
	}
	if version != strconv.Itoa(sqliteSchemaVersion) {
		return fmt.Errorf("unsupported SQLite schema version %s", version)
	}
	return nil
}

func (s *Store) migrateLegacyFiles() error {
	legacyExists, err := fileExists(s.legacyPath)
	if err != nil {
		return fmt.Errorf("inspect legacy data store: %w", err)
	}
	usageExists, err := fileExists(s.usagePath)
	if err != nil {
		return fmt.Errorf("inspect legacy usage log: %w", err)
	}
	if !legacyExists && !usageExists {
		return nil
	}

	var completed string
	err = s.db.QueryRow(`SELECT value FROM atom2api_meta WHERE key = ?`, legacyMigrationKey).Scan(&completed)
	if err == nil {
		return removeLegacyFiles(s.legacyPath, s.usagePath)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check legacy migration status: %w", err)
	}

	empty, err := s.sqliteIsEmpty()
	if err != nil {
		return err
	}
	if !empty {
		return errors.New("legacy data files and populated SQLite data both exist; refusing an ambiguous migration")
	}
	state, err := loadLegacyState(s.legacyPath, s.usagePath, legacyExists, usageExists)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin legacy data migration: %w", err)
	}
	defer tx.Rollback()
	if err := writeCoreStateTx(tx, state); err != nil {
		return fmt.Errorf("migrate legacy state: %w", err)
	}
	for _, record := range state.Usage {
		if err := insertUsageTx(tx, record); err != nil {
			return fmt.Errorf("migrate legacy usage: %w", err)
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO atom2api_meta(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		legacyMigrationKey, s.legacyPath,
	); err != nil {
		return fmt.Errorf("record legacy data migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy data migration: %w", err)
	}
	if err := removeLegacyFiles(s.legacyPath, s.usagePath); err != nil {
		return err
	}
	return nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func removeLegacyFiles(paths ...string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove migrated legacy file %s: %w", path, err)
		}
	}
	return nil
}

func (s *Store) sqliteIsEmpty() (bool, error) {
	for _, table := range []string{
		"atom2api_accounts", "atom2api_api_keys", "atom2api_model_settings",
		"atom2api_plan_claim_logs", "atom2api_usage_records",
	} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			return false, fmt.Errorf("count SQLite table %s: %w", table, err)
		}
		if count != 0 {
			return false, nil
		}
	}
	return true, nil
}

func loadLegacyState(statePath, usagePath string, stateExists, usageExists bool) (persistedState, error) {
	state := persistedState{Version: stateVersion, ModelSettings: map[string]ModelSetting{}}
	if stateExists {
		data, err := os.ReadFile(statePath)
		if err != nil {
			return persistedState{}, fmt.Errorf("read legacy data store: %w", err)
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return persistedState{}, fmt.Errorf("decode legacy data store: %w", err)
		}
		if state.Version != stateVersion {
			return persistedState{}, fmt.Errorf("unsupported legacy data store version %d", state.Version)
		}
		if state.ModelSettings == nil {
			state.ModelSettings = map[string]ModelSetting{}
		}
	}
	if usageExists {
		file, err := os.Open(usagePath)
		if err != nil {
			return persistedState{}, fmt.Errorf("open legacy usage log: %w", err)
		}
		scanner := bufio.NewScanner(file)
		// Audit entries can contain the full proxied request and response bodies.
		scanner.Buffer(make([]byte, 64<<10), 128<<20)
		for scanner.Scan() {
			var record UsageRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				file.Close()
				return persistedState{}, fmt.Errorf("decode legacy usage log: %w", err)
			}
			state.Usage = append(state.Usage, record)
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			return persistedState{}, fmt.Errorf("read legacy usage log: %w", err)
		}
		if err := file.Close(); err != nil {
			return persistedState{}, fmt.Errorf("close legacy usage log: %w", err)
		}
	}
	if len(state.Usage) > maxUsageRecords {
		state.Usage = append([]UsageRecord(nil), state.Usage[len(state.Usage)-maxUsageRecords:]...)
	}
	return state, nil
}

func (s *Store) loadSQLite() error {
	accounts, err := loadJSONRows[Account](s.db, `SELECT data FROM atom2api_accounts ORDER BY sort_order`)
	if err != nil {
		return fmt.Errorf("load accounts from SQLite: %w", err)
	}
	apiKeys, err := loadJSONRows[APIKey](s.db, `SELECT data FROM atom2api_api_keys ORDER BY sort_order`)
	if err != nil {
		return fmt.Errorf("load API keys from SQLite: %w", err)
	}
	settings, err := loadJSONRows[ModelSetting](s.db, `SELECT data FROM atom2api_model_settings ORDER BY upstream`)
	if err != nil {
		return fmt.Errorf("load model settings from SQLite: %w", err)
	}
	claimLogs, err := loadJSONRows[PlanClaimLog](s.db, `SELECT data FROM atom2api_plan_claim_logs ORDER BY seq`)
	if err != nil {
		return fmt.Errorf("load plan claim logs from SQLite: %w", err)
	}
	usage, err := loadJSONRows[UsageRecord](s.db, `
		SELECT data FROM (
			SELECT seq, data FROM atom2api_usage_records ORDER BY seq DESC LIMIT 50000
		) ORDER BY seq`)
	if err != nil {
		return fmt.Errorf("load usage records from SQLite: %w", err)
	}
	s.state = persistedState{
		Version: stateVersion, Accounts: accounts, APIKeys: apiKeys,
		ModelSettings: make(map[string]ModelSetting, len(settings)),
		PlanClaimLogs: claimLogs, Usage: usage,
	}
	for _, setting := range settings {
		s.state.ModelSettings[setting.Upstream] = setting
	}
	return nil
}

func loadJSONRows[T any](db *sql.DB, query string) ([]T, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []T
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var value T
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) saveLocked() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin SQLite state update: %w", err)
	}
	defer tx.Rollback()
	if err := writeCoreStateTx(tx, s.state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite state update: %w", err)
	}
	return nil
}

func writeCoreStateTx(tx *sql.Tx, state persistedState) error {
	for _, table := range []string{
		"atom2api_accounts", "atom2api_api_keys", "atom2api_model_settings", "atom2api_plan_claim_logs",
	} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return fmt.Errorf("clear SQLite table %s: %w", table, err)
		}
	}
	for index, account := range state.Accounts {
		if err := insertJSON(tx, `INSERT INTO atom2api_accounts(id, sort_order, data) VALUES (?, ?, ?)`, account.ID, index, account); err != nil {
			return fmt.Errorf("save account %s: %w", account.ID, err)
		}
	}
	for index, key := range state.APIKeys {
		if err := insertJSON(tx, `INSERT INTO atom2api_api_keys(id, sort_order, data) VALUES (?, ?, ?)`, key.ID, index, key); err != nil {
			return fmt.Errorf("save API key %s: %w", key.ID, err)
		}
	}
	for upstream, setting := range state.ModelSettings {
		if err := insertJSON(tx, `INSERT INTO atom2api_model_settings(upstream, data) VALUES (?, ?)`, upstream, setting); err != nil {
			return fmt.Errorf("save model setting %s: %w", upstream, err)
		}
	}
	for _, entry := range state.PlanClaimLogs {
		if err := insertJSON(tx, `INSERT INTO atom2api_plan_claim_logs(id, data) VALUES (?, ?)`, entry.ID, entry); err != nil {
			return fmt.Errorf("save plan claim log %s: %w", entry.ID, err)
		}
	}
	return nil
}

func insertJSON(tx *sql.Tx, query string, args ...any) error {
	value := args[len(args)-1]
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	args[len(args)-1] = data
	_, err = tx.Exec(query, args...)
	return err
}

func insertUsageTx(tx *sql.Tx, record UsageRecord) error {
	return insertJSON(tx,
		`INSERT INTO atom2api_usage_records(id, timestamp, data) VALUES (?, ?, ?)`,
		record.ID, record.Timestamp.UTC().Format(sqliteTimestamp), record,
	)
}

func (s *Store) DeleteUsageRecordsBefore(cutoff time.Time) (int, error) {
	cutoff = cutoff.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := make([]UsageRecord, 0, len(s.state.Usage))
	deleted := 0
	for _, record := range s.state.Usage {
		if record.Timestamp.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, record)
	}
	if deleted == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin usage record cleanup: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM atom2api_usage_records WHERE timestamp < ?`, cutoff.Format(sqliteTimestamp)); err != nil {
		return 0, fmt.Errorf("delete usage records: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit usage record cleanup: %w", err)
	}
	s.state.Usage = kept
	return deleted, nil
}

func (s *Store) ClearUsageDetailsBefore(cutoff time.Time) (int, error) {
	cutoff = cutoff.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	updated := append([]UsageRecord(nil), s.state.Usage...)
	changed := make([]UsageRecord, 0)
	for index := range updated {
		record := &updated[index]
		if !record.Timestamp.Before(cutoff) || !usageRecordHasDetails(*record) {
			continue
		}
		clearUsageRecordDetails(record)
		changed = append(changed, *record)
	}
	if len(changed) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin usage detail cleanup: %w", err)
	}
	defer tx.Rollback()
	for _, record := range changed {
		data, err := json.Marshal(record)
		if err != nil {
			return 0, fmt.Errorf("encode usage record %s: %w", record.ID, err)
		}
		if _, err := tx.Exec(
			`UPDATE atom2api_usage_records SET data = ? WHERE id = ? AND timestamp = ?`,
			data, record.ID, record.Timestamp.UTC().Format(sqliteTimestamp),
		); err != nil {
			return 0, fmt.Errorf("clear usage record %s details: %w", record.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit usage detail cleanup: %w", err)
	}
	s.state.Usage = updated
	return len(changed), nil
}

func usageRecordHasDetails(record UsageRecord) bool {
	return record.Error != "" || record.RequestBody != "" || record.ResponseBody != "" ||
		len(record.RequestHeaders) > 0 || len(record.ResponseHeaders) > 0 || len(record.Attempts) > 0
}

func clearUsageRecordDetails(record *UsageRecord) {
	record.Error = ""
	record.RequestBody = ""
	record.ResponseBody = ""
	record.RequestHeaders = nil
	record.ResponseHeaders = nil
	record.Attempts = nil
}

func (s *Store) saveUsageLocked(record UsageRecord, compact bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin SQLite usage update: %w", err)
	}
	defer tx.Rollback()
	if err := insertUsageTx(tx, record); err != nil {
		return fmt.Errorf("save usage record: %w", err)
	}
	if err := writeCoreStateTx(tx, s.state); err != nil {
		return err
	}
	if compact {
		if _, err := tx.Exec(`DELETE FROM atom2api_usage_records WHERE seq NOT IN (
			SELECT seq FROM atom2api_usage_records ORDER BY seq DESC LIMIT 50000
		)`); err != nil {
			return fmt.Errorf("compact SQLite usage records: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite usage update: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
