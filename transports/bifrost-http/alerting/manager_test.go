package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/governance"
)

type memoryAlertStore struct {
	channels  []tables.TableAlertChannel
	rules     []tables.TableAlertRule
	history   []logstore.AlertHistory
	cooldowns map[string]time.Time
}

func (s *memoryAlertStore) ListAlertChannels(context.Context) ([]tables.TableAlertChannel, error) {
	return s.channels, nil
}
func (s *memoryAlertStore) GetAlertChannel(_ context.Context, id string) (*tables.TableAlertChannel, error) {
	for i := range s.channels {
		if s.channels[i].ID == id {
			clone := s.channels[i]
			return &clone, nil
		}
	}
	return nil, configstore.ErrNotFound
}
func (s *memoryAlertStore) CreateAlertChannel(_ context.Context, v *tables.TableAlertChannel) error {
	s.channels = append(s.channels, *v)
	return nil
}
func (s *memoryAlertStore) UpdateAlertChannel(_ context.Context, v *tables.TableAlertChannel) error {
	for i := range s.channels {
		if s.channels[i].ID == v.ID {
			s.channels[i] = *v
			return nil
		}
	}
	return configstore.ErrNotFound
}
func (s *memoryAlertStore) DeleteAlertChannel(_ context.Context, id string) error {
	filtered := s.channels[:0]
	for _, channel := range s.channels {
		if channel.ID != id {
			filtered = append(filtered, channel)
		}
	}
	s.channels = filtered
	return nil
}
func (s *memoryAlertStore) ListAlertRules(context.Context) ([]tables.TableAlertRule, error) {
	return s.rules, nil
}
func (s *memoryAlertStore) GetAlertRule(_ context.Context, id string) (*tables.TableAlertRule, error) {
	for i := range s.rules {
		if s.rules[i].ID == id {
			clone := s.rules[i]
			return &clone, nil
		}
	}
	return nil, configstore.ErrNotFound
}
func (s *memoryAlertStore) CreateAlertRule(_ context.Context, v *tables.TableAlertRule) error {
	s.rules = append(s.rules, *v)
	return nil
}
func (s *memoryAlertStore) UpdateAlertRule(_ context.Context, v *tables.TableAlertRule) error {
	for i := range s.rules {
		if s.rules[i].ID == v.ID {
			s.rules[i] = *v
			return nil
		}
	}
	return configstore.ErrNotFound
}
func (s *memoryAlertStore) DeleteAlertRule(_ context.Context, id string) error {
	filtered := s.rules[:0]
	for _, rule := range s.rules {
		if rule.ID != id {
			filtered = append(filtered, rule)
		}
	}
	s.rules = filtered
	return nil
}
func (s *memoryAlertStore) CreateAlertHistory(_ context.Context, v *logstore.AlertHistory) error {
	s.history = append(s.history, *v)
	return nil
}
func (s *memoryAlertStore) ListAlertHistory(_ context.Context, q logstore.AlertHistoryQuery) ([]logstore.AlertHistory, int64, error) {
	return s.history, int64(len(s.history)), nil
}
func (s *memoryAlertStore) DeleteAlertHistoryBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (s *memoryAlertStore) ListAlertCooldowns(context.Context) ([]tables.TableAlertCooldown, error) {
	result := make([]tables.TableAlertCooldown, 0, len(s.cooldowns))
	for key, lastSentAt := range s.cooldowns {
		result = append(result, tables.TableAlertCooldown{Key: key, LastSentAt: lastSentAt})
	}
	return result, nil
}
func (s *memoryAlertStore) UpsertAlertCooldown(_ context.Context, key string, lastSentAt time.Time) error {
	if s.cooldowns == nil {
		s.cooldowns = make(map[string]time.Time)
	}
	s.cooldowns[key] = lastSentAt
	return nil
}
func (s *memoryAlertStore) DeleteAlertSuppressionsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	var deleted int64
	for key, lastSentAt := range s.cooldowns {
		if (strings.HasPrefix(key, "suppression:") || strings.HasPrefix(key, "cycle-suppression:")) && lastSentAt.Before(cutoff) {
			delete(s.cooldowns, key)
			deleted++
		}
	}
	return deleted, nil
}
func (s *memoryAlertStore) ListLatestAlertRuleSends(context.Context) ([]logstore.AlertHistory, error) {
	latest := make(map[string]logstore.AlertHistory)
	for _, row := range s.history {
		if row.Status != "sent" {
			continue
		}
		key := row.RuleID + "|" + row.ScopeType + "|" + row.ScopeID + "|" + row.TargetType + "|" + row.TargetID
		if current, ok := latest[key]; !ok || row.CreatedAt.After(current.CreatedAt) {
			latest[key] = row
		}
	}
	result := make([]logstore.AlertHistory, 0, len(latest))
	for _, row := range latest {
		result = append(result, row)
	}
	return result, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type fakeMetricsStore struct {
	total  int64
	errors int64
	calls  []logstore.SearchFilters
}

func (s *fakeMetricsStore) GetStats(_ context.Context, filters logstore.SearchFilters) (*logstore.SearchStats, error) {
	s.calls = append(s.calls, filters)
	successRate := 0.0
	if s.total > 0 {
		successRate = float64(s.total-s.errors) / float64(s.total) * 100
	}
	return &logstore.SearchStats{TotalRequests: s.total, SuccessRate: successRate}, nil
}

func TestEvaluateNowDispatchesAndAppliesRuleCooldown(t *testing.T) {
	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{{ID: "channel-1", Name: "Local", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "http://127.0.0.1/alerts"}}},
		rules:    []tables.TableAlertRule{{ID: "rule-1", Name: "Budget high", Enabled: true, ScopeType: "virtual_key", ScopeID: "vk-1", CELExpression: "budget_usage_percent >= 80.0", ChannelIDs: []string{"channel-1"}, CooldownMilliseconds: 60000}},
	}
	snapshot := func(context.Context) *governance.GovernanceData {
		return &governance.GovernanceData{VirtualKeys: map[string]*tables.TableVirtualKey{"secret": {ID: "vk-1", Budgets: []tables.TableBudget{{ID: "budget-1", MaxLimit: 100, CurrentUsage: 90}}}}}
	}
	manager, err := NewManager(store, snapshot, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	restartedManager, err := NewManager(store, snapshot, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	restartedManager.privateClient = manager.privateClient
	if err := restartedManager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.history) != 2 {
		t.Fatalf("expected one sent and one durable coalesced cooldown history record, got %d", len(store.history))
	}
	if store.history[0].Status != "sent" || store.history[1].Status != "skipped" {
		t.Fatalf("unexpected statuses: %s (%s), %s (%s)", store.history[0].Status, store.history[0].StatusDetail, store.history[1].Status, store.history[1].StatusDetail)
	}
	if got := store.history[0].Evaluation["budget_usage_percent"]; got != 90.0 {
		t.Fatalf("expected 90%% usage, got %v", got)
	}
	if _, exists := store.history[0].Evaluation["provider_error_rate"]; exists {
		t.Fatalf("governance history must not contain provider metrics: %#v", store.history[0].Evaluation)
	}
}

