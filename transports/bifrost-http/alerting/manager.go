package alerting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	bifrost "github.com/maximhq/bifrost/core"
	corenetwork "github.com/maximhq/bifrost/core/network"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/governance"
)

const (
	DefaultSweepInterval     = 60 * time.Second
	DefaultRuleCooldown      = 60 * time.Second
	MaxProviderWindowSeconds = 30 * 24 * 60 * 60
	maxResponseBytes         = 64 << 10
	maxWeComMarkdownBytes    = 4096
	alertLeaderLockKey       = "bifrost:alerting:leader"
)

var (
	ErrRuleEvaluationInProgress = errors.New("this alert rule is already being evaluated")
	ErrAlertingNotLeader        = errors.New("manual alert evaluation must run on the active alerting leader")
	ErrAlertRuleDisabled        = errors.New("disabled alert rules cannot be evaluated manually")
)

type RuleEvaluationResult struct {
	RuleID          string `json:"rule_id"`
	Matched         bool   `json:"matched"`
	MatchedTargets  int    `json:"matched_targets"`
	SentCount       int    `json:"sent_count"`
	SkippedCount    int    `json:"skipped_count"`
	FailedCount     int    `json:"failed_count"`
	CooldownIgnored bool   `json:"cooldown_ignored"`
}

func (r *RuleEvaluationResult) merge(other RuleEvaluationResult) {
	r.SentCount += other.SentCount
	r.SkippedCount += other.SkippedCount
	r.FailedCount += other.FailedCount
}

type leaderLockStore interface {
	TryAcquireLock(context.Context, *tables.TableDistributedLock) (bool, error)
	GetLock(context.Context, string) (*tables.TableDistributedLock, error)
	UpdateLockExpiry(context.Context, string, string, time.Time) error
	CleanupExpiredLockByKey(context.Context, string) (bool, error)
}

type NetworkConfig struct {
	AllowHTTP           bool `json:"allow_http"`
	AllowPrivateNetwork bool `json:"allow_private_network"`
}

type Config struct {
	HistoryRetentionDays      *int              `json:"history_retention_days,omitempty"`
	EvaluationIntervalSeconds int64             `json:"evaluation_interval_seconds,omitempty"`
	WebhookNetwork            NetworkConfig     `json:"webhook_network,omitempty"`
	Channels                  []ChannelSpec     `json:"channels,omitempty"`
	Rules                     []RuleSpec        `json:"rules,omitempty"`
	ProviderExists            func(string) bool `json:"-"`
}

type ChannelSpec struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type"`
	Enabled     *bool          `json:"enabled,omitempty"`
	Config      map[string]any `json:"config"`
}

type RuleSpec struct {
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
	Description             string         `json:"description,omitempty"`
	Enabled                 *bool          `json:"enabled,omitempty"`
	ScopeType               string         `json:"scope_type"`
	ScopeID                 string         `json:"scope_id"`
	TargetType              *string        `json:"target_type,omitempty"`
	TargetID                *string        `json:"target_id,omitempty"`
	CELExpression           string         `json:"cel_expression"`
	ChannelIDs              []string       `json:"channel_ids"`
	QueryBuilder            map[string]any `json:"query,omitempty"`
	LegacyQueryBuilder      map[string]any `json:"query_builder,omitempty"`
	CooldownSeconds         *int64         `json:"cooldown_seconds,omitempty"`
	WindowSeconds           int64          `json:"window_seconds,omitempty"`
	MinRequests             int64          `json:"min_requests,omitempty"`
	NotifyOncePerResetCycle bool           `json:"notify_once_per_reset_cycle,omitempty"`
}

type GovernanceSnapshot func(context.Context) *governance.GovernanceData

type MetricsStore interface {
	GetStats(context.Context, logstore.SearchFilters) (*logstore.SearchStats, error)
}

type HistoryStore interface {
	CreateAlertHistory(context.Context, *logstore.AlertHistory) error
	ListAlertHistory(context.Context, logstore.AlertHistoryQuery) ([]logstore.AlertHistory, int64, error)
	DeleteAlertHistoryBefore(context.Context, time.Time) (int64, error)
	ListLatestAlertRuleSends(context.Context) ([]logstore.AlertHistory, error)
}

type Manager struct {
	store           configstore.AlertStore
	leaderStore     leaderLockStore
	holderID        string
	governance      GovernanceSnapshot
	metrics         MetricsStore
	history         HistoryStore
	logger          schemas.Logger
	network         NetworkConfig
	env             *cel.Env
	client          *http.Client
	privateClient   *http.Client
	programs        sync.Map // rule ID + expression -> cel.Program
	ruleEvaluations sync.Map // rule ID -> struct{} while a manual evaluation is in flight
	evaluationMu    sync.Mutex
	leaderMu        sync.Mutex
	isLeader        bool
	cooldownsMu     sync.Mutex
	ruleSent        map[string]time.Time
	suppressionSeen map[string]time.Time
	cancel          context.CancelFunc
	done            chan struct{}
	sweepInterval   time.Duration
	retentionDays   int
	lastPrune       time.Time
	providerExists  func(string) bool
}

func NewManager(store configstore.AlertStore, snapshot GovernanceSnapshot, metrics MetricsStore, history HistoryStore, logger schemas.Logger, cfg *Config) (*Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("alerting requires a config store with alert configuration persistence")
	}
	if history == nil {
		return nil, fmt.Errorf("alerting requires a logs store for alert history")
	}
	env, err := cel.NewEnv(
		cel.Variable("budget_usage_percent", cel.DoubleType),
		cel.Variable("budget_spent", cel.DoubleType),
		cel.Variable("budget_limit", cel.DoubleType),
		cel.Variable("rate_limit_request_usage_percent", cel.DoubleType),
		cel.Variable("request_usage", cel.IntType),
		cel.Variable("request_limit", cel.IntType),
		cel.Variable("rate_limit_token_usage_percent", cel.DoubleType),
		cel.Variable("token_usage", cel.IntType),
		cel.Variable("token_limit", cel.IntType),
		cel.Variable("scope_type", cel.StringType),
		cel.Variable("scope_id", cel.StringType),
		cel.Variable("target_type", cel.StringType),
		cel.Variable("target_id", cel.StringType),
		cel.Variable("provider", cel.StringType),
		cel.Variable("model", cel.StringType),
		cel.Variable("provider_error_rate", cel.DoubleType),
		cel.Variable("provider_error_count", cel.IntType),
		cel.Variable("provider_success_count", cel.IntType),
		cel.Variable("provider_request_count", cel.IntType),
		cel.Variable("window_seconds", cel.IntType),
		cel.Variable("reset_cycle_id", cel.StringType),
	)
	if err != nil {
		return nil, err
	}
	manager := &Manager{
		store:           store,
		holderID:        uuid.NewString(),
		governance:      snapshot,
		metrics:         metrics,
		history:         history,
		logger:          logger,
		env:             env,
		ruleSent:        make(map[string]time.Time),
		suppressionSeen: make(map[string]time.Time),
		done:            make(chan struct{}),
		sweepInterval:   DefaultSweepInterval,
		retentionDays:   365,
	}
	manager.leaderStore, _ = store.(leaderLockStore)
	if cfg != nil {
		manager.network = cfg.WebhookNetwork
		if cfg.EvaluationIntervalSeconds != 0 {
			if cfg.EvaluationIntervalSeconds < 5 || cfg.EvaluationIntervalSeconds > 3600 {
				return nil, fmt.Errorf("evaluation_interval_seconds must be between 5 and 3600")
			}
			manager.sweepInterval = time.Duration(cfg.EvaluationIntervalSeconds) * time.Second
		}
		if cfg.HistoryRetentionDays != nil {
			manager.retentionDays = *cfg.HistoryRetentionDays
		}
		manager.providerExists = cfg.ProviderExists
	}
	manager.client = newHTTPClient(false)
	manager.privateClient = newHTTPClient(true)
	if err := manager.restoreCooldowns(context.Background()); err != nil && logger != nil {
		logger.Warn("alerting: failed to restore cooldown state: %v", err)
	}
	if cfg != nil {
		if err := manager.SyncConfig(context.Background(), cfg); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func (m *Manager) SetProviderValidator(validator func(string) bool) {
	m.providerExists = validator
}

func newHTTPClient(allowPrivate bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if allowPrivate {
		transport.DialContext = newPrivateAlertDialContext()
	} else {
		transport.DialContext = corenetwork.SSRFSafeDialContext(5 * time.Second)
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return bifrost.ValidateExternalURL(req.URL.String(), allowPrivate)
	}
	return client
}

func newPrivateAlertDialContext() func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return func(ctx context.Context, networkName, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid alert dial address: %w", err)
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("alert destination DNS lookup failed")
		}
		for _, ip := range ips {
			if ip.IsUnspecified() || corenetwork.IsLinkLocal(ip) {
				return nil, fmt.Errorf("alert destination resolved to unsafe address")
			}
		}
		return dialer.DialContext(ctx, networkName, net.JoinHostPort(ips[0].String(), port))
	}
}

