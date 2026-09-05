package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/routing/complexity"
	"github.com/valyala/fasthttp"
)

type mockRoutingManager struct {
	RoutingManager
	reloadedConfig *complexity.AnalyzerConfig
	reloadCalls    int
	reloadErr      error
	reloadIDs      []string
}

func (m *mockRoutingManager) ReloadRoutingRule(_ context.Context, id string) error {
	m.reloadIDs = append(m.reloadIDs, id)
	return m.reloadErr
}

func (m *mockRoutingManager) RemoveRoutingRule(context.Context, string) error { return nil }

func (m *mockRoutingManager) ReloadComplexityAnalyzerConfig(_ context.Context, config *complexity.AnalyzerConfig) error {
	m.reloadCalls++
	m.reloadedConfig = config
	return m.reloadErr
}

func testComplexityAnalyzerPayload(t *testing.T, cfg complexity.AnalyzerConfig) string {
	t.Helper()
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal complexity analyzer config: %v", err)
	}
	return string(body)
}

// unreachableConfigStore fails the complexity read the way an unreachable
// database does, and delegates everything else. The embedded interface is nil,
// so any other call panics rather than quietly returning a zero value.
type unreachableConfigStore struct {
	configstore.ConfigStore
}

func (unreachableConfigStore) GetComplexityAnalyzerConfig(context.Context) (*configstore.ComplexityAnalyzerConfig, error) {
	return nil, errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
}

// TestComplexityAnalyzerConfigGetDegradesOnUnreadableConfig covers a stored
// config this version cannot parse — after a rollback, for instance. The
// analyzer has already fallen back to defaults for the same reason, logging a
// warning, so the page must show what is actually running instead of failing.
func TestComplexityAnalyzerConfigGetDegradesOnUnreadableConfig(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)

	// Well-formed JSON, but the boundaries are out of order, so it fails
	// validation on the way out of the store.
	require.NoError(t, store.UpdateConfig(context.Background(), &tables.TableGovernanceConfig{
		Key:   tables.ConfigComplexityAnalyzerConfigKey,
		Value: `{"tier_boundaries":{"simple_medium":0.9,"medium_complex":0.1}}`,
	}))

	_, err := store.GetComplexityAnalyzerConfig(context.Background())
	require.ErrorIs(t, err, configstore.ErrConfigUnreadable,
		"the store must mark this as unreadable, not as an infrastructure failure")

	handler := &RoutingHandler{configStore: store, routingManager: &mockRoutingManager{}}
	ctx := newTestRequestCtx("")
	handler.getComplexityAnalyzerConfig(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(),
		"an unreadable stored config must not take the page down: %s", string(ctx.Response.Body()))

	var resp complexity.AnalyzerConfig
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
	require.Equal(t, complexity.DefaultTierBoundaries(), resp.TierBoundaries)
}

// TestComplexityAnalyzerConfigGetStillFailsWhenStoreUnreachable is the other
// half: defaults are only correct when the config is unreadable. Serving them
// when the store is down would report a broken installation as a working one.
func TestComplexityAnalyzerConfigGetStillFailsWhenStoreUnreachable(t *testing.T) {
	SetLogger(&mockLogger{})
	handler := &RoutingHandler{
		configStore:    unreachableConfigStore{},
		routingManager: &mockRoutingManager{},
	}

	ctx := newTestRequestCtx("")
	handler.getComplexityAnalyzerConfig(ctx)

	require.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode(),
		"an unreachable store must surface as an error, not as defaults")
}