func TestValidateExpressionRequiresBoolean(t *testing.T) {
	store := &memoryAlertStore{}
	manager, err := NewManager(store, func(context.Context) *governance.GovernanceData { return &governance.GovernanceData{} }, nil, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateExpression("budget_spent + 1.0"); err == nil {
		t.Fatal("expected non-boolean expression to fail")
	}
	if err := manager.ValidateExpression("request_usage > 10 && token_usage > 20"); err != nil {
		t.Fatalf("expected valid expression: %v", err)
	}
}

func TestRuleCooldownIsMeasuredFromSuccessfulSend(t *testing.T) {
	store := &memoryAlertStore{history: []logstore.AlertHistory{{RuleID: "r", ScopeType: "team", ScopeID: "t", TargetType: "budget", TargetID: "b", ChannelID: "c", Status: "sent", CreatedAt: time.Now().Add(-time.Minute)}}}
	manager, err := NewManager(store, func(context.Context) *governance.GovernanceData { return &governance.GovernanceData{} }, nil, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if manager.ruleSent[alertCooldownKey("rule", "r", "team", "t", "budget", "b")].IsZero() {
		t.Fatal("expected cooldown state to be restored")
	}
}

func TestProviderErrorRateRuleUsesRollingLogWindow(t *testing.T) {
	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{{ID: "channel-1", Name: "Local", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "http://127.0.0.1/alerts"}}},
		rules:    []tables.TableAlertRule{{ID: "provider-rule", Name: "OpenAI errors", Enabled: true, ScopeType: "provider", ScopeID: "openai", TargetType: stringPtr("model"), TargetID: stringPtr("gpt-4o"), CELExpression: "provider_error_rate >= 10.0", ChannelIDs: []string{"channel-1"}, CooldownMilliseconds: 60000, WindowSeconds: 300, MinRequests: 50}},
	}
	metrics := &fakeMetricsStore{total: 100, errors: 20}
	manager, err := NewManager(store, nil, metrics, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.history) != 1 || store.history[0].Status != "sent" {
		t.Fatalf("expected one sent alert, got %#v", store.history)
	}
	input := store.history[0].Evaluation
	if input["provider_error_rate"] != 20.0 || input["provider_request_count"] != int64(100) || input["provider_error_count"] != int64(20) {
		t.Fatalf("unexpected provider metrics: %#v", input)
	}
	if _, exists := input["budget_limit"]; exists {
		t.Fatalf("provider history must not contain governance metrics: %#v", input)
	}
	if len(metrics.calls) != 1 || metrics.calls[0].Providers[0] != "openai" || metrics.calls[0].Models[0] != "gpt-4o" {
		t.Fatalf("unexpected log filters: %#v", metrics.calls)
	}
	window := metrics.calls[0].EndTime.Sub(*metrics.calls[0].StartTime)
	if window < 299*time.Second || window > 301*time.Second {
		t.Fatalf("unexpected rolling window: %s", window)
	}
}