func (m *Manager) Start(parent context.Context) {
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil && m.logger != nil {
				m.logger.Error("alerting: recovered sweep-loop panic type=%T\n%s", recovered, debug.Stack())
			}
			close(m.done)
		}()
		ticker := time.NewTicker(m.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.EvaluateNow(ctx); err != nil && m.logger != nil {
					m.logger.Error("alerting sweep failed: %v", err)
				}
			}
		}
	}()
}

func (m *Manager) Close() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	<-m.done
}

func (m *Manager) ValidateExpression(expression string) error {
	ast, issues := m.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return issues.Err()
	}
	if ast.OutputType() != cel.BoolType {
		return fmt.Errorf("CEL expression must evaluate to boolean, got %s", ast.OutputType())
	}
	return nil
}

func (m *Manager) ListHistory(ctx context.Context, query logstore.AlertHistoryQuery) ([]logstore.AlertHistory, int64, error) {
	return m.history.ListAlertHistory(ctx, query)
}

func (m *Manager) ValidateChannel(channel *tables.TableAlertChannel) error {
	if err := channel.Validate(); err != nil {
		return err
	}
	required := "webhook_url"
	switch channel.Type {
	case tables.AlertChannelPagerDuty:
		if err := requireExactlyOneConfigKey(channel.Config, "routing_key", "integration_key"); err != nil {
			return fmt.Errorf("pagerduty: %w", err)
		}
		if stringConfig(channel.Config, "routing_key", "integration_key") == "" {
			return fmt.Errorf("routing_key is required for pagerduty")
		}
		return nil
	case tables.AlertChannelWebhook:
		required = "url"
	}
	if err := requireExactlyOneConfigKey(channel.Config, required, alternateURLKey(required)); err != nil {
		return fmt.Errorf("%s: %w", channel.Type, err)
	}
	rawURL := stringConfig(channel.Config, required, alternateURLKey(required))
	if rawURL == "" {
		return fmt.Errorf("%s is required for %s", required, channel.Type)
	}
	if channel.Type == tables.AlertChannelWebhook {
		if err := validateWebhookHeaders(channel.Config["headers"]); err != nil {
			return err
		}
	}
	return m.validateURL(rawURL)
}

func requireExactlyOneConfigKey(config map[string]any, first, second string) error {
	count := 0
	for _, key := range []string{first, second} {
		if value, ok := config[key].(string); ok && strings.TrimSpace(value) != "" {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("exactly one of %s or %s is required", first, second)
	}
	return nil
}

func validateWebhookHeaders(value any) error {
	if value == nil {
		return nil
	}
	validate := func(name, value string) error {
		if !validHTTPHeaderName(name) {
			return fmt.Errorf("invalid webhook header name %q", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("webhook header %q contains a newline", name)
		}
		return nil
	}
	switch headers := value.(type) {
	case map[string]string:
		for name, headerValue := range headers {
			if err := validate(name, headerValue); err != nil {
				return err
			}
		}
	case map[string]any:
		for name, raw := range headers {
			headerValue, ok := raw.(string)
			if !ok {
				return fmt.Errorf("webhook header %q must be a string", name)
			}
			if err := validate(name, headerValue); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("webhook headers must be an object")
	}
	return nil
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			continue
		}
		return false
	}
	return true
}

func alternateURLKey(key string) string {
	if key == "url" {
		return "webhook_url"
	}
	return "url"
}

func (m *Manager) validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if parsed.Scheme == "http" && !m.network.AllowHTTP {
		return fmt.Errorf("webhook URL must use https unless alerting.webhook_network.allow_http is enabled")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("unsupported webhook URL scheme %q", parsed.Scheme)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("webhook URL must not contain credentials or a fragment")
	}
	return bifrost.ValidateExternalURL(rawURL, m.network.AllowPrivateNetwork)
}

