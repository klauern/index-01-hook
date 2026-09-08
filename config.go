package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const defaultMaxBodyBytes int64 = 64 << 20

// Webhook tokens must have enough material to resist online guessing. Operators
// should generate tokens with a cryptographically secure random source.
const minWebhookTokenBytes = 32

type Config struct {
	AllowLegacyWebhookToken  bool
	EvaluationRetention      time.Duration
	EvaluationPollInterval   time.Duration
	Token                    string
	DBPath                   string
	ListenAddr               string
	MaxBodyBytes             int64
	DeepSeekToken            string
	DeepSeekModel            string
	TimeZone                 string
	TickTickToken            string
	TickTickDefaultProjectID string
	TickTickNoteProjectID    string
	TickTickProjectAliases   map[string]string
	WorkerOwner              string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		Token:                    getenv("INDEX01_WEBHOOK_TOKEN"),
		DBPath:                   getenv("INDEX01_DB_PATH"),
		ListenAddr:               getenv("INDEX01_LISTEN_ADDR"),
		MaxBodyBytes:             defaultMaxBodyBytes,
		DeepSeekToken:            getenv("INDEX01_DEEPSEEK_TOKEN"),
		DeepSeekModel:            defaultDeepSeekModel,
		TimeZone:                 defaultDeepSeekTimeZone,
		TickTickToken:            getenv("INDEX01_TICKTICK_TOKEN"),
		TickTickDefaultProjectID: getenv("INDEX01_TICKTICK_DEFAULT_PROJECT_ID"),
		TickTickNoteProjectID:    getenv("INDEX01_TICKTICK_NOTE_PROJECT_ID"),
		WorkerOwner:              getenv("INDEX01_WORKER_OWNER"),
	}
	if raw := getenv("INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN"); raw != "" {
		legacy, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN must be a boolean")
		}
		cfg.AllowLegacyWebhookToken = legacy
	}
	if err := validateWebhookTokenWithLegacy(cfg.Token, cfg.AllowLegacyWebhookToken); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.DeepSeekToken) == "" {
		return Config{}, fmt.Errorf("INDEX01_DEEPSEEK_TOKEN is required")
	}
	rawModel := getenv("INDEX01_DEEPSEEK_MODEL")
	if rawModel != "" && strings.TrimSpace(rawModel) == "" {
		return Config{}, fmt.Errorf("INDEX01_DEEPSEEK_MODEL must not be blank")
	}
	if rawModel != "" {
		cfg.DeepSeekModel = strings.TrimSpace(rawModel)
	}
	if !safeProviderIdentifier(cfg.DeepSeekModel) {
		return Config{}, fmt.Errorf("INDEX01_DEEPSEEK_MODEL is invalid")
	}
	rawTimeZone := getenv("INDEX01_TIME_ZONE")
	if rawTimeZone != "" && strings.TrimSpace(rawTimeZone) == "" {
		return Config{}, fmt.Errorf("INDEX01_TIME_ZONE must not be blank")
	}
	if rawTimeZone != "" {
		cfg.TimeZone = strings.TrimSpace(rawTimeZone)
	}
	normalizedTimeZone, err := normalizeTimeZone(cfg.TimeZone)
	if err != nil {
		return Config{}, fmt.Errorf("INDEX01_TIME_ZONE is invalid")
	}
	cfg.TimeZone = normalizedTimeZone
	if raw := getenv("INDEX01_EVALUATION_RETENTION_DAYS"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 || days > 365 {
			return Config{}, fmt.Errorf("INDEX01_EVALUATION_RETENTION_DAYS must be an integer from 0 to 365")
		}
		cfg.EvaluationRetention = time.Duration(days) * 24 * time.Hour
	}
	if cfg.EvaluationRetention > 0 {
		cfg.EvaluationPollInterval = 6 * time.Hour
	}
	if raw := getenv("INDEX01_EVALUATION_POLL_INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || (interval != 0 && (interval < time.Hour || interval > 7*24*time.Hour)) {
			return Config{}, fmt.Errorf("INDEX01_EVALUATION_POLL_INTERVAL must be 0 or a duration from 1h to 168h")
		}
		cfg.EvaluationPollInterval = interval
	}
	if strings.TrimSpace(cfg.TickTickToken) == "" {
		return Config{}, fmt.Errorf("INDEX01_TICKTICK_TOKEN is required")
	}
	if strings.TrimSpace(cfg.TickTickDefaultProjectID) == "" {
		return Config{}, fmt.Errorf("INDEX01_TICKTICK_DEFAULT_PROJECT_ID is required")
	}
	if strings.TrimSpace(cfg.TickTickNoteProjectID) == "" {
		return Config{}, fmt.Errorf("INDEX01_TICKTICK_NOTE_PROJECT_ID is required")
	}
	if strings.TrimSpace(cfg.WorkerOwner) == "" {
		cfg.WorkerOwner = "index-01-hook"
	}
	aliases, err := parseProjectAliases(getenv("INDEX01_TICKTICK_PROJECT_ALIASES"))
	if err != nil {
		return Config{}, err
	}
	cfg.TickTickProjectAliases = aliases
	cfg.DBPath, err = normalizeDatabasePath(cfg.DBPath)
	if err != nil {
		return Config{}, err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	} else if strings.TrimSpace(cfg.ListenAddr) == "" {
		return Config{}, fmt.Errorf("INDEX01_LISTEN_ADDR must not be blank")
	}
	if raw := getenv("INDEX01_MAX_BODY_BYTES"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("INDEX01_MAX_BODY_BYTES must be a positive integer")
		}
		cfg.MaxBodyBytes = value
	}
	return cfg, nil
}

func validateWebhookToken(token string) error {
	return validateWebhookTokenWithLegacy(token, false)
}

// Legacy mode preserves an existing sender credential during a coordinated upgrade.
// New deployments retain the default 32-byte requirement.
func validateWebhookTokenWithLegacy(token string, legacy bool) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("INDEX01_WEBHOOK_TOKEN is required")
	}
	minimum := minWebhookTokenBytes
	if legacy {
		minimum = 14
	}
	if len(token) < minimum {
		return fmt.Errorf("INDEX01_WEBHOOK_TOKEN must be at least %d bytes", minimum)
	}
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return fmt.Errorf("INDEX01_WEBHOOK_TOKEN must not contain whitespace")
	}
	return nil
}

func normalizeTimeZone(value string) (string, error) {
	timeZone := strings.TrimSpace(value)
	if timeZone == "" || timeZone == "Local" {
		return "", fmt.Errorf("time zone is invalid")
	}
	if _, err := time.LoadLocation(timeZone); err != nil {
		return "", fmt.Errorf("time zone is invalid")
	}
	return timeZone, nil
}

func normalizeDatabasePath(path string) (string, error) {
	if path == "" {
		return "./index01.db", nil
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("INDEX01_DB_PATH must not be blank")
	}
	return path, nil
}

func parseProjectAliases(raw string) (map[string]string, error) {
	aliases := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return aliases, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&aliases); err != nil || aliases == nil {
		return nil, fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIASES must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIASES must contain one JSON object")
	}
	normalized := make(map[string]string, len(aliases))
	for rawAlias, rawProjectID := range aliases {
		alias := strings.ToLower(strings.TrimSpace(rawAlias))
		projectID := strings.TrimSpace(rawProjectID)
		if alias == "" || projectID == "" {
			return nil, fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIASES contains a blank alias or project")
		}
		if _, exists := normalized[alias]; exists {
			return nil, fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIASES contains duplicate normalized aliases")
		}
		normalized[alias] = projectID
	}
	return normalized, nil
}
