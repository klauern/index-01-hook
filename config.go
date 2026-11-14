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
	AllowLegacyWebhookToken   bool
	EvaluationRetention       time.Duration
	EvaluationPollInterval    time.Duration
	Token                     string
	DBPath                    string
	ListenAddr                string
	MaxBodyBytes              int64
	DeepSeekToken             string
	DeepSeekModel             string
	TypeSafeToken             string
	TypeSafeModel             string
	TypeSafeEndpoint          string
	TypeSafeVerify            bool
	TypeSafeShadow            bool
	TimeZone                  string
	TickTickToken             string
	TickTickDefaultProjectID  string
	TickTickNoteProjectID     string
	TickTickProjectAliases    map[string]string
	TypeSafeAliasDescriptions map[string]string
	WorkerOwner               string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		Token:                    getenv("INDEX01_WEBHOOK_TOKEN"),
		DBPath:                   getenv("INDEX01_DB_PATH"),
		ListenAddr:               getenv("INDEX01_LISTEN_ADDR"),
		MaxBodyBytes:             defaultMaxBodyBytes,
		DeepSeekToken:            getenv("INDEX01_DEEPSEEK_TOKEN"),
		TypeSafeToken:            getenv("INDEX01_TYPESAFE_TOKEN"),
		DeepSeekModel:            defaultDeepSeekModel,
		TypeSafeModel:            defaultTypeSafeModel,
		TypeSafeEndpoint:         typeSafeAPIEndpoint,
		TimeZone:                 defaultDeepSeekTimeZone,
		TickTickToken:            getenv("INDEX01_TICKTICK_TOKEN"),
		TickTickDefaultProjectID: getenv("INDEX01_TICKTICK_DEFAULT_PROJECT_ID"),
		TickTickNoteProjectID:    getenv("INDEX01_TICKTICK_NOTE_PROJECT_ID"),
		WorkerOwner:              getenv("INDEX01_WORKER_OWNER"),
	}
	stages := []func(func(string) string) error{
		cfg.loadVerification,
		cfg.loadTypeSafeProvider,
		cfg.loadDeepSeekProvider,
		cfg.loadTimeZone,
		cfg.loadEvaluationRetention,
		cfg.loadEvaluationPolling,
		cfg.loadTickTickRouting,
		cfg.loadRoutingDescriptions,
		cfg.loadHTTPStorage,
	}
	for _, stage := range stages {
		if err := stage(getenv); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// configValue binds a supplied value to its diagnostic name.
type configValue struct{ raw, variable string }

// boolean parses an optional environment flag.
func (value configValue) boolean() (bool, error) {
	raw, variable := value.raw, value.variable
	if raw == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", variable)
	}
	return parsed, nil
}

// loadVerification applies authentication flags before provider configuration.
func (cfg *Config) loadVerification(getenv func(string) string) error {
	legacy, err := (configValue{getenv("INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN"), "INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN"}).boolean()
	if err != nil {
		return err
	}
	cfg.AllowLegacyWebhookToken = legacy
	if err := rejectProductionVerification(getenv("INDEX01_TYPESAFE_VERIFY")); err != nil {
		return err
	}
	shadow, err := (configValue{getenv("INDEX01_TYPESAFE_SHADOW"), "INDEX01_TYPESAFE_SHADOW"}).boolean()
	if err != nil {
		return err
	}
	cfg.TypeSafeShadow = shadow
	return cfg.validateVerificationToken()
}

// rejectProductionVerification keeps production verification disabled.
func rejectProductionVerification(raw string) error {
	verify, err := (configValue{raw, "INDEX01_TYPESAFE_VERIFY"}).boolean()
	if err != nil {
		return err
	}
	if verify {
		return fmt.Errorf("INDEX01_TYPESAFE_VERIFY is not approved; use INDEX01_TYPESAFE_SHADOW")
	}
	return nil
}

// verificationEnabled includes shadow and production mode for shared validation.
func (cfg Config) verificationEnabled() bool { return cfg.TypeSafeVerify || cfg.TypeSafeShadow }

// validateVerificationToken rejects missing credentials for enabled modes.
func (cfg Config) validateVerificationToken() error {
	if !cfg.verificationEnabled() {
		return nil
	}
	if strings.TrimSpace(cfg.TypeSafeToken) == "" {
		return fmt.Errorf("INDEX01_TYPESAFE_TOKEN is required when verification is enabled")
	}
	return nil
}

// loadTypeSafeProvider applies one configuration stage without changing validation order.
func (cfg *Config) loadTypeSafeProvider(getenv func(string) string) error {
	if rawModel := getenv("INDEX01_TYPESAFE_MODEL"); rawModel != "" {
		if strings.TrimSpace(rawModel) == "" || !safeProviderIdentifier(strings.TrimSpace(rawModel)) {
			return fmt.Errorf("INDEX01_TYPESAFE_MODEL is invalid")
		}
		cfg.TypeSafeModel = strings.TrimSpace(rawModel)
	}
	if rawEndpoint := getenv("INDEX01_TYPESAFE_ENDPOINT"); rawEndpoint != "" {
		cfg.TypeSafeEndpoint = strings.TrimSpace(rawEndpoint)
	}
	if err := validateTypeSafeEndpoint(cfg.TypeSafeEndpoint); err != nil {
		return fmt.Errorf("INDEX01_TYPESAFE_ENDPOINT is invalid")
	}

	return nil
}