func (m *Manager) ValidateRule(ctx context.Context, rule *tables.TableAlertRule) error {
	if rule.Name == "" || rule.ScopeID == "" || rule.CELExpression == "" {
		return fmt.Errorf("name, scope_id, and cel_expression are required")
	}
	if rule.ScopeType != "virtual_key" && rule.ScopeType != "team" && rule.ScopeType != "customer" && rule.ScopeType != "provider" {
		return fmt.Errorf("scope_type must be virtual_key, team, customer, or provider")
	}
	if (rule.TargetType == nil) != (rule.TargetID == nil) {
		return fmt.Errorf("target_type and target_id must be provided together")
	}
	if rule.ScopeType == "provider" {
		if m.metrics == nil {
			return fmt.Errorf("provider alert rules require logging to be enabled")
		}
		if m.providerExists != nil && !m.providerExists(rule.ScopeID) {
			return fmt.Errorf("referenced provider %q does not exist", rule.ScopeID)
		}
		if rule.TargetType != nil && *rule.TargetType != "model" {
			return fmt.Errorf("provider rules only support an optional model target")
		}
		if rule.NotifyOncePerResetCycle {
			return fmt.Errorf("notify_once_per_reset_cycle is only supported for governance rules")
		}
	} else if rule.TargetType != nil && *rule.TargetType != "budget" {
		return fmt.Errorf("governance rules only support budget targets")
	}
	if len(rule.ChannelIDs) == 0 {
		return fmt.Errorf("at least one channel is required")
	}
	seenChannels := make(map[string]struct{}, len(rule.ChannelIDs))
	for _, channelID := range rule.ChannelIDs {
		if _, exists := seenChannels[channelID]; exists {
			return fmt.Errorf("duplicate alert channel %q", channelID)
		}
		seenChannels[channelID] = struct{}{}
	}
	if rule.CooldownMilliseconds < 0 || rule.CooldownMilliseconds%1000 != 0 {
		return fmt.Errorf("cooldown_milliseconds must be a non-negative whole-second value")
	}
	if rule.WindowSeconds == 0 {
		rule.WindowSeconds = 300
	}
	if rule.MinRequests == 0 {
		rule.MinRequests = 1
	}
	if rule.WindowSeconds < 60 || rule.WindowSeconds > MaxProviderWindowSeconds {
		return fmt.Errorf("window_seconds must be between 60 and %d", MaxProviderWindowSeconds)
	}
	if rule.MinRequests < 1 {
		return fmt.Errorf("min_requests must be at least 1")
	}
	if err := m.ValidateExpression(rule.CELExpression); err != nil {
		return fmt.Errorf("invalid CEL expression: %w", err)
	}
	for _, id := range rule.ChannelIDs {
		if _, err := m.store.GetAlertChannel(ctx, id); err != nil {
			return fmt.Errorf("alert channel %q does not exist", id)
		}
	}
	if rule.ScopeType == "provider" {
		return nil
	}
	if m.governance == nil {
		return fmt.Errorf("governance snapshot is unavailable")
	}
	data := m.governance(ctx)
	if data == nil || !scopeExists(data, rule.ScopeType, rule.ScopeID) {
		return fmt.Errorf("referenced %s scope %q does not exist", rule.ScopeType, rule.ScopeID)
	}
	if rule.TargetID != nil {
		budgets, _ := collectScope(data, rule.ScopeType, rule.ScopeID)
		found := false
		for _, budget := range budgets {
			if budget.ID == *rule.TargetID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("target budget %q does not belong to the scope", *rule.TargetID)
		}
	}
	return nil
}

func scopeExists(data *governance.GovernanceData, scopeType, scopeID string) bool {
	switch scopeType {
	case "virtual_key":
		for _, vk := range data.VirtualKeys {
			if vk != nil && vk.ID == scopeID {
				return true
			}
		}
	case "team":
		_, ok := data.Teams[scopeID]
		return ok
	case "customer":
		_, ok := data.Customers[scopeID]
		return ok
	}
	return false
}

func collectScope(data *governance.GovernanceData, scopeType, scopeID string) ([]tables.TableBudget, *tables.TableRateLimit) {
	switch scopeType {
	case "virtual_key":
		for _, vk := range data.VirtualKeys {
			if vk != nil && vk.ID == scopeID {
				return vk.Budgets, vk.RateLimit
			}
		}
	case "team":
		if team := data.Teams[scopeID]; team != nil {
			return team.Budgets, team.RateLimit
		}
	case "customer":
		if customer := data.Customers[scopeID]; customer != nil {
			return customer.Budgets, customer.RateLimit
		}
	}
	return nil, nil
}

func (m *Manager) program(rule *tables.TableAlertRule) (cel.Program, error) {
	key := rule.ID + "\x00" + rule.CELExpression
	if value, ok := m.programs.Load(key); ok {
		return value.(cel.Program), nil
	}
	ast, issues := m.env.Compile(rule.CELExpression)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	program, err := m.env.Program(ast)
	if err == nil {
		m.programs.Store(key, program)
	}
	return program, err
}

func (m *Manager) EvaluateNow(ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if m.logger != nil {
				m.logger.Error("alerting: recovered evaluation panic type=%T\n%s", recovered, debug.Stack())
			}
			err = fmt.Errorf("alert evaluation failed unexpectedly")
		}
	}()
	m.evaluationMu.Lock()
	defer m.evaluationMu.Unlock()
	leader, err := m.ensureLeadership(ctx)
	if err != nil {
		return fmt.Errorf("alerting leadership check failed: %w", err)
	}
	if !leader {
		return nil
	}
	evaluationCtx, stopHeartbeat := m.withLeadershipHeartbeat(ctx)
	defer stopHeartbeat()
	ctx = evaluationCtx
	if err := m.pruneHistory(ctx); err != nil && m.logger != nil {
		m.logger.Warn("alerting: history retention cleanup failed: %v", err)
	}
	rules, err := m.store.ListAlertRules(ctx)
	if err != nil {
		return err
	}
	channelMap, err := m.alertChannelsByID(ctx)
	if err != nil {
		return err
	}
	var data *governance.GovernanceData
	if m.governance != nil {
		data = m.governance(ctx)
	}
	for i := range rules {
		if rules[i].Enabled {
			m.evaluateRule(ctx, &rules[i], channelMap, data, false, "")
		}
	}
	return nil
}

func (m *Manager) alertChannelsByID(ctx context.Context) (map[string]*tables.TableAlertChannel, error) {
	channels, err := m.store.ListAlertChannels(ctx)
	if err != nil {
		return nil, err
	}
	channelMap := make(map[string]*tables.TableAlertChannel, len(channels))
	for i := range channels {
		channelMap[channels[i].ID] = &channels[i]
	}
	return channelMap, nil
}

func (m *Manager) evaluateRule(
	ctx context.Context,
	rule *tables.TableAlertRule,
	channels map[string]*tables.TableAlertChannel,
	data *governance.GovernanceData,
	ignoreCooldown bool,
	sentDetail string,
) RuleEvaluationResult {
	result := RuleEvaluationResult{RuleID: rule.ID, CooldownIgnored: ignoreCooldown}
	program, err := m.program(rule)
	if err != nil {
		m.logFailure(ctx, rule, nil, nil, "invalid CEL expression: "+err.Error())
		result.FailedCount++
		return result
	}
	if rule.ScopeType == "provider" {
		input, metricsErr := m.providerEvaluationInput(ctx, rule)
		if metricsErr != nil {
			m.logFailure(ctx, rule, nil, input, "provider metrics query failed: "+metricsErr.Error())
			result.FailedCount++
			return result
		}
		if input["provider_request_count"].(int64) < rule.MinRequests {
			return result
		}
		output, _, evalErr := program.Eval(input)
		if evalErr != nil {
			m.logFailure(ctx, rule, nil, input, "CEL evaluation failed: "+evalErr.Error())
			result.FailedCount++
			return result
		}
		if matched, ok := output.Value().(bool); ok && matched {
			result.Matched = true
			result.MatchedTargets = 1
			result.merge(m.dispatchMatch(ctx, rule, input, channels, ignoreCooldown, sentDetail))
		}
		return result
	}
	if data == nil {
		m.logFailure(ctx, rule, nil, nil, "governance snapshot is unavailable")
		result.FailedCount++
		return result
	}
	if !scopeExists(data, rule.ScopeType, rule.ScopeID) {
		m.logFailure(ctx, rule, nil, nil, fmt.Sprintf("referenced %s scope %q no longer exists", rule.ScopeType, rule.ScopeID))
		result.FailedCount++
		return result
	}
	budgets, rateLimit := collectScope(data, rule.ScopeType, rule.ScopeID)
	if rule.TargetID != nil {
		filtered := budgets[:0]
		for _, budget := range budgets {
			if budget.ID == *rule.TargetID {
				filtered = append(filtered, budget)
			}
		}
		budgets = filtered
		if len(budgets) == 0 {
			m.logFailure(ctx, rule, nil, nil, fmt.Sprintf("target budget %q no longer exists in the scope", *rule.TargetID))
			result.FailedCount++
			return result
		}
	}
	if len(budgets) == 0 {
		budgets = []tables.TableBudget{{}}
	}
	for budgetIndex := range budgets {
		input := evaluationInput(rule, &budgets[budgetIndex], rateLimit)
		output, _, evalErr := program.Eval(input)
		if evalErr != nil {
			m.logFailure(ctx, rule, nil, input, "CEL evaluation failed: "+evalErr.Error())
			result.FailedCount++
			continue
		}
		matched, ok := output.Value().(bool)
		if !ok || !matched {
			continue
		}
		result.Matched = true
		result.MatchedTargets++
		result.merge(m.dispatchMatch(ctx, rule, input, channels, ignoreCooldown, sentDetail))
	}
	return result
}

