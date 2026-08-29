package rules

import (
	"context"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRoutingRulesPropagatesErrorFallbacks(t *testing.T) {
	store, engine, err := newTestEngine(DefaultChainMaxDepth)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRule(context.Background(), errorFallbackRule(
		"policy", "model == 'gpt-4o'", false, "azure/gpt-image-1",
	)))

	ctx := schemas.NewBifrostContext(context.Background(), time.Now())
	decision, err := engine.EvaluateRoutingRules(ctx, &EvaluationContext{
		Provider: schemas.OpenAI, Model: "gpt-4o", Headers: map[string]string{}, QueryParams: map[string]string{},
	})
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.Len(t, decision.ErrorFallbacks, 1)
	assert.Equal(t, "content policy", decision.ErrorFallbacks[0].Name)
	assert.Equal(t, []string{"azure/gpt-image-1"}, decision.ErrorFallbacks[0].Fallbacks)

	logs := ctx.GetRoutingEngineLogs()
	require.NotEmpty(t, logs)
	assert.Contains(t, logs[len(logs)-1].Message, "error_fallbacks=1")
}

func TestEvaluateRoutingRulesRecoversRawOnlyErrorFallbacks(t *testing.T) {
	store, engine, err := newTestEngine(DefaultChainMaxDepth)
	require.NoError(t, err)
	raw := `[{"name":"rate limit","scenario":"rate_limit","when":{"status_codes":[429]},"fallbacks":["anthropic/claude-sonnet-4"]}]`
	rule := baseErrorFallbackRule("raw-only", "model == 'gpt-4o'", false)
	rule.ErrorFallbacks = &raw
	require.Nil(t, rule.ParsedErrorFallbacks)
	require.NoError(t, store.UpsertRule(context.Background(), rule))

	decision, err := engine.EvaluateRoutingRules(
		schemas.NewBifrostContext(context.Background(), time.Now()),
		&EvaluationContext{Provider: schemas.OpenAI, Model: "gpt-4o"},
	)
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.Len(t, decision.ErrorFallbacks, 1)
	assert.Equal(t, "rate_limit", decision.ErrorFallbacks[0].Scenario)
	assert.Equal(t, []int{429}, decision.ErrorFallbacks[0].When.StatusCodes)
}

func TestEvaluateRoutingRulesChainUsesFinalRuleErrorFallbacks(t *testing.T) {
	store, engine, err := newTestEngine(DefaultChainMaxDepth)
	require.NoError(t, err)
	first := errorFallbackRule("first", "model == 'incoming'", true, "azure/first")
	first.Targets[0].Model = bifrost.Ptr("routed")
	first.Priority = 0
	second := errorFallbackRule("second", "model == 'routed'", false, "anthropic/final")
	second.Priority = 1
	require.NoError(t, store.UpsertRule(context.Background(), first))
	require.NoError(t, store.UpsertRule(context.Background(), second))

	decision, err := engine.EvaluateRoutingRules(
		schemas.NewBifrostContext(context.Background(), time.Now()),
		&EvaluationContext{Provider: schemas.OpenAI, Model: "incoming"},
	)
	require.NoError(t, err)
	require.NotNil(t, decision)
	assert.Equal(t, "second", decision.MatchedRuleID)
	require.Len(t, decision.ErrorFallbacks, 1)
	assert.Equal(t, []string{"anthropic/final"}, decision.ErrorFallbacks[0].Fallbacks)
}

func TestEffectiveRoutingErrorFallbacksRejectsMalformedRawJSON(t *testing.T) {
	raw := `[{`
	_, err := effectiveRoutingErrorFallbacks(&configstoreTables.TableRoutingRule{ErrorFallbacks: &raw})
	require.Error(t, err)
}

func baseErrorFallbackRule(id, expression string, chain bool) *configstoreTables.TableRoutingRule {
	return &configstoreTables.TableRoutingRule{
		ID: id, Name: id, CelExpression: expression, ChainRule: chain,
		Targets:  []configstoreTables.TableRoutingTarget{{Provider: bifrost.Ptr("openai"), Model: bifrost.Ptr("gpt-4o"), Weight: 1}},
		Enabled:  bifrost.Ptr(true),
		Scope:    "global",
		Priority: 0,
	}
}

func errorFallbackRule(id, expression string, chain bool, fallback string) *configstoreTables.TableRoutingRule {
	rule := baseErrorFallbackRule(id, expression, chain)
	rule.ParsedErrorFallbacks = []configstoreTables.TableRoutingErrorFallback{{
		Name:     "content policy",
		Scenario: "content_policy",
		Supplement: &configstoreTables.TableRoutingErrorFallbackSupplement{
			Providers: []string{"openai"}, ErrorCodes: []string{"safety_violations"},
		},
		When: configstoreTables.TableRoutingErrorFallbackCondition{
			Categories: []string{"content_policy"}, StatusCodes: []int{400},
		},
		Fallbacks: []string{fallback},
	}}
	return rule
}