func TestComplexityAnalyzerConfigGetReturnsDefaultsWhenUnset(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	handler := &RoutingHandler{
		configStore:    store,
		routingManager: &mockRoutingManager{},
	}

	ctx := newTestRequestCtx("")
	handler.getComplexityAnalyzerConfig(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	var resp complexity.AnalyzerConfig
	if err := json.Unmarshal(ctx.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.TierBoundaries != complexity.DefaultTierBoundaries() {
		t.Fatalf("expected default boundaries, got %+v", resp.TierBoundaries)
	}
	if len(resp.Keywords.CodeKeywords) == 0 {
		t.Fatalf("expected default code keywords")
	}
}

func TestComplexityAnalyzerConfigPutPersistsAndReloads(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	manager := &mockRoutingManager{}
	handler := &RoutingHandler{
		configStore:    store,
		routingManager: manager,
	}

	cfg := complexity.DefaultAnalyzerConfig()
	cfg.TierBoundaries.SimpleMedium = 0.12
	cfg.TierBoundaries.MediumComplex = 0.34
	cfg.TierBoundaries.ComplexReasoning = 0.78
	cfg.Keywords.CodeKeywords = []string{" Function ", "api", "API"}

	ctx := newTestRequestCtx(testComplexityAnalyzerPayload(t, cfg))
	handler.updateComplexityAnalyzerConfig(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	if manager.reloadCalls != 1 {
		t.Fatalf("expected one reload, got %d", manager.reloadCalls)
	}
	if manager.reloadedConfig == nil || manager.reloadedConfig.TierBoundaries.ComplexReasoning != 0.78 {
		t.Fatalf("expected reload with normalized config, got %+v", manager.reloadedConfig)
	}

	stored, err := store.GetComplexityAnalyzerConfig(context.Background())
	if err != nil {
		t.Fatalf("get stored config: %v", err)
	}
	if stored == nil || len(stored.Keywords.CodeKeywords) != 2 || stored.Keywords.CodeKeywords[0] != "api" {
		t.Fatalf("expected normalized stored keywords, got %+v", stored)
	}
}

func TestComplexityAnalyzerConfigPutRejectsInvalidPayloads(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	handler := &RoutingHandler{
		configStore:    store,
		routingManager: &mockRoutingManager{},
	}

	valid := complexity.DefaultAnalyzerConfig()
	validBody := testComplexityAnalyzerPayload(t, valid)
	invalidBoundaries := valid
	invalidBoundaries.TierBoundaries.MediumComplex = invalidBoundaries.TierBoundaries.SimpleMedium
	emptyKeywords := valid
	emptyKeywords.Keywords.CodeKeywords = nil

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown field", body: strings.TrimSuffix(validBody, "}") + `,"extra":true}`, want: "Invalid request payload"},
		{name: "multiple json values", body: validBody + `{}`, want: "multiple JSON values"},
		{name: "invalid boundaries", body: testComplexityAnalyzerPayload(t, invalidBoundaries), want: "tier boundaries"},
		{name: "empty keywords", body: testComplexityAnalyzerPayload(t, emptyKeywords), want: "keyword lists must be non-empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newTestRequestCtx(tt.body)
			handler.updateComplexityAnalyzerConfig(ctx)
			if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
			}
			if !strings.Contains(string(ctx.Response.Body()), tt.want) {
				t.Fatalf("expected response to contain %q, got %s", tt.want, string(ctx.Response.Body()))
			}
		})
	}
}

func TestComplexityAnalyzerConfigResetPersistsDefaultsAndReloads(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	manager := &mockRoutingManager{}
	handler := &RoutingHandler{
		configStore:    store,
		routingManager: manager,
	}

	custom := complexity.DefaultAnalyzerConfig()
	custom.TierBoundaries.ComplexReasoning = 0.80
	if err := store.UpdateComplexityAnalyzerConfig(context.Background(), &custom); err != nil {
		t.Fatalf("seed custom config: %v", err)
	}

	ctx := newTestRequestCtx("")
	handler.resetComplexityAnalyzerConfig(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", ctx.Response.StatusCode(), string(ctx.Response.Body()))
	}
	if manager.reloadCalls != 1 {
		t.Fatalf("expected one reload, got %d", manager.reloadCalls)
	}
	stored, err := store.GetComplexityAnalyzerConfig(context.Background())
	if err != nil {
		t.Fatalf("get stored config: %v", err)
	}
	if stored == nil || stored.TierBoundaries != complexity.DefaultTierBoundaries() {
		t.Fatalf("expected stored defaults, got %+v", stored)
	}
}