func (m *Manager) EvaluateRuleNow(ctx context.Context, ruleID string, ignoreCooldown bool) (*RuleEvaluationResult, error) {
	if _, loaded := m.ruleEvaluations.LoadOrStore(ruleID, struct{}{}); loaded {
		return nil, ErrRuleEvaluationInProgress
	}
	defer m.ruleEvaluations.Delete(ruleID)
	m.evaluationMu.Lock()
	defer m.evaluationMu.Unlock()
	leader, err := m.ensureLeadership(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerting leadership check failed: %w", err)
	}
	if !leader {
		return nil, ErrAlertingNotLeader
	}
	evaluationCtx, stopHeartbeat := m.withLeadershipHeartbeat(ctx)
	defer stopHeartbeat()
	rule, err := m.store.GetAlertRule(evaluationCtx, ruleID)
	if err != nil {
		return nil, err
	}
	if !rule.Enabled {
		return nil, ErrAlertRuleDisabled
	}
	channels, err := m.alertChannelsByID(evaluationCtx)
	if err != nil {
		return nil, err
	}
	var data *governance.GovernanceData
	if m.governance != nil {
		data = m.governance(evaluationCtx)
	}
	detail := "manual evaluation"
	if ignoreCooldown {
		detail = "manual override: cooldown ignored"
	}
	result := m.evaluateRule(evaluationCtx, rule, channels, data, ignoreCooldown, detail)
	return &result, nil
}

func (m *Manager) RunningRuleEvaluations() []string {
	ruleIDs := make([]string, 0)
	m.ruleEvaluations.Range(func(key, _ any) bool {
		ruleIDs = append(ruleIDs, key.(string))
		return true
	})
	sort.Strings(ruleIDs)
	return ruleIDs
}

func (m *Manager) providerEvaluationInput(ctx context.Context, rule *tables.TableAlertRule) (map[string]any, error) {
	model := ""
	if rule.TargetType != nil && rule.TargetID != nil && *rule.TargetType == "model" {
		model = *rule.TargetID
	}
	input := map[string]any{
		"budget_usage_percent":             0.0,
		"budget_spent":                     0.0,
		"budget_limit":                     0.0,
		"rate_limit_request_usage_percent": 0.0,
		"request_usage":                    int64(0),
		"request_limit":                    int64(0),
		"rate_limit_token_usage_percent":   0.0,
		"token_usage":                      int64(0),
		"token_limit":                      int64(0),
		"scope_type":                       "provider",
		"scope_id":                         rule.ScopeID,
		"target_type":                      "",
		"target_id":                        "",
		"provider":                         rule.ScopeID,
		"model":                            model,
		"provider_error_rate":              0.0,
		"provider_error_count":             int64(0),
		"provider_success_count":           int64(0),
		"provider_request_count":           int64(0),
		"window_seconds":                   rule.WindowSeconds,
		"reset_cycle_id":                   "",
	}
	if model != "" {
		input["target_type"] = "model"
		input["target_id"] = model
	}
	if m.metrics == nil {
		return input, fmt.Errorf("logging is not enabled")
	}
	end := time.Now().UTC()
	start := end.Add(-time.Duration(rule.WindowSeconds) * time.Second)
	filters := logstore.SearchFilters{Providers: []string{rule.ScopeID}, Status: []string{"success", "error"}, StartTime: &start, EndTime: &end}
	if model != "" {
		filters.Models = []string{model}
	}
	totalStats, err := m.metrics.GetStats(ctx, filters)
	if err != nil {
		return input, err
	}
	total := totalStats.TotalRequests
	successes := int64(math.Round(float64(total) * totalStats.SuccessRate / 100))
	if successes < 0 {
		successes = 0
	}
	if successes > total {
		successes = total
	}
	errorsCount := total - successes
	input["provider_request_count"] = total
	input["provider_error_count"] = errorsCount
	input["provider_success_count"] = successes
	input["provider_error_rate"] = percent(float64(errorsCount), float64(total))
	return input, nil
}

func (m *Manager) pruneHistory(ctx context.Context) error {
	if m.retentionDays <= 0 || time.Since(m.lastPrune) < 24*time.Hour {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -m.retentionDays)
	if _, err := m.history.DeleteAlertHistoryBefore(ctx, cutoff); err != nil {
		return err
	}
	if _, err := m.store.DeleteAlertSuppressionsBefore(ctx, cutoff); err != nil {
		return err
	}
	m.cooldownsMu.Lock()
	for key, lastSentAt := range m.suppressionSeen {
		if lastSentAt.Before(cutoff) {
			delete(m.suppressionSeen, key)
		}
	}
	m.cooldownsMu.Unlock()
	m.lastPrune = time.Now()
	return nil
}

// ensureLeadership uses the existing shared config-store lease so a multi-node
// OSS deployment has the same single-dispatch guarantee as Enterprise. A
// custom AlertStore without lock support is treated as a single-node store.
func (m *Manager) ensureLeadership(ctx context.Context) (bool, error) {
	if m.leaderStore == nil {
		return true, nil
	}
	m.leaderMu.Lock()
	defer m.leaderMu.Unlock()
	now := time.Now().UTC()
	expiresAt := now.Add(m.leaderLeaseDuration())
	lock, err := m.leaderStore.GetLock(ctx, alertLeaderLockKey)
	if err != nil {
		return false, err
	}
	if lock != nil && lock.HolderID == m.holderID && lock.ExpiresAt.After(now) {
		if err := m.leaderStore.UpdateLockExpiry(ctx, alertLeaderLockKey, m.holderID, expiresAt); err != nil {
			return false, err
		}
		m.isLeader = true
		return true, nil
	}
	if lock != nil && lock.ExpiresAt.After(now) {
		m.isLeader = false
		return false, nil
	}
	if lock != nil && !lock.ExpiresAt.After(now) {
		if _, err := m.leaderStore.CleanupExpiredLockByKey(ctx, alertLeaderLockKey); err != nil {
			return false, err
		}
	}
	acquired, err := m.leaderStore.TryAcquireLock(ctx, &tables.TableDistributedLock{LockKey: alertLeaderLockKey, HolderID: m.holderID, ExpiresAt: expiresAt})
	if err != nil || !acquired {
		m.isLeader = false
		return acquired, err
	}
	// A promoted follower may have been running for hours. Rebuild cooldowns
	// from durable successful-send history before its first dispatch.
	if !m.isLeader {
		if err := m.restoreCooldowns(ctx); err != nil {
			return false, err
		}
	}
	m.isLeader = true
	return true, nil
}