func TestProviderErrorRateRuleHonorsMinimumSamples(t *testing.T) {
	store := &memoryAlertStore{rules: []tables.TableAlertRule{{ID: "provider-rule", Name: "OpenAI errors", Enabled: true, ScopeType: "provider", ScopeID: "openai", CELExpression: "provider_error_rate >= 10.0", WindowSeconds: 300, MinRequests: 100}}}
	manager, err := NewManager(store, nil, &fakeMetricsStore{total: 20, errors: 20}, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.history) != 0 {
		t.Fatalf("expected no alert below minimum samples, got %#v", store.history)
	}
}

func TestValidateProviderRuleRequiresLoggingAndAcceptsModelTarget(t *testing.T) {
	store := &memoryAlertStore{channels: []tables.TableAlertChannel{{ID: "channel-1"}}}
	rule := &tables.TableAlertRule{Name: "Provider errors", ScopeType: "provider", ScopeID: "openai", TargetType: stringPtr("model"), TargetID: stringPtr("gpt-4o"), CELExpression: "provider_error_rate >= 10.0", ChannelIDs: []string{"channel-1"}, WindowSeconds: 300, MinRequests: 10}
	withoutLogs, err := NewManager(store, nil, nil, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withoutLogs.ValidateRule(context.Background(), rule); err == nil || !strings.Contains(err.Error(), "logging") {
		t.Fatalf("expected logging validation error, got %v", err)
	}
	withLogs, err := NewManager(store, nil, &fakeMetricsStore{}, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withLogs.ValidateRule(context.Background(), rule); err != nil {
		t.Fatalf("expected provider rule to validate: %v", err)
	}
	rule.WindowSeconds = 3 * 24 * 60 * 60
	if err := withLogs.ValidateRule(context.Background(), rule); err != nil {
		t.Fatalf("expected a multi-day provider window to validate: %v", err)
	}
	rule.WindowSeconds = MaxProviderWindowSeconds + 1
	if err := withLogs.ValidateRule(context.Background(), rule); err == nil || !strings.Contains(err.Error(), "window_seconds") {
		t.Fatalf("expected oversized window error, got %v", err)
	}
	rule.WindowSeconds = 300
	withLogs.SetProviderValidator(func(name string) bool { return name == "anthropic" })
	if err := withLogs.ValidateRule(context.Background(), rule); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing provider error, got %v", err)
	}
}

func TestConcurrentEvaluationsDoNotDuplicateDelivery(t *testing.T) {
	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{{ID: "channel-1", Name: "Local", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "http://127.0.0.1/alerts"}}},
		rules:    []tables.TableAlertRule{{ID: "rule-1", Name: "Budget high", Enabled: true, ScopeType: "virtual_key", ScopeID: "vk-1", CELExpression: "budget_usage_percent >= 80.0", ChannelIDs: []string{"channel-1"}, CooldownMilliseconds: 60000, WindowSeconds: 300, MinRequests: 1}},
	}
	snapshot := func(context.Context) *governance.GovernanceData {
		return &governance.GovernanceData{VirtualKeys: map[string]*tables.TableVirtualKey{"secret": {ID: "vk-1", Budgets: []tables.TableBudget{{ID: "budget-1", MaxLimit: 100, CurrentUsage: 90}}}}}
	}
	manager, err := NewManager(store, snapshot, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	var deliveries atomic.Int64
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		deliveries.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.EvaluateNow(context.Background()); err != nil {
				t.Errorf("evaluation failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := deliveries.Load(); got != 1 {
		t.Fatalf("expected one delivery, got %d", got)
	}
}

func TestNotifyOncePerResetCycleSendsAgainAfterReset(t *testing.T) {
	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{{ID: "channel-1", Name: "Local", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "http://127.0.0.1/alerts"}}},
		rules:    []tables.TableAlertRule{{ID: "rule-1", Name: "Budget high", Enabled: true, ScopeType: "virtual_key", ScopeID: "vk-1", CELExpression: "budget_usage_percent >= 80.0", ChannelIDs: []string{"channel-1"}, CooldownMilliseconds: 0, WindowSeconds: 300, MinRequests: 1, NotifyOncePerResetCycle: true}},
	}
	lastReset := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	snapshot := func(context.Context) *governance.GovernanceData {
		return &governance.GovernanceData{VirtualKeys: map[string]*tables.TableVirtualKey{"secret": {ID: "vk-1", Budgets: []tables.TableBudget{{ID: "budget-1", MaxLimit: 100, CurrentUsage: 90, LastReset: lastReset}}}}}
	}
	manager, err := NewManager(store, snapshot, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	var deliveries atomic.Int64
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		deliveries.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	lastReset = lastReset.Add(time.Hour)
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := deliveries.Load(); got != 2 {
		t.Fatalf("expected one send per reset cycle, got %d", got)
	}
	if len(store.history) != 3 || store.history[1].Status != "skipped" {
		t.Fatalf("unexpected history: %#v", store.history)
	}
}