// TestRoutingRoutesServeCanonicalAndLegacyPaths pins the backwards-compatibility contract:
// every routing endpoint answers on both its /api/routing path and the /api/governance path
// it shipped under before routing became its own plugin, and each pair resolves to the same
// handler so the two can never drift.
func TestRoutingRoutesServeCanonicalAndLegacyPaths(t *testing.T) {
	r := router.New()
	h := &RoutingHandler{}
	h.RegisterRoutes(r)

	pairs := []struct {
		method    string
		canonical string
		legacy    string
	}{
		{fasthttp.MethodGet, "/api/routing/rules", "/api/governance/routing-rules"},
		{fasthttp.MethodPost, "/api/routing/rules", "/api/governance/routing-rules"},
		{fasthttp.MethodGet, "/api/routing/rules/{rule_id}", "/api/governance/routing-rules/{rule_id}"},
		{fasthttp.MethodPut, "/api/routing/rules/{rule_id}", "/api/governance/routing-rules/{rule_id}"},
		{fasthttp.MethodDelete, "/api/routing/rules/{rule_id}", "/api/governance/routing-rules/{rule_id}"},
		{fasthttp.MethodGet, "/api/routing/complexity-analyzer-config", "/api/governance/complexity-analyzer-config"},
		{fasthttp.MethodPut, "/api/routing/complexity-analyzer-config", "/api/governance/complexity-analyzer-config"},
		{fasthttp.MethodPost, "/api/routing/complexity-analyzer-config/reset", "/api/governance/complexity-analyzer-config/reset"},
	}

	for _, pair := range pairs {
		for _, path := range []string{pair.canonical, pair.legacy} {
			if got := countRegisteredRoute(r, pair.method, path); got != 1 {
				t.Fatalf("%s %s registrations = %d, want 1", pair.method, path, got)
			}
		}
	}
}

func TestCreateRoutingRulePersistsAndReturnsErrorFallbacks(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	manager := &mockRoutingManager{}
	handler := &RoutingHandler{configStore: store, routingManager: manager}

	reqCtx := newTestRequestCtx(`{
		"name":"image-safe-fallback",
		"cel_expression":"true",
		"targets":[{"provider":"openai","model":"gpt-image-1","weight":1}],
		"error_fallbacks":[{
			"name":" content policy ",
			"scenario":"CONTENT_POLICY",
			"supplement":{"providers":[" Custom-Provider "],"message_contains_any":[" unsafe "]},
			"fallbacks":[" Custom-Safety/image-model "]
		}]
	}`)
	handler.createRoutingRule(reqCtx)

	require.Equal(t, fasthttp.StatusOK, reqCtx.Response.StatusCode(), string(reqCtx.Response.Body()))
	require.Len(t, manager.reloadIDs, 1)
	rules, err := store.GetRoutingRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].ParsedErrorFallbacks, 1)
	stored := rules[0].ParsedErrorFallbacks[0]
	assert.Equal(t, "content policy", stored.Name)
	assert.Equal(t, "content_policy", stored.Scenario)
	require.NotNil(t, stored.Supplement)
	assert.Equal(t, []string{"custom-provider"}, stored.Supplement.Providers)
	assert.Equal(t, []string{"unsafe"}, stored.Supplement.MessageContainsAny)
	assert.Equal(t, []string{"custom-safety/image-model"}, stored.Fallbacks)
	assert.Contains(t, string(reqCtx.Response.Body()), `"error_fallbacks"`)
}