// loadDeepSeekProvider applies one configuration stage without changing validation order.
func (cfg *Config) loadDeepSeekProvider(getenv func(string) string) error {
	if err := validateWebhookTokenWithLegacy(cfg.Token, cfg.AllowLegacyWebhookToken); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.DeepSeekToken) == "" {
		return fmt.Errorf("INDEX01_DEEPSEEK_TOKEN is required")
	}
	rawModel := getenv("INDEX01_DEEPSEEK_MODEL")
	if rawModel != "" && strings.TrimSpace(rawModel) == "" {
		return fmt.Errorf("INDEX01_DEEPSEEK_MODEL must not be blank")
	}
	if rawModel != "" {
		cfg.DeepSeekModel = strings.TrimSpace(rawModel)
	}
	if !safeProviderIdentifier(cfg.DeepSeekModel) {
		return fmt.Errorf("INDEX01_DEEPSEEK_MODEL is invalid")
	}

	return nil
}

// loadTimeZone applies one configuration stage without changing validation order.
func (cfg *Config) loadTimeZone(getenv func(string) string) error {
	rawTimeZone := getenv("INDEX01_TIME_ZONE")
	if rawTimeZone != "" && strings.TrimSpace(rawTimeZone) == "" {
		return fmt.Errorf("INDEX01_TIME_ZONE must not be blank")
	}
	if rawTimeZone != "" {
		cfg.TimeZone = strings.TrimSpace(rawTimeZone)
	}
	normalizedTimeZone, err := normalizeTimeZone(cfg.TimeZone)
	if err != nil {
		return fmt.Errorf("INDEX01_TIME_ZONE is invalid")
	}
	cfg.TimeZone = normalizedTimeZone

	return nil
}

// loadEvaluationRetention applies one configuration stage without changing validation order.
func (cfg *Config) loadEvaluationRetention(getenv func(string) string) error {
	if raw := getenv("INDEX01_EVALUATION_RETENTION_DAYS"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || !validRetentionDays(days) {
			return fmt.Errorf("INDEX01_EVALUATION_RETENTION_DAYS must be an integer from 0 to 365")
		}
		cfg.EvaluationRetention = time.Duration(days) * 24 * time.Hour
	}
	if cfg.TypeSafeShadow && cfg.EvaluationRetention == 0 {
		return fmt.Errorf("INDEX01_EVALUATION_RETENTION_DAYS must be nonzero when INDEX01_TYPESAFE_SHADOW is enabled")
	}

	return nil
}

// loadEvaluationPolling applies one configuration stage without changing validation order.
func (cfg *Config) loadEvaluationPolling(getenv func(string) string) error {
	if cfg.EvaluationRetention > 0 {
		cfg.EvaluationPollInterval = 6 * time.Hour
	}
	if raw := getenv("INDEX01_EVALUATION_POLL_INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || !validEvaluationPollInterval(interval) {
			return fmt.Errorf("INDEX01_EVALUATION_POLL_INTERVAL must be 0 or a duration from 1h to 168h")
		}
		cfg.EvaluationPollInterval = interval
	}

	return nil
}

// loadTickTickRouting applies one configuration stage without changing validation order.
func (cfg *Config) loadTickTickRouting(getenv func(string) string) error {
	if strings.TrimSpace(cfg.TickTickToken) == "" {
		return fmt.Errorf("INDEX01_TICKTICK_TOKEN is required")
	}
	if strings.TrimSpace(cfg.TickTickDefaultProjectID) == "" {
		return fmt.Errorf("INDEX01_TICKTICK_DEFAULT_PROJECT_ID is required")
	}
	if strings.TrimSpace(cfg.TickTickNoteProjectID) == "" {
		return fmt.Errorf("INDEX01_TICKTICK_NOTE_PROJECT_ID is required")
	}
	if strings.TrimSpace(cfg.WorkerOwner) == "" {
		cfg.WorkerOwner = "index-01-hook"
	}
	aliases, err := parseProjectAliases(getenv("INDEX01_TICKTICK_PROJECT_ALIASES"))
	if err != nil {
		return err
	}
	cfg.TickTickProjectAliases = aliases

	return nil
}

// loadRoutingDescriptions applies one configuration stage without changing validation order.
func (cfg *Config) loadRoutingDescriptions(getenv func(string) string) error {
	descriptions, err := parseAliasDescriptions(
		getenv("INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS"),
		cfg.TickTickProjectAliases,
		cfg.TickTickDefaultProjectID,
		cfg.TickTickNoteProjectID,
	)
	if err != nil {
		return err
	}
	if cfg.verificationEnabled() && len(cfg.TickTickProjectAliases) != len(descriptions) {
		return fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS must describe every configured alias when verification is enabled")
	}
	cfg.TypeSafeAliasDescriptions = descriptions

	return nil
}