func TestEvaluationIntervalConfiguresSweep(t *testing.T) {
	store := &memoryAlertStore{}
	manager, err := NewManager(store, nil, nil, store, nil, &Config{EvaluationIntervalSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if manager.sweepInterval != 10*time.Second {
		t.Fatalf("unexpected interval: %s", manager.sweepInterval)
	}
	if _, err := NewManager(store, nil, nil, store, nil, &Config{EvaluationIntervalSeconds: 1}); err == nil {
		t.Fatal("expected invalid interval to fail")
	}
}

func TestSyncConfigPrunesOnlyStaleConfigManagedResources(t *testing.T) {
	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{{ID: "stale-channel", ManagedByConfig: true}, {ID: "api-channel"}},
		rules:    []tables.TableAlertRule{{ID: "stale-rule", ManagedByConfig: true}, {ID: "api-rule"}},
	}
	manager, err := NewManager(store, nil, nil, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncConfig(context.Background(), &Config{}); err != nil {
		t.Fatal(err)
	}
	if len(store.channels) != 1 || store.channels[0].ID != "api-channel" {
		t.Fatalf("unexpected channels: %#v", store.channels)
	}
	if len(store.rules) != 1 || store.rules[0].ID != "api-rule" {
		t.Fatalf("unexpected rules: %#v", store.rules)
	}
}

func TestMissingGovernanceScopeDoesNotEvaluateSyntheticZeroMetrics(t *testing.T) {
	store := &memoryAlertStore{rules: []tables.TableAlertRule{{ID: "rule-1", Name: "Zero budget", Enabled: true, ScopeType: "team", ScopeID: "deleted-team", CELExpression: "budget_spent == 0.0", WindowSeconds: 300, MinRequests: 1}}}
	manager, err := NewManager(store, func(context.Context) *governance.GovernanceData {
		return &governance.GovernanceData{Teams: map[string]*tables.TableTeam{}}
	}, nil, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EvaluateNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.history) != 1 || store.history[0].Status != "failed" || !strings.Contains(store.history[0].StatusDetail, "no longer exists") {
		t.Fatalf("unexpected history: %#v", store.history)
	}
}

func TestDeliveryErrorDoesNotExposeResponseBody(t *testing.T) {
	store := &memoryAlertStore{}
	manager, err := NewManager(store, nil, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("downstream-secret")), Header: make(http.Header)}, nil
	})}
	rule := &tables.TableAlertRule{ID: "r1", Name: "Rule", ScopeType: "provider", ScopeID: "openai", CELExpression: "true"}
	channel := &tables.TableAlertChannel{ID: "c1", Type: tables.AlertChannelWebhook, Config: map[string]any{"url": "http://127.0.0.1/hook"}}
	err = manager.deliver(context.Background(), rule, channel, map[string]any{"target_type": "", "target_id": ""}, time.Now())
	if err == nil || strings.Contains(err.Error(), "downstream-secret") {
		t.Fatalf("unexpected delivery error: %v", err)
	}
}