func TestCreateRoutingRuleAcceptsContentSafetyRecognitionWithoutFallbackTargets(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	manager := &mockRoutingManager{}
	handler := &RoutingHandler{configStore: store, routingManager: manager}

	reqCtx := newTestRequestCtx(`{
		"name":"recognize-content-policy",
		"cel_expression":"true",
		"targets":[{"provider":"openai","model":"gpt-image-1","weight":1}],
		"error_fallbacks":[{
			"name":"custom recognition",
			"scenario":"content_policy",
			"supplement":{"message_contains_any":["vendor moderation gate"]},
			"when":{},
			"fallbacks":[]
		}]
	}`)
	handler.createRoutingRule(reqCtx)

	require.Equal(t, fasthttp.StatusOK, reqCtx.Response.StatusCode(), string(reqCtx.Response.Body()))
	rules, err := store.GetRoutingRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].ParsedErrorFallbacks, 1)
	stored := rules[0].ParsedErrorFallbacks[0]
	require.NotNil(t, stored.Supplement)
	assert.Equal(t, []string{"vendor moderation gate"}, stored.Supplement.MessageContainsAny)
	assert.Empty(t, stored.Fallbacks)
}

func TestCreateRoutingRuleAcceptsLegacyContentSafetyRecognitionWithoutFallbackTargets(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	handler := &RoutingHandler{configStore: store, routingManager: &mockRoutingManager{}}

	reqCtx := newTestRequestCtx(`{
		"name":"legacy-recognition",
		"cel_expression":"true",
		"targets":[{"provider":"openai","model":"gpt-image-1","weight":1}],
		"error_fallbacks":[{
			"when":{"categories":["content_policy"],"message_contains":["legacy moderation gate"]},
			"fallbacks":[]
		}]
	}`)
	handler.createRoutingRule(reqCtx)

	require.Equal(t, fasthttp.StatusOK, reqCtx.Response.StatusCode(), string(reqCtx.Response.Body()))
	rules, err := store.GetRoutingRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].ParsedErrorFallbacks, 1)
	assert.Equal(t, []string{"legacy moderation gate"}, rules[0].ParsedErrorFallbacks[0].When.MessageContains)
	assert.Empty(t, rules[0].ParsedErrorFallbacks[0].Fallbacks)
}

func TestUpdateRoutingRuleCanExplicitlyClearErrorFallbacks(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	manager := &mockRoutingManager{}
	handler := &RoutingHandler{configStore: store, routingManager: manager}
	enabled := true
	rule := &tables.TableRoutingRule{
		ID: "routing-clear-error-fallbacks", Name: "clear policy", Enabled: &enabled,
		CelExpression: "true", Scope: "global",
		Targets: []tables.TableRoutingTarget{{Provider: schemas.Ptr("openai"), Model: schemas.Ptr("gpt-4o"), Weight: 1}},
		ParsedErrorFallbacks: []tables.TableRoutingErrorFallback{{
			Scenario: "timeout", Fallbacks: []string{"anthropic/claude-sonnet-4"},
		}},
	}
	require.NoError(t, store.CreateRoutingRule(context.Background(), rule))

	reqCtx := newTestRequestCtx(`{"error_fallbacks":[]}`)
	reqCtx.SetUserValue("rule_id", rule.ID)
	handler.updateRoutingRule(reqCtx)
	require.Equal(t, fasthttp.StatusOK, reqCtx.Response.StatusCode(), string(reqCtx.Response.Body()))

	stored, err := store.GetRoutingRule(context.Background(), rule.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.ParsedErrorFallbacks)
	assert.Nil(t, stored.ErrorFallbacks)
	require.Len(t, manager.reloadIDs, 1)
}

