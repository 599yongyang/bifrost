package governance

import (
	"context"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRoutingErrorFallbacks(t *testing.T) {
	resolved := resolveRoutingErrorFallbacks([]configstoreTables.TableRoutingErrorFallback{{
		Name: "content policy",
		When: configstoreTables.TableRoutingErrorFallbackCondition{
			Categories:      []string{"content_policy"},
			MessageContains: []string{"unsafe"},
		},
		Fallbacks: []string{"azure/", "openai/gpt-image-1"},
	}}, "gpt-image-1")

	require.Len(t, resolved, 1)
	assert.Equal(t, "content policy", resolved[0].Name)
	assert.Equal(t, []schemas.FailureCategory{schemas.FailureCategoryContentPolicy}, resolved[0].When.Categories)
	assert.Equal(t, []string{"unsafe"}, resolved[0].When.MessageContains)
	assert.Equal(t, []schemas.Fallback{
		{Provider: schemas.Azure, Model: "gpt-image-1"},
		{Provider: schemas.OpenAI, Model: "gpt-image-1"},
	}, resolved[0].Fallbacks)
}

func TestResolveRoutingErrorFallbacks_PreservesScenarioAndSupplement(t *testing.T) {
	resolved := resolveRoutingErrorFallbacks([]configstoreTables.TableRoutingErrorFallback{{
		Name:     "content policy",
		Scenario: "content_policy",
		Supplement: &configstoreTables.TableRoutingErrorFallbackSupplement{
			Providers:          []string{"openai"},
			MessageContainsAny: []string{"unsafe"},
		},
		Fallbacks: []string{"azure/", "openai/gpt-image-1"},
	}}, "gpt-image-1")

	require.Len(t, resolved, 1)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, resolved[0].Scenario)
	require.NotNil(t, resolved[0].Supplement)
	assert.Equal(t, []schemas.ModelProvider{schemas.OpenAI}, resolved[0].Supplement.Providers)
	assert.Equal(t, []string{"unsafe"}, resolved[0].Supplement.MessageContainsAny)
	assert.Equal(t, []schemas.Fallback{
		{Provider: schemas.Azure, Model: "gpt-image-1"},
		{Provider: schemas.OpenAI, Model: "gpt-image-1"},
	}, resolved[0].Fallbacks)
}

func TestEvaluateRoutingRulesPropagatesErrorFallbacks(t *testing.T) {
	store, err := NewLocalGovernanceStore(context.Background(), NewMockLogger(), nil, &configstore.GovernanceConfig{}, nil)
	require.NoError(t, err)

	engine, err := NewRoutingEngine(store, NewMockLogger(), schemas.Ptr(10))
	require.NoError(t, err)

	rule := &configstoreTables.TableRoutingRule{
		ID:            "error-fallback-propagation",
		Name:          "Error fallback propagation",
		CelExpression: "model == 'gpt-4o'",
		Targets: []configstoreTables.TableRoutingTarget{
			{Provider: bifrost.Ptr("openai"), Model: bifrost.Ptr("gpt-4o"), Weight: 1.0},
		},
		ParsedErrorFallbacks: []configstoreTables.TableRoutingErrorFallback{{
			Name: "content policy",
			When: configstoreTables.TableRoutingErrorFallbackCondition{
				Categories: []string{"content_policy"},
			},
			Fallbacks: []string{"azure/gpt-image-1"},
		}},
		Enabled:  bifrost.Ptr(true),
		Scope:    "global",
		Priority: 0,
	}
	require.NoError(t, store.UpdateRoutingRuleInMemory(context.Background(), rule))

	decision, err := engine.EvaluateRoutingRules(
		schemas.NewBifrostContext(context.Background(), time.Now()),
		&RoutingContext{
			Provider:    schemas.OpenAI,
			Model:       "gpt-4o",
			Headers:     map[string]string{},
			QueryParams: map[string]string{},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.Len(t, decision.ErrorFallbacks, 1)
	assert.Equal(t, "content policy", decision.ErrorFallbacks[0].Name)
	assert.Equal(t, []string{"azure/gpt-image-1"}, decision.ErrorFallbacks[0].Fallbacks)
}

func TestApplyRoutingRulesErrorFallbackInheritsRoutedModel(t *testing.T) {
	store, err := NewLocalGovernanceStore(context.Background(), NewMockLogger(), nil, &configstore.GovernanceConfig{}, nil)
	require.NoError(t, err)
	engine, err := NewRoutingEngine(store, NewMockLogger(), schemas.Ptr(10))
	require.NoError(t, err)

	rule := &configstoreTables.TableRoutingRule{
		ID:            "routed-model-inheritance",
		Name:          "Routed model inheritance",
		CelExpression: "model == 'incoming-model'",
		Targets: []configstoreTables.TableRoutingTarget{{
			Provider: bifrost.Ptr("openai"),
			Model:    bifrost.Ptr("routed-model"),
			Weight:   1,
		}},
		ParsedErrorFallbacks: []configstoreTables.TableRoutingErrorFallback{{
			When:      configstoreTables.TableRoutingErrorFallbackCondition{Categories: []string{"content_policy"}},
			Fallbacks: []string{"azure/"},
		}},
		Enabled: bifrost.Ptr(true),
		Scope:   "global",
	}
	require.NoError(t, store.UpdateRoutingRuleInMemory(context.Background(), rule))
	plugin := &GovernancePlugin{logger: NewMockLogger(), store: store, engine: engine}
	req := &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "incoming-model"},
	}

	_, err = plugin.applyRoutingRules(schemas.NewBifrostContext(context.Background(), time.Now()), req, nil)
	require.NoError(t, err)
	assert.Equal(t, "routed-model", req.ChatRequest.Model)
	require.Len(t, req.ChatRequest.ErrorFallbacks, 1)
	require.Len(t, req.ChatRequest.ErrorFallbacks[0].Fallbacks, 1)
	assert.Equal(t, schemas.Azure, req.ChatRequest.ErrorFallbacks[0].Fallbacks[0].Provider)
	assert.Equal(t, "routed-model", req.ChatRequest.ErrorFallbacks[0].Fallbacks[0].Model)
}
