package routing

import (
	"context"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/routing/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRoutingErrorFallbacksPreservesScenarioSupplementAndCustomProvider(t *testing.T) {
	resolved := resolveRoutingErrorFallbacks([]configstoreTables.TableRoutingErrorFallback{{
		Name:     "content policy",
		Scenario: "content_policy",
		Supplement: &configstoreTables.TableRoutingErrorFallbackSupplement{
			Providers: []string{"openai"}, ErrorCodes: []string{"safety_violations"}, StatusCodes: []int{400},
		},
		When: configstoreTables.TableRoutingErrorFallbackCondition{
			Categories: []string{"content_policy"}, ErrorTypes: []string{"policy_error"}, MessageContains: []string{"blocked"},
		},
		Fallbacks: []string{"unregistered-safety-target/image-model"},
	}}, "routed-model")

	require.Len(t, resolved, 1)
	assert.Equal(t, schemas.FailureCategory("content_policy"), resolved[0].Scenario)
	require.NotNil(t, resolved[0].Supplement)
	assert.Equal(t, []schemas.ModelProvider{schemas.OpenAI}, resolved[0].Supplement.Providers)
	assert.Equal(t, []string{"safety_violations"}, resolved[0].Supplement.ErrorCodes)
	assert.Equal(t, []schemas.FailureCategory{schemas.FailureCategory("content_policy")}, resolved[0].When.Categories)
	require.Len(t, resolved[0].Fallbacks, 1)
	assert.Equal(t, schemas.ModelProvider("unregistered-safety-target"), resolved[0].Fallbacks[0].Provider)
	assert.Equal(t, "image-model", resolved[0].Fallbacks[0].Model)
}

func TestPreRequestHookInjectsErrorFallbacksUsingRoutedModelWithoutChangingOrdinaryFallbackSemantics(t *testing.T) {
	store, err := rules.NewLocalStore(context.Background(), rules.NewMockLogger(), nil)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRule(context.Background(), &configstoreTables.TableRoutingRule{
		ID: "inject", Name: "inject", CelExpression: "model == 'incoming'",
		Targets:         []configstoreTables.TableRoutingTarget{{Provider: bifrost.Ptr("openai"), Model: bifrost.Ptr("routed-model"), Weight: 1}},
		ParsedFallbacks: []string{"anthropic/"},
		ParsedErrorFallbacks: []configstoreTables.TableRoutingErrorFallback{{
			Scenario: "content_policy", Fallbacks: []string{"azure/"},
		}},
		Enabled: bifrost.Ptr(true), Scope: "global",
	}))
	plugin, err := InitFromStore(context.Background(), nil, rules.NewMockLogger(), nil, store, NewMockGovernance())
	require.NoError(t, err)
	req := &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "incoming"},
	}

	require.NoError(t, plugin.PreRequestHook(schemas.NewBifrostContext(context.Background(), time.Now()), req))
	assert.Equal(t, "routed-model", req.ChatRequest.Model)
	require.Len(t, req.ChatRequest.Fallbacks, 1)
	assert.Equal(t, "incoming", req.ChatRequest.Fallbacks[0].Model, "ordinary fallback inheritance must remain unchanged")
	require.Len(t, req.ChatRequest.ErrorFallbacks, 1)
	require.Len(t, req.ChatRequest.ErrorFallbacks[0].Fallbacks, 1)
	assert.Equal(t, schemas.Azure, req.ChatRequest.ErrorFallbacks[0].Fallbacks[0].Provider)
	assert.Equal(t, "routed-model", req.ChatRequest.ErrorFallbacks[0].Fallbacks[0].Model)
}

func TestPreRequestHookRawOnlyPolicySurvivesRuleUpdateAndRemoval(t *testing.T) {
	store, err := rules.NewLocalStore(context.Background(), rules.NewMockLogger(), nil)
	require.NoError(t, err)
	plugin, err := InitFromStore(context.Background(), nil, rules.NewMockLogger(), nil, store, NewMockGovernance())
	require.NoError(t, err)

	raw := `[{"scenario":"rate_limit","fallbacks":["anthropic/claude-sonnet-4"]}]`
	require.NoError(t, store.UpsertRule(context.Background(), cachedErrorFallbackRule("reload", &raw, nil)))
	first := chatRequest("incoming")
	require.NoError(t, plugin.PreRequestHook(schemas.NewBifrostContext(context.Background(), time.Now()), first))
	require.Len(t, first.ChatRequest.ErrorFallbacks, 1)
	assert.Equal(t, schemas.Anthropic, first.ChatRequest.ErrorFallbacks[0].Fallbacks[0].Provider)

	updated := []configstoreTables.TableRoutingErrorFallback{{Scenario: "content_policy", Fallbacks: []string{"azure/gpt-image-1"}}}
	require.NoError(t, store.UpsertRule(context.Background(), cachedErrorFallbackRule("reload", nil, updated)))
	second := chatRequest("incoming")
	require.NoError(t, plugin.PreRequestHook(schemas.NewBifrostContext(context.Background(), time.Now()), second))
	require.Len(t, second.ChatRequest.ErrorFallbacks, 1)
	assert.Equal(t, schemas.FailureCategory("content_policy"), second.ChatRequest.ErrorFallbacks[0].Scenario)
	assert.Equal(t, schemas.Azure, second.ChatRequest.ErrorFallbacks[0].Fallbacks[0].Provider)

	require.NoError(t, store.DeleteRule(context.Background(), "reload"))
	third := chatRequest("incoming")
	require.NoError(t, plugin.PreRequestHook(schemas.NewBifrostContext(context.Background(), time.Now()), third))
	assert.Nil(t, third.ChatRequest.ErrorFallbacks)
}

func cachedErrorFallbackRule(id string, raw *string, parsed []configstoreTables.TableRoutingErrorFallback) *configstoreTables.TableRoutingRule {
	return &configstoreTables.TableRoutingRule{
		ID: id, Name: id, CelExpression: "model == 'incoming'",
		Targets:              []configstoreTables.TableRoutingTarget{{Provider: bifrost.Ptr("openai"), Model: bifrost.Ptr("routed"), Weight: 1}},
		ErrorFallbacks:       raw,
		ParsedErrorFallbacks: parsed,
		Enabled:              bifrost.Ptr(true),
		Scope:                "global",
	}
}

func chatRequest(model string) *schemas.BifrostRequest {
	return &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: model},
	}
}