func (m *Manager) leaderLeaseDuration() time.Duration {
	duration := m.sweepInterval + 15*time.Second
	if duration < 45*time.Second {
		return 45 * time.Second
	}
	return duration
}

func (m *Manager) withLeadershipHeartbeat(parent context.Context) (context.Context, func()) {
	if m.leaderStore == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	interval := m.leaderLeaseDuration() / 3
	if interval > 20*time.Second {
		interval = 20 * time.Second
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil && m.logger != nil {
				m.logger.Error("alerting: recovered leadership heartbeat panic type=%T\n%s", recovered, debug.Stack())
			}
			close(done)
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.leaderMu.Lock()
				err := m.leaderStore.UpdateLockExpiry(ctx, alertLeaderLockKey, m.holderID, time.Now().UTC().Add(m.leaderLeaseDuration()))
				if err != nil {
					m.isLeader = false
				}
				m.leaderMu.Unlock()
				if err != nil {
					if m.logger != nil {
						m.logger.Error("alerting: lost leader lease during evaluation: %v", err)
					}
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-done
	}
}

func evaluationInput(rule *tables.TableAlertRule, budget *tables.TableBudget, rateLimit *tables.TableRateLimit) map[string]any {
	input := map[string]any{
		"budget_usage_percent":             0.0,
		"budget_spent":                     0.0,
		"budget_limit":                     0.0,
		"rate_limit_request_usage_percent": 0.0,
		"request_usage":                    int64(0),
		"request_limit":                    int64(0),
		"rate_limit_token_usage_percent":   0.0,
		"token_usage":                      int64(0),
		"token_limit":                      int64(0),
		"scope_type":                       rule.ScopeType,
		"scope_id":                         rule.ScopeID,
		"target_type":                      "",
		"target_id":                        "",
		"provider":                         "",
		"model":                            "",
		"provider_error_rate":              0.0,
		"provider_error_count":             int64(0),
		"provider_success_count":           int64(0),
		"provider_request_count":           int64(0),
		"window_seconds":                   rule.WindowSeconds,
		"reset_cycle_id":                   "",
	}
	if budget != nil && budget.ID != "" {
		limit := budget.EffectiveMaxLimit()
		input["budget_spent"] = budget.CurrentUsage
		input["budget_limit"] = limit
		input["budget_usage_percent"] = percent(budget.CurrentUsage, limit)
		input["target_type"] = "budget"
		input["target_id"] = budget.ID
		input["reset_cycle_id"] = resetCycleID("budget", budget.ID, budget.LastReset)
	}
	if rateLimit != nil {
		input["request_usage"] = rateLimit.RequestCurrentUsage
		input["token_usage"] = rateLimit.TokenCurrentUsage
		if rateLimit.RequestMaxLimit != nil {
			input["request_limit"] = *rateLimit.RequestMaxLimit
			input["rate_limit_request_usage_percent"] = percent(float64(rateLimit.RequestCurrentUsage), float64(*rateLimit.RequestMaxLimit))
		}
		if rateLimit.TokenMaxLimit != nil {
			input["token_limit"] = *rateLimit.TokenMaxLimit
			input["rate_limit_token_usage_percent"] = percent(float64(rateLimit.TokenCurrentUsage), float64(*rateLimit.TokenMaxLimit))
		}
		if input["reset_cycle_id"] == "" && rateLimit.ID != "" {
			input["reset_cycle_id"] = rateLimitResetCycleID(rule, rateLimit)
		}
	}
	return input
}

func resetCycleID(kind, id string, lastReset time.Time) string {
	return fmt.Sprintf("%s:%s:%s", kind, id, lastReset.UTC().Format(time.RFC3339Nano))
}

func rateLimitResetCycleID(rule *tables.TableAlertRule, rateLimit *tables.TableRateLimit) string {
	expression := rule.CELExpression
	usesRequests := strings.Contains(expression, "request_usage") || strings.Contains(expression, "rate_limit_request_usage_percent") || strings.Contains(expression, "request_limit")
	usesTokens := strings.Contains(expression, "token_usage") || strings.Contains(expression, "rate_limit_token_usage_percent") || strings.Contains(expression, "token_limit")
	switch {
	case usesRequests && !usesTokens:
		return resetCycleID("request_rate_limit", rateLimit.ID, rateLimit.RequestLastReset)
	case usesTokens && !usesRequests:
		return resetCycleID("token_rate_limit", rateLimit.ID, rateLimit.TokenLastReset)
	default:
		return fmt.Sprintf("rate_limit:%s:request=%s:token=%s", rateLimit.ID, rateLimit.RequestLastReset.UTC().Format(time.RFC3339Nano), rateLimit.TokenLastReset.UTC().Format(time.RFC3339Nano))
	}
}

func percent(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return used / limit * 100
}

func (m *Manager) dispatchMatch(
	ctx context.Context,
	rule *tables.TableAlertRule,
	input map[string]any,
	channels map[string]*tables.TableAlertChannel,
	ignoreCooldown bool,
	sentDetail string,
) RuleEvaluationResult {
	result := RuleEvaluationResult{RuleID: rule.ID, Matched: true, MatchedTargets: 1, CooldownIgnored: ignoreCooldown}
	now := time.Now().UTC()
	targetType, _ := input["target_type"].(string)
	targetID, _ := input["target_id"].(string)
	ruleKey := alertCooldownKey("rule", rule.ID, rule.ScopeType, rule.ScopeID, targetType, targetID)
	suppressionKey := alertCooldownKey("suppression", rule.ID, rule.ScopeType, rule.ScopeID, targetType, targetID)
	ruleCooldown := time.Duration(rule.CooldownMilliseconds) * time.Millisecond
	cycleKey := ""
	if rule.NotifyOncePerResetCycle {
		cycleID, _ := input["reset_cycle_id"].(string)
		if cycleID == "" {
			m.record(ctx, rule, nil, input, "failed", "reset cycle identity is unavailable")
			result.FailedCount++
			return result
		}
		cycleKey = alertCooldownKey("cycle", rule.ID, rule.ScopeType, rule.ScopeID, targetType, targetID, cycleID)
		cycleSuppressionKey := alertCooldownKey("cycle-suppression", rule.ID, rule.ScopeType, rule.ScopeID, targetType, targetID)
		m.cooldownsMu.Lock()
		cycleSentAt := m.ruleSent[cycleKey]
		m.cooldownsMu.Unlock()
		if !cycleSentAt.IsZero() {
			m.recordSuppressionOnce(ctx, rule, input, cycleSuppressionKey, cycleSentAt, "skipped because this reset cycle was already notified")
			result.SkippedCount++
			return result
		}
		ruleCooldown = 0
	}
	if ignoreCooldown {
		ruleCooldown = 0
	}
	m.cooldownsMu.Lock()
	lastRule := m.ruleSent[ruleKey]
	m.cooldownsMu.Unlock()
	if ruleCooldown > 0 && now.Sub(lastRule) < ruleCooldown {
		m.recordSuppressionOnce(ctx, rule, input, suppressionKey, lastRule, "skipped due to rule cooldown")
		result.SkippedCount++
		return result
	}
	for _, channelID := range rule.ChannelIDs {
		channel := channels[channelID]
		if channel == nil || !channel.Enabled {
			m.record(ctx, rule, channel, input, "failed", "alert channel is missing or disabled")
			result.FailedCount++
			continue
		}
		if err := m.deliver(ctx, rule, channel, input, now); err != nil {
			m.record(ctx, rule, channel, input, "failed", err.Error())
			result.FailedCount++
			continue
		}
		m.cooldownsMu.Lock()
		m.ruleSent[ruleKey] = now
		if cycleKey != "" {
			m.ruleSent[cycleKey] = now
		}
		m.cooldownsMu.Unlock()
		if err := m.store.UpsertAlertCooldown(ctx, ruleKey, now); err != nil && m.logger != nil {
			m.logger.Error("alerting: failed to persist rule cooldown: %v", err)
		}
		if cycleKey != "" {
			if err := m.store.UpsertAlertCooldown(ctx, cycleKey, now); err != nil && m.logger != nil {
				m.logger.Error("alerting: failed to persist reset-cycle notification state: %v", err)
			}
		}
		m.record(ctx, rule, channel, input, "sent", sentDetail)
		result.SentCount++
	}
	return result
}

func (m *Manager) recordSuppressionOnce(
	ctx context.Context,
	rule *tables.TableAlertRule,
	input map[string]any,
	key string,
	lastSentAt time.Time,
	detail string,
) {
	m.cooldownsMu.Lock()
	alreadyRecorded := !m.suppressionSeen[key].Before(lastSentAt)
	if !alreadyRecorded {
		m.suppressionSeen[key] = lastSentAt
	}
	m.cooldownsMu.Unlock()
	if !alreadyRecorded {
		m.record(ctx, rule, nil, input, "skipped", detail)
		if err := m.store.UpsertAlertCooldown(ctx, key, lastSentAt); err != nil && m.logger != nil {
			m.logger.Error("alerting: failed to persist suppression state: %v", err)
		}
	}
}

func alertCooldownKey(kind string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return kind + ":" + hex.EncodeToString(hash[:])
}

// TestChannel sends a synthetic notification through a persisted channel
// without evaluating a rule or writing to alert history.
func (m *Manager) TestChannel(ctx context.Context, channel *tables.TableAlertChannel) error {
	if err := m.ValidateChannel(channel); err != nil {
		return err
	}
	rule := &tables.TableAlertRule{
		ID:            "test-notification",
		Name:          "Bifrost 通知渠道测试",
		ScopeType:     "system",
		ScopeID:       "bifrost",
		CELExpression: "true",
	}
	input := map[string]any{
		"target_type":       "channel",
		"target_id":         channel.ID,
		"channel_name":      channel.Name,
		"channel_type":      channel.Type,
		"test_notification": true,
	}
	return m.deliver(ctx, rule, channel, input, time.Now().UTC())
}

func (m *Manager) deliver(ctx context.Context, rule *tables.TableAlertRule, channel *tables.TableAlertChannel, input map[string]any, now time.Time) (err error) {
	channelType := ""
	if channel != nil {
		channelType = channel.Type
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if m.logger != nil {
				m.logger.Error("alerting: recovered delivery panic channel_type=%s panic_type=%T\n%s", channelType, recovered, debug.Stack())
			}
			err = fmt.Errorf("alert delivery failed unexpectedly")
		}
	}()
	if rule == nil || channel == nil {
		return fmt.Errorf("alert delivery requires rule and channel")
	}
	message := alertMessage(rule, input, now)
	var endpoint string
	var payload any
	switch channel.Type {
	case tables.AlertChannelSlack:
		endpoint = stringConfig(channel.Config, "webhook_url", "url")
		payload = map[string]any{"blocks": []any{
			map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": rule.Name}},
			map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "```" + message + "```"}},
		}}
	case tables.AlertChannelMicrosoftTeams:
		endpoint = stringConfig(channel.Config, "webhook_url", "url")
		payload = map[string]any{"type": "message", "attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive", "content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4",
				"body": []any{map[string]any{"type": "TextBlock", "weight": "Bolder", "text": rule.Name}, map[string]any{"type": "TextBlock", "wrap": true, "text": message}},
			},
		}}}
	case tables.AlertChannelWeCom:
		endpoint = stringConfig(channel.Config, "webhook_url", "url")
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"content": weComAlertMarkdown(rule, input, now)},
		}
	case tables.AlertChannelPagerDuty:
		endpoint = "https://events.pagerduty.com/v2/enqueue"
		payload = map[string]any{
			"routing_key": stringConfig(channel.Config, "routing_key", "integration_key"), "event_action": "trigger",
			"dedup_key": dedupKey(rule, input), "payload": map[string]any{"summary": message, "source": "Bifrost Alerting", "severity": alertSeverity(input), "custom_details": input},
		}
	case tables.AlertChannelWebhook:
		endpoint = stringConfig(channel.Config, "url", "webhook_url")
		if isTestNotification(input) {
			payload = map[string]any{
				"event": "alert.test", "timestamp": now,
				"channel": map[string]any{"id": channel.ID, "name": channel.Name, "type": channel.Type}, "message": message,
			}
		} else {
			payload = map[string]any{
				"event": "alert.triggered", "timestamp": now, "rule": map[string]any{"id": rule.ID, "name": rule.Name},
				"scope": map[string]any{"type": rule.ScopeType, "id": rule.ScopeID}, "cel_expression": rule.CELExpression, "input": input, "message": message,
			}
		}
	default:
		return fmt.Errorf("unsupported alert channel type: %s", channel.Type)
	}
	if err := m.validateURL(endpoint); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if channel.Type == tables.AlertChannelMicrosoftTeams && len(body) > 28*1024 {
		return fmt.Errorf("microsoft teams payload exceeds 28 KB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if channel.Type == tables.AlertChannelWebhook {
		for key, value := range stringMapConfig(channel.Config, "headers") {
			if !blockedHeader(key) {
				req.Header.Set(key, value)
			}
		}
	}
	client := m.client
	if m.network.AllowPrivateNetwork {
		client = m.privateClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("delivery failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if readErr != nil {
		return fmt.Errorf("read delivery response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delivery failed with HTTP %d", resp.StatusCode)
	}
	if channel.Type == tables.AlertChannelWeCom {
		var result struct {
			ErrorCode int `json:"errcode"`
		}
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return fmt.Errorf("invalid WeCom webhook response: %w", err)
		}
		if result.ErrorCode != 0 {
			return fmt.Errorf("WeCom webhook rejected the message (errcode %d)", result.ErrorCode)
		}
	}
	return nil
}