// loadHTTPStorage applies one configuration stage without changing validation order.
func (cfg *Config) loadHTTPStorage(getenv func(string) string) error {
	path, err := normalizeDatabasePath(cfg.DBPath)
	if err != nil {
		return err
	}
	cfg.DBPath = path
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	} else if strings.TrimSpace(cfg.ListenAddr) == "" {
		return fmt.Errorf("INDEX01_LISTEN_ADDR must not be blank")
	}
	if raw := getenv("INDEX01_MAX_BODY_BYTES"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return fmt.Errorf("INDEX01_MAX_BODY_BYTES must be a positive integer")
		}
		cfg.MaxBodyBytes = value
	}
	return nil
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

// stringMap accepts one JSON object and rejects null or trailing values.
func (value configValue) stringMap() (map[string]string, error) {
	raw, variable := value.raw, value.variable
	values := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return values, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&values); err != nil || values == nil {
		return nil, fmt.Errorf("%s must be a JSON object", variable)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%s must contain one JSON object", variable)
	}
	return values, nil
}

// parseProjectAliases normalizes keys without allowing collisions.
func parseProjectAliases(raw string) (map[string]string, error) {
	aliases, err := (configValue{raw, "INDEX01_TICKTICK_PROJECT_ALIASES"}).stringMap()
	if err != nil {
		return nil, err
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

// aliasDescriptionPolicy holds the context needed to reject private identifiers.
type aliasDescription struct {
	alias       string
	description string
}

type aliasDescriptionPolicy struct {
	aliases    map[string]string
	projectIDs []string
}

// newAliasDescriptionPolicy includes both aliases and default destinations.
func newAliasDescriptionPolicy(aliases map[string]string, additional []string) aliasDescriptionPolicy {
	policy := aliasDescriptionPolicy{aliases: aliases}
	for _, id := range aliases {
		policy.projectIDs = append(policy.projectIDs, strings.TrimSpace(id))
	}
	for _, id := range additional {
		policy.projectIDs = append(policy.projectIDs, strings.TrimSpace(id))
	}
	return policy
}

// containsIdentifier checks the alias and description using the existing case rules.
func (policy aliasDescriptionPolicy) containsIdentifier(value aliasDescription) bool {
	alias, description := value.alias, value.description
	for _, id := range policy.projectIDs {
		if id == "" {
			continue
		}
		if strings.EqualFold(alias, id) {
			return true
		}
		if strings.Contains(strings.ToLower(description), strings.ToLower(id)) {
			return true
		}
	}
	return false
}

// valid rejects blank, oversized, and multiline descriptions.
func (value aliasDescription) valid() bool {
	alias, description := value.alias, value.description
	if alias == "" || description == "" {
		return false
	}
	if len(description) > maxQueuedNotesBytes {
		return false
	}
	return !strings.ContainsAny(description, "\r\n\x00")
}

// addDescription preserves validation precedence before inserting an alias.
func (policy aliasDescriptionPolicy) addDescription(normalized map[string]string, raw aliasDescription) error {
	alias := strings.ToLower(strings.TrimSpace(raw.alias))
	description := strings.TrimSpace(raw.description)
	if policy.containsIdentifier(aliasDescription{alias, description}) {
		return fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS must not contain a project identifier")
	}
	if !(aliasDescription{alias, description}).valid() {
		return fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS contains a blank or invalid description")
	}
	if _, ok := policy.aliases[alias]; !ok {
		return fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS contains an unknown alias")
	}
	if _, exists := normalized[alias]; exists {
		return fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS contains duplicate normalized aliases")
	}
	normalized[alias] = description
	return nil
}

// validateDescriptionCoverage requires a description for every configured alias.
func validateDescriptionCoverage(aliases, descriptions map[string]string) error {
	for alias := range aliases {
		if _, ok := descriptions[alias]; !ok {
			return fmt.Errorf("INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS must describe every configured alias")
		}
	}
	return nil
}

// parseAliasDescriptions retains the optional blank-input behavior.
func parseAliasDescriptions(raw string, aliases map[string]string, additionalProjectIDs ...string) (map[string]string, error) {
	descriptions, err := (configValue{raw, "INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS"}).stringMap()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return descriptions, nil
	}
	policy := newAliasDescriptionPolicy(aliases, additionalProjectIDs)
	normalized := make(map[string]string, len(descriptions))
	for alias, description := range descriptions {
		if err := policy.addDescription(normalized, aliasDescription{alias, description}); err != nil {
			return nil, err
		}
	}
	if err := validateDescriptionCoverage(aliases, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// validRetentionDays expresses the bounded evidence retention policy.
func validRetentionDays(days int) bool { return days >= 0 && days <= 365 }

// validEvaluationPollInterval allows disabled polling or the supported range.
func validEvaluationPollInterval(interval time.Duration) bool {
	if interval == 0 {
		return true
	}
	return interval >= time.Hour && interval <= 7*24*time.Hour
}