func TestChannelSendsClearlyMarkedTestNotificationWithoutHistory(t *testing.T) {
	store := &memoryAlertStore{}
	manager, err := NewManager(store, nil, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	channel := &tables.TableAlertChannel{ID: "c1", Name: "Ops webhook", Type: tables.AlertChannelWebhook, Config: map[string]any{"url": "http://127.0.0.1/hook"}}
	if err := manager.TestChannel(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != "alert.test" {
		t.Fatalf("unexpected test event: %#v", payload)
	}
	channelPayload, ok := payload["channel"].(map[string]any)
	if !ok || channelPayload["name"] != "Ops webhook" {
		t.Fatalf("test notification is not clearly marked: %#v", payload)
	}
	if _, exists := payload["rule"]; exists {
		t.Fatalf("test payload must not pretend to be a rule alert: %#v", payload)
	}
	if len(store.history) != 0 {
		t.Fatalf("test notification polluted alert history: %#v", store.history)
	}
	if severity := alertSeverity(map[string]any{"test_notification": true}); severity != "info" {
		t.Fatalf("unexpected test severity: %s", severity)
	}
}

func TestWeComTestMessageUsesChannelSummaryAndChinaTime(t *testing.T) {
	rule := &tables.TableAlertRule{Name: "Bifrost 通知渠道测试", ScopeType: "system", ScopeID: "bifrost", CELExpression: "true"}
	input := map[string]any{"test_notification": true, "channel_name": "平台告警群", "channel_type": "wecom"}
	content := weComAlertMarkdown(rule, input, time.Date(2026, 8, 24, 13, 41, 18, 0, time.UTC))
	for _, expected := range []string{"通知渠道测试", "平台告警群", "连接正常", "2026-08-24 21:41:18 UTC+08:00"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("test markdown does not contain %q: %s", expected, content)
		}
	}
	for _, unexpected := range []string{"规则：", "作用域：", "条件："} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("test markdown contains irrelevant field %q: %s", unexpected, content)
		}
	}
}

func TestWeComDeliveryUsesDynamicMarkdownAndChecksErrorCode(t *testing.T) {
	store := &memoryAlertStore{}
	manager, err := NewManager(store, nil, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	var received struct {
		MessageType string `json:"msgtype"`
		Markdown    struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`)), Header: make(http.Header)}, nil
	})}
	rule := &tables.TableAlertRule{ID: "r1", Name: "OpenAI 错误率", ScopeType: "provider", ScopeID: "openai", CELExpression: "provider_error_rate >= 10"}
	channel := &tables.TableAlertChannel{ID: "c1", Type: tables.AlertChannelWeCom, Config: map[string]any{"webhook_url": "http://127.0.0.1/hook"}}
	input := map[string]any{
		"provider": "openai", "model": "gpt-4.1", "provider_error_rate": 12.5,
		"provider_error_count": int64(25), "provider_request_count": int64(200), "window_seconds": int64(300),
	}
	if err := manager.deliver(context.Background(), rule, channel, input, time.Date(2026, 8, 24, 12, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if received.MessageType != "markdown" {
		t.Fatalf("unexpected message type: %q", received.MessageType)
	}
	for _, expected := range []string{"OpenAI 错误率", "openai", "gpt-4.1", "12.50%", "25 / 200", "最近 5 分钟"} {
		if !strings.Contains(received.Markdown.Content, expected) {
			t.Fatalf("markdown does not contain %q: %s", expected, received.Markdown.Content)
		}
	}

	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errcode":93000,"errmsg":"invalid webhook url"}`)), Header: make(http.Header)}, nil
	})}
	if err := manager.deliver(context.Background(), rule, channel, input, time.Now()); err == nil || !strings.Contains(err.Error(), "errcode 93000") {
		t.Fatalf("expected WeCom API error, got %v", err)
	}
}