func isTestNotification(input map[string]any) bool {
	value, _ := input["test_notification"].(bool)
	return value
}

func alertSeverity(input map[string]any) string {
	if isTestNotification(input) {
		return "info"
	}
	return "warning"
}

func weComAlertMarkdown(rule *tables.TableAlertRule, input map[string]any, now time.Time) string {
	if isTestNotification(input) {
		lines := []string{
			"## Bifrost 通知渠道测试",
			fmt.Sprintf("> 渠道：**%s**", markdownInline(displayValue(input["channel_name"]))),
			fmt.Sprintf("> 类型：%s", markdownInline(displayValue(input["channel_type"]))),
			"> 状态：<font color=\"info\">连接正常</font>",
			fmt.Sprintf("> 测试时间：%s", formatWeComTime(now)),
			"收到此消息表示 Bifrost 已成功连接该通知渠道。",
		}
		return truncateUTF8Bytes(strings.Join(lines, "\n"), maxWeComMarkdownBytes)
	}
	lines := []string{
		"## Bifrost 告警",
		fmt.Sprintf("> 规则：**%s**", markdownInline(rule.Name)),
	}
	if provider := displayValue(input["provider"]); provider != "" {
		lines = append(lines, fmt.Sprintf("> 供应商：%s", markdownInline(provider)))
	}
	if model := displayValue(input["model"]); model != "" {
		lines = append(lines, fmt.Sprintf("> 模型：%s", markdownInline(model)))
	}
	if rate, ok := numericValue(input["provider_error_rate"]); ok {
		lines = append(lines, fmt.Sprintf("> 错误率：<font color=\"warning\">%.2f%%</font>", rate))
	}
	if failures, ok := numericValue(input["provider_error_count"]); ok {
		if total, totalOK := numericValue(input["provider_request_count"]); totalOK {
			lines = append(lines, fmt.Sprintf("> 失败 / 总请求：%.0f / %.0f", failures, total))
		}
	}
	if window, ok := numericValue(input["window_seconds"]); ok && window > 0 {
		lines = append(lines, fmt.Sprintf("> 统计窗口：最近 %s", formatAlertDuration(int64(window))))
	}
	if rule.ScopeType != "provider" {
		lines = append(lines, fmt.Sprintf("> 作用域：%s / %s", markdownInline(rule.ScopeType), markdownInline(rule.ScopeID)))
	}
	lines = append(lines,
		fmt.Sprintf("> 触发时间：%s", formatWeComTime(now)),
		fmt.Sprintf("> 条件：`%s`", strings.ReplaceAll(rule.CELExpression, "`", "'")),
	)
	return truncateUTF8Bytes(strings.Join(lines, "\n"), maxWeComMarkdownBytes)
}

