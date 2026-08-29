package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	alertengine "github.com/maximhq/bifrost/transports/bifrost-http/alerting"
	"github.com/valyala/fasthttp"
)

type alertHandlerTestStore struct {
	configstore.AlertStore
	alertengine.HistoryStore
	channel tables.TableAlertChannel
	rule    tables.TableAlertRule
}

func (s *alertHandlerTestStore) GetAlertChannel(context.Context, string) (*tables.TableAlertChannel, error) {
	channel := s.channel
	return &channel, nil
}

func (s *alertHandlerTestStore) ListAlertChannels(context.Context) ([]tables.TableAlertChannel, error) {
	return []tables.TableAlertChannel{s.channel}, nil
}

func (s *alertHandlerTestStore) ListAlertRules(context.Context) ([]tables.TableAlertRule, error) {
	if s.rule.ID == "" {
		return nil, nil
	}
	return []tables.TableAlertRule{s.rule}, nil
}

func (s *alertHandlerTestStore) GetAlertRule(context.Context, string) (*tables.TableAlertRule, error) {
	rule := s.rule
	return &rule, nil
}

func (s *alertHandlerTestStore) GetStats(context.Context, logstore.SearchFilters) (*logstore.SearchStats, error) {
	return &logstore.SearchStats{TotalRequests: 1, SuccessRate: 0}, nil
}

func (s *alertHandlerTestStore) ListAlertCooldowns(context.Context) ([]tables.TableAlertCooldown, error) {
	return nil, nil
}

func (s *alertHandlerTestStore) CreateAlertHistory(context.Context, *logstore.AlertHistory) error {
	return nil
}

func (s *alertHandlerTestStore) ListAlertHistory(context.Context, logstore.AlertHistoryQuery) ([]logstore.AlertHistory, int64, error) {
	return nil, 0, nil
}

func (s *alertHandlerTestStore) DeleteAlertHistoryBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s *alertHandlerTestStore) ListLatestAlertRuleSends(context.Context) ([]logstore.AlertHistory, error) {
	return nil, nil
}

func TestMergeAlertChannelConfigPreservesRedactedSecretsAndAppliesPartialChanges(t *testing.T) {
	existing := map[string]any{
		"url":     "https://example.com/hook",
		"headers": map[string]any{"X-API-Key": "old-secret", "X-Tenant": "old-tenant"},
	}
	incoming := map[string]any{
		"url":     "***redacted***",
		"headers": map[string]any{"X-API-Key": "***redacted***", "X-Tenant": "new-tenant"},
	}
	want := map[string]any{
		"url":     "https://example.com/hook",
		"headers": map[string]any{"X-API-Key": "old-secret", "X-Tenant": "new-tenant"},
	}
	if got := mergeAlertChannelConfig(existing, incoming); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected merged config: %#v", got)
	}
}

func TestMergeAlertChannelConfigTreatsEmptyConfigAsNoSecretChange(t *testing.T) {
	existing := map[string]any{"routing_key": "secret"}
	if got := mergeAlertChannelConfig(existing, map[string]any{}); !reflect.DeepEqual(got, existing) {
		t.Fatalf("unexpected merged config: %#v", got)
	}
}

func TestAlertChannelTestAPIDoesNotLeakWeComResponseMessage(t *testing.T) {
	const secretResponse = "secret webhook token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":93000,"errmsg":"` + secretResponse + `"}`))
	}))
	defer server.Close()

	store := &alertHandlerTestStore{channel: tables.TableAlertChannel{
		ID: "c1", Name: "WeCom", Type: tables.AlertChannelWeCom, Enabled: true,
		Config: map[string]any{"webhook_url": server.URL},
	}, rule: tables.TableAlertRule{
		ID: "r1", Name: "Always", Enabled: true, ScopeType: "provider", ScopeID: "openai",
		CELExpression: "provider_error_rate >= 0.0", ChannelIDs: []string{"c1"}, WindowSeconds: 300, MinRequests: 1,
	}}
	manager, err := alertengine.NewManager(store, nil, store, store, nil, &alertengine.Config{WebhookNetwork: alertengine.NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAlertingHandler(manager, store)
	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "c1")
	handler.testChannel(ctx)
	response := string(ctx.Response.Body())
	if ctx.Response.StatusCode() != fasthttp.StatusBadGateway || strings.Contains(response, secretResponse) || !strings.Contains(response, "errcode 93000") {
		t.Fatalf("unsafe API response status=%d body=%s", ctx.Response.StatusCode(), response)
	}

	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "r1")
	ctx.Request.SetBodyString(`{"ignore_cooldown":true}`)
	handler.evaluateRuleNow(ctx)
	response = string(ctx.Response.Body())
	if ctx.Response.StatusCode() != fasthttp.StatusOK || strings.Contains(response, secretResponse) || !strings.Contains(response, `"failed_count":1`) {
		t.Fatalf("unsafe manual evaluation API response status=%d body=%s", ctx.Response.StatusCode(), response)
	}
}