func TestWeComMarkdownRespectsByteLimit(t *testing.T) {
	rule := &tables.TableAlertRule{Name: strings.Repeat("告警", 3000), ScopeType: "provider", CELExpression: "true"}
	content := weComAlertMarkdown(rule, map[string]any{}, time.Now())
	if len(content) > maxWeComMarkdownBytes {
		t.Fatalf("content has %d bytes", len(content))
	}
	if !strings.HasSuffix(content, "…") {
		t.Fatalf("expected truncated content")
	}
}

func TestEvaluateRuleNowRespectsAndCanOverrideCooldown(t *testing.T) {
	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{{ID: "channel-1", Name: "Local", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "http://127.0.0.1/alerts"}}},
		rules:    []tables.TableAlertRule{{ID: "provider-rule", Name: "Provider errors", Enabled: true, ScopeType: "provider", ScopeID: "openai", CELExpression: "provider_error_count >= 3", ChannelIDs: []string{"channel-1"}, CooldownMilliseconds: 60000, WindowSeconds: 300, MinRequests: 1}},
	}
	metrics := &fakeMetricsStore{total: 10, errors: 3}
	manager, err := NewManager(store, nil, metrics, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	var deliveries atomic.Int64
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		deliveries.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}

	first, err := manager.EvaluateRuleNow(context.Background(), "provider-rule", false)
	if err != nil || first.SentCount != 1 || !first.Matched {
		t.Fatalf("unexpected first result: %#v, %v", first, err)
	}
	second, err := manager.EvaluateRuleNow(context.Background(), "provider-rule", false)
	if err != nil || second.SkippedCount != 1 || second.SentCount != 0 {
		t.Fatalf("expected cooldown suppression: %#v, %v", second, err)
	}
	forced, err := manager.EvaluateRuleNow(context.Background(), "provider-rule", true)
	if err != nil || forced.SentCount != 1 || !forced.CooldownIgnored {
		t.Fatalf("expected forced send: %#v, %v", forced, err)
	}
	if deliveries.Load() != 2 {
		t.Fatalf("expected two deliveries, got %d", deliveries.Load())
	}
	if detail := store.history[len(store.history)-1].StatusDetail; detail != "manual override: cooldown ignored" {
		t.Fatalf("forced send was not audited: %q", detail)
	}
}

type blockingMetricsStore struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingMetricsStore) GetStats(context.Context, logstore.SearchFilters) (*logstore.SearchStats, error) {
	close(s.started)
	<-s.release
	return &logstore.SearchStats{TotalRequests: 10, SuccessRate: 70}, nil
}

func TestEvaluateRuleNowRejectsDuplicateInFlightRun(t *testing.T) {
	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{{ID: "channel-1", Name: "Local", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "http://127.0.0.1/alerts"}}},
		rules:    []tables.TableAlertRule{{ID: "provider-rule", Name: "Provider errors", Enabled: true, ScopeType: "provider", ScopeID: "openai", CELExpression: "provider_error_count >= 3", ChannelIDs: []string{"channel-1"}, WindowSeconds: 300, MinRequests: 1}},
	}
	metrics := &blockingMetricsStore{started: make(chan struct{}), release: make(chan struct{})}
	manager, err := NewManager(store, nil, metrics, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	done := make(chan error, 1)
	go func() {
		_, runErr := manager.EvaluateRuleNow(context.Background(), "provider-rule", false)
		done <- runErr
	}()
	<-metrics.started
	if running := manager.RunningRuleEvaluations(); len(running) != 1 || running[0] != "provider-rule" {
		t.Fatalf("unexpected running rules: %#v", running)
	}
	if _, err := manager.EvaluateRuleNow(context.Background(), "provider-rule", false); !errors.Is(err, ErrRuleEvaluationInProgress) {
		t.Fatalf("expected in-progress error, got %v", err)
	}
	close(metrics.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func stringPtr(value string) *string { return &value }