func formatWeComTime(value time.Time) string {
	chinaStandardTime := time.FixedZone("UTC+08:00", 8*60*60)
	return value.In(chinaStandardTime).Format("2006-01-02 15:04:05 UTC+08:00")
}

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	default:
		return 0, false
	}
}

func displayValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func markdownInline(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "`", "'")
}

func formatAlertDuration(seconds int64) string {
	if seconds%(24*60*60) == 0 {
		return fmt.Sprintf("%d 天", seconds/(24*60*60))
	}
	if seconds%(60*60) == 0 {
		return fmt.Sprintf("%d 小时", seconds/(60*60))
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%d 分钟", seconds/60)
	}
	return fmt.Sprintf("%d 秒", seconds)
}

func truncateUTF8Bytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	const suffix = "…"
	end := limit - len(suffix)
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + suffix
}

func blockedHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connection", "content-length", "expect", "host", "keep-alive",
		"proxy-authorization", "proxy-connection", "te", "trailer",
		"transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func stringConfig(config map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := config[key].(string); ok && value != "" {
			return resolveConfigString(value)
		}
	}
	return ""
}

func stringMapConfig(config map[string]any, key string) map[string]string {
	result := map[string]string{}
	switch value := config[key].(type) {
	case map[string]string:
		for k, v := range value {
			result[k] = resolveConfigString(v)
		}
	case map[string]any:
		for k, v := range value {
			if text, ok := v.(string); ok {
				result[k] = resolveConfigString(text)
			}
		}
	}
	return result
}

func resolveConfigString(value string) string {
	if strings.HasPrefix(value, "env.") {
		return os.Getenv(strings.TrimPrefix(value, "env."))
	}
	return value
}

func dedupKey(rule *tables.TableAlertRule, input map[string]any) string {
	raw := fmt.Sprintf("%s|%s|%s|%v|%v", rule.ID, rule.ScopeType, rule.ScopeID, input["target_type"], input["target_id"])
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

func alertMessage(rule *tables.TableAlertRule, input map[string]any, now time.Time) string {
	if isTestNotification(input) {
		return fmt.Sprintf(
			"Bifrost notification channel test\nChannel: %s\nType: %s\nStatus: connection successful\nTest time: %s",
			displayValue(input["channel_name"]), displayValue(input["channel_type"]), now.UTC().Format(time.RFC3339),
		)
	}
	keys := make([]string, 0, len(input))
	for key := range input {
		if key != "scope_type" && key != "scope_id" && key != "target_type" && key != "target_id" {
			if rule.ScopeType == "provider" && key != "provider" && key != "model" && key != "window_seconds" && !strings.HasPrefix(key, "provider_") {
				continue
			}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, fmt.Sprintf("%s=%v", key, input[key]))
	}
	return fmt.Sprintf("Alert matched: %s\nScope: %s/%s\nTarget: %v/%v\nExpression: %s\nValues: %s", rule.Name, rule.ScopeType, rule.ScopeID, input["target_type"], input["target_id"], rule.CELExpression, strings.Join(values, ", "))
}

func (m *Manager) logFailure(ctx context.Context, rule *tables.TableAlertRule, channel *tables.TableAlertChannel, input map[string]any, detail string) {
	m.record(ctx, rule, channel, input, "failed", detail)
}

func (m *Manager) record(ctx context.Context, rule *tables.TableAlertRule, channel *tables.TableAlertChannel, input map[string]any, status, detail string) {
	history := &logstore.AlertHistory{
		ID: uuid.NewString(), RuleID: rule.ID, RuleName: rule.Name, ScopeType: rule.ScopeType, ScopeID: rule.ScopeID,
		CELExpression: rule.CELExpression, Evaluation: historyEvaluation(rule, input), Status: status, StatusDetail: detail, CreatedAt: time.Now().UTC(),
	}
	if input != nil {
		history.TargetType, _ = input["target_type"].(string)
		history.TargetID, _ = input["target_id"].(string)
	}
	if channel != nil {
		history.ChannelID, history.ChannelName, history.ChannelType = channel.ID, channel.Name, channel.Type
	}
	if err := m.history.CreateAlertHistory(ctx, history); err != nil && m.logger != nil {
		m.logger.Error("alerting: failed to write history: %v", err)
	}
}

func historyEvaluation(rule *tables.TableAlertRule, input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	keys := []string{
		"budget_usage_percent", "budget_spent", "budget_limit",
		"rate_limit_request_usage_percent", "request_usage", "request_limit",
		"rate_limit_token_usage_percent", "token_usage", "token_limit",
	}
	if rule.ScopeType == "provider" {
		keys = []string{"provider_error_rate", "provider_error_count", "provider_success_count", "provider_request_count", "window_seconds"}
	}
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, exists := input[key]; exists {
			result[key] = value
		}
	}
	return result
}