func TestRoutingRuleGetAndListExposeLegacyRawOnlyErrorFallbacks(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	handler := &RoutingHandler{configStore: store, routingManager: &mockRoutingManager{}}
	enabled := true
	raw := `[{"when":{"status_codes":[429]},"fallbacks":["anthropic/claude-sonnet-4"]}]`
	rule := &tables.TableRoutingRule{
		ID: "routing-raw-error-fallbacks", Name: "legacy raw policy", Enabled: &enabled,
		CelExpression: "true", Scope: "global", ErrorFallbacks: &raw,
		Targets: []tables.TableRoutingTarget{{Provider: schemas.Ptr("openai"), Model: schemas.Ptr("gpt-4o"), Weight: 1}},
	}
	require.NoError(t, store.CreateRoutingRule(context.Background(), rule))

	getCtx := newTestRequestCtx("")
	getCtx.SetUserValue("rule_id", rule.ID)
	handler.getRoutingRule(getCtx)
	require.Equal(t, fasthttp.StatusOK, getCtx.Response.StatusCode(), string(getCtx.Response.Body()))
	assert.Contains(t, string(getCtx.Response.Body()), `"error_fallbacks":[{"when":{"status_codes":[429]}`)

	listCtx := newTestRequestCtx("")
	handler.getRoutingRules(listCtx)
	require.Equal(t, fasthttp.StatusOK, listCtx.Response.StatusCode(), string(listCtx.Response.Body()))
	assert.Contains(t, string(listCtx.Response.Body()), `"error_fallbacks":[{"when":{"status_codes":[429]}`)
}

func TestRoutingRuleRejectsInvalidErrorFallbackContracts(t *testing.T) {
	SetLogger(&mockLogger{})
	tests := []struct {
		name       string
		policyJSON string
		want       string
	}{
		{"unknown scenario", `{"scenario":"made_up","fallbacks":["openai/gpt-4o"]}`, "scenario"},
		{"scenario and when", `{"scenario":"timeout","when":{"status_codes":[504]},"fallbacks":["openai/gpt-4o"]}`, "both scenario and when"},
		{"empty when", `{"when":{},"fallbacks":["openai/gpt-4o"]}`, "either scenario or when"},
		{"bad status", `{"when":{"status_codes":[99]},"fallbacks":["openai/gpt-4o"]}`, "valid HTTP status"},
		{"empty matcher", `{"when":{"error_codes":[" "]},"fallbacks":["openai/gpt-4o"]}`, "must not be empty"},
		{"empty supplement", `{"scenario":"timeout","supplement":{"providers":["openai"]},"fallbacks":["openai/gpt-4o"]}`, "non-provider matcher"},
		{"empty timeout fallback chain", `{"scenario":"timeout","fallbacks":[]}`, "at least one fallback target"},
		{"empty content fallback without recognition", `{"scenario":"content_policy","fallbacks":[]}`, "at least one fallback target"},
		{"empty legacy content fallback without recognition", `{"when":{"categories":["content_policy"]},"fallbacks":[]}`, "at least one fallback target"},
		{"invalid fallback", `{"scenario":"timeout","fallbacks":["not-a-provider"]}`, "provider/model"},
		{"duplicate fallback", `{"scenario":"timeout","fallbacks":["openai/GPT-4O","openai/gpt-4o"]}`, "duplicates an earlier"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := setupPricingOverrideHandlerStore(t)
			handler := &RoutingHandler{configStore: store, routingManager: &mockRoutingManager{}}
			body := `{"name":"invalid-policy","cel_expression":"true","targets":[{"provider":"openai","model":"gpt-4o","weight":1}],"error_fallbacks":[` + tt.policyJSON + `]}`
			reqCtx := newTestRequestCtx(body)
			handler.createRoutingRule(reqCtx)
			require.Equal(t, fasthttp.StatusBadRequest, reqCtx.Response.StatusCode(), string(reqCtx.Response.Body()))
			assert.Contains(t, string(reqCtx.Response.Body()), tt.want)
		})
	}
}

func TestContentSafetySignalsReturnsRuntimeCatalog(t *testing.T) {
	handler := &RoutingHandler{}
	ctx := newTestRequestCtx("")
	handler.getContentSafetySignals(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	var catalog struct {
		Structured    []string `json:"structured"`
		FinishReasons []string `json:"finish_reasons"`
		Messages      []string `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &catalog))
	assert.Contains(t, catalog.Structured, "content_filter")
	assert.Contains(t, catalog.FinishReasons, "contentfilter")
	assert.Contains(t, catalog.FinishReasons, "image_safety")
	assert.Contains(t, catalog.Messages, "裸露、色情或情色内容")
}