func (m *Manager) restoreCooldowns(ctx context.Context) error {
	cooldowns, err := m.store.ListAlertCooldowns(ctx)
	if err != nil {
		return err
	}
	ruleHistory, err := m.history.ListLatestAlertRuleSends(ctx)
	if err != nil {
		return err
	}
	m.cooldownsMu.Lock()
	defer m.cooldownsMu.Unlock()
	persistedRuleKeys := make(map[string]struct{})
	for _, cooldown := range cooldowns {
		if strings.HasPrefix(cooldown.Key, "rule:") || strings.HasPrefix(cooldown.Key, "cycle:") {
			persistedRuleKeys[cooldown.Key] = struct{}{}
			if cooldown.LastSentAt.After(m.ruleSent[cooldown.Key]) {
				m.ruleSent[cooldown.Key] = cooldown.LastSentAt
			}
		} else if strings.HasPrefix(cooldown.Key, "suppression:") || strings.HasPrefix(cooldown.Key, "cycle-suppression:") {
			if cooldown.LastSentAt.After(m.suppressionSeen[cooldown.Key]) {
				m.suppressionSeen[cooldown.Key] = cooldown.LastSentAt
			}
		}
	}
	for _, item := range ruleHistory {
		key := alertCooldownKey("rule", item.RuleID, item.ScopeType, item.ScopeID, item.TargetType, item.TargetID)
		if _, restored := persistedRuleKeys[key]; restored {
			continue
		}
		if item.CreatedAt.After(m.ruleSent[key]) {
			m.ruleSent[key] = item.CreatedAt
		}
	}
	return nil
}

// SyncConfig reconciles declarative config.json entries by stable ID.
func (m *Manager) SyncConfig(ctx context.Context, cfg *Config) error {
	declaredChannelIDs := make(map[string]struct{}, len(cfg.Channels))
	for _, spec := range cfg.Channels {
		enabled := true
		if spec.Enabled != nil {
			enabled = *spec.Enabled
		}
		channel := &tables.TableAlertChannel{ID: spec.ID, Name: spec.Name, Description: spec.Description, Type: spec.Type, Enabled: enabled, Config: spec.Config, ManagedByConfig: true}
		if channel.ID == "" {
			return fmt.Errorf("declarative alert channel id is required")
		}
		declaredChannelIDs[channel.ID] = struct{}{}
		if err := m.ValidateChannel(channel); err != nil {
			return fmt.Errorf("alert channel %s: %w", channel.ID, err)
		}
		if existing, err := m.store.GetAlertChannel(ctx, channel.ID); err == nil {
			channel.CreatedAt = existing.CreatedAt
			if err := m.store.UpdateAlertChannel(ctx, channel); err != nil {
				return err
			}
		} else if errors.Is(err, configstore.ErrNotFound) {
			if err := m.store.CreateAlertChannel(ctx, channel); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	channelsAfterUpsert, err := m.store.ListAlertChannels(ctx)
	if err != nil {
		return err
	}
	channelsByID := make(map[string]tables.TableAlertChannel, len(channelsAfterUpsert))
	for _, channel := range channelsAfterUpsert {
		channelsByID[channel.ID] = channel
	}
	declaredRuleIDs := make(map[string]struct{}, len(cfg.Rules))
	for _, spec := range cfg.Rules {
		enabled := true
		if spec.Enabled != nil {
			enabled = *spec.Enabled
		}
		for _, channelID := range spec.ChannelIDs {
			if channel, exists := channelsByID[channelID]; exists && channel.ManagedByConfig {
				if _, remainsDeclared := declaredChannelIDs[channelID]; !remainsDeclared {
					return fmt.Errorf("alert rule %s references config-managed channel %s that is no longer declared", spec.ID, channelID)
				}
			}
		}
		cooldown := int64(DefaultRuleCooldown / time.Millisecond)
		if spec.CooldownSeconds != nil {
			cooldown = *spec.CooldownSeconds * 1000
		}
		query := spec.QueryBuilder
		if query == nil {
			query = spec.LegacyQueryBuilder
		}
		rule := &tables.TableAlertRule{ID: spec.ID, Name: spec.Name, Description: spec.Description, Enabled: enabled, ScopeType: spec.ScopeType, ScopeID: spec.ScopeID, TargetType: spec.TargetType, TargetID: spec.TargetID, CELExpression: spec.CELExpression, ChannelIDs: spec.ChannelIDs, QueryBuilder: query, CooldownMilliseconds: cooldown, WindowSeconds: spec.WindowSeconds, MinRequests: spec.MinRequests, NotifyOncePerResetCycle: spec.NotifyOncePerResetCycle, ManagedByConfig: true}
		if rule.ID == "" {
			return fmt.Errorf("declarative alert rule id is required")
		}
		declaredRuleIDs[rule.ID] = struct{}{}
		if err := m.ValidateRule(ctx, rule); err != nil {
			return fmt.Errorf("alert rule %s: %w", rule.ID, err)
		}
		if existing, err := m.store.GetAlertRule(ctx, rule.ID); err == nil {
			rule.CreatedAt = existing.CreatedAt
			if err := m.store.UpdateAlertRule(ctx, rule); err != nil {
				return err
			}
		} else if errors.Is(err, configstore.ErrNotFound) {
			if err := m.store.CreateAlertRule(ctx, rule); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	existingRules, err := m.store.ListAlertRules(ctx)
	if err != nil {
		return err
	}
	for _, rule := range existingRules {
		if !rule.ManagedByConfig {
			continue
		}
		if _, declared := declaredRuleIDs[rule.ID]; !declared {
			if err := m.store.DeleteAlertRule(ctx, rule.ID); err != nil {
				return fmt.Errorf("delete stale config-managed alert rule %s: %w", rule.ID, err)
			}
		}
	}
	existingChannels, err := m.store.ListAlertChannels(ctx)
	if err != nil {
		return err
	}
	for _, channel := range existingChannels {
		if !channel.ManagedByConfig {
			continue
		}
		if _, declared := declaredChannelIDs[channel.ID]; !declared {
			if err := m.store.DeleteAlertChannel(ctx, channel.ID); err != nil {
				return fmt.Errorf("delete stale config-managed alert channel %s: %w", channel.ID, err)
			}
		}
	}
	return nil
}
