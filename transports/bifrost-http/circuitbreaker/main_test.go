package circuitbreaker

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func stringPtr(value string) *string { return &value }

func testConfig(policy Policy) *Config {
	return &Config{Policies: []Policy{policy}}
}

func basePolicy() Policy {
	return Policy{
		Name:             "azure-ptu-spillover",
		PrimaryProvider:  schemas.Azure,
		PrimaryModel:     "gpt-4o-ptu",
		FallbackProvider: schemas.Azure,
		FallbackModel:    "gpt-4o-paygo",
		Condition: Condition{Signals: []Signal{{
			Source:      "response_header",
			HeaderName:  "X-Ms-Is-Spilled-Over",
			HeaderValue: stringPtr("true"),
		}}},
		DefaultCooldown: "30s",
	}
}

func requestContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
}

func chatRequest() *schemas.BifrostRequest {
	return &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.Azure, Model: "gpt-4o-ptu"},
	}
}

func chatResponse(headers map[string]string) *schemas.BifrostResponse {
	return &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{
		ExtraFields: schemas.BifrostResponseExtraFields{
			RoutingInfo:             schemas.RoutingInfo{Provider: schemas.Azure, Model: "gpt-4o-ptu"},
			ProviderResponseHeaders: headers,
		},
	}}
}

func TestSharedCircuitTripsReroutesAndUsesSingleHalfOpenProbe(t *testing.T) {
	policy := basePolicy()
	policy.CooldownHeader = "retry-after-ms"
	plugin, err := Init(testConfig(policy), nil)
	require.NoError(t, err)

	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }

	ctx := requestContext()
	req := chatRequest()
	require.NoError(t, plugin.PreRequestHook(ctx, req))
	require.Equal(t, schemas.Azure, req.ChatRequest.Provider)
	require.Empty(t, req.ChatRequest.Fallbacks)
	_, _, err = plugin.PreLLMHook(ctx, req)
	require.NoError(t, err)

	_, _, err = plugin.PostLLMHook(ctx, chatResponse(map[string]string{
		"x-ms-is-spilled-over": "TRUE",
		"Retry-After-Ms":       "1500",
	}), nil)
	require.NoError(t, err)

	openReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), openReq))
	require.Equal(t, "gpt-4o-paygo", openReq.ChatRequest.Model)

	now = now.Add(1501 * time.Millisecond)
	probeCtx := requestContext()
	probeReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(probeCtx, probeReq))
	_, _, err = plugin.PreLLMHook(probeCtx, probeReq)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o-ptu", probeReq.ChatRequest.Model)

	concurrentReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), concurrentReq))
	require.Equal(t, "gpt-4o-paygo", concurrentReq.ChatRequest.Model)

	_, _, err = plugin.PostLLMHook(probeCtx, chatResponse(map[string]string{}), nil)
	require.NoError(t, err)
	closedReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), closedReq))
	require.Equal(t, "gpt-4o-ptu", closedReq.ChatRequest.Model)
}

func TestConditionSupportsANDContainsAndExists(t *testing.T) {
	policy := basePolicy()
	policy.Condition = Condition{
		Operator: "AND",
		Signals: []Signal{
			{Source: "response_header", HeaderName: "x-capacity", HeaderContains: stringPtr("spill")},
			{Source: "response_header", HeaderName: "x-degraded"},
		},
	}
	plugin, err := Init(testConfig(policy), nil)
	require.NoError(t, err)

	ctx := requestContext()
	require.NoError(t, plugin.PreRequestHook(ctx, chatRequest()))
	_, _, err = plugin.PostLLMHook(ctx, chatResponse(map[string]string{"X-Capacity": "PTU-SPILLOVER"}), nil)
	require.NoError(t, err)
	stillClosed := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), stillClosed))
	require.Equal(t, "gpt-4o-ptu", stillClosed.ChatRequest.Model)

	ctx = requestContext()
	require.NoError(t, plugin.PreRequestHook(ctx, chatRequest()))
	_, _, err = plugin.PostLLMHook(ctx, chatResponse(map[string]string{
		"X-Capacity": "ptu-SPILLover",
		"X-Degraded": "",
	}), nil)
	require.NoError(t, err)
	openReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), openReq))
	require.Equal(t, "gpt-4o-paygo", openReq.ChatRequest.Model)
}

func TestKeySubCircuitsSuppressOnlyDegradedKeysThenOpenMainCircuit(t *testing.T) {
	policy := basePolicy()
	policy.PrimaryKeyIDs = []string{"key-1", "key-2"}
	plugin, err := Init(testConfig(policy), nil)
	require.NoError(t, err)
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }
	keys := []schemas.Key{{ID: "key-1"}, {ID: "key-2"}, {ID: "untracked"}}

	tripKey := func(keyID string) {
		ctx := requestContext()
		require.NoError(t, plugin.PreRequestHook(ctx, chatRequest()))
		ctx.SetValue(schemas.BifrostContextKeySelectedKeyID, keyID)
		_, _, hookErr := plugin.PostLLMHook(ctx, chatResponse(map[string]string{"X-Ms-Is-Spilled-Over": "true"}), nil)
		require.NoError(t, hookErr)
	}

	tripKey("key-1")
	filterCtx := requestContext()
	require.NoError(t, plugin.PreRequestHook(filterCtx, chatRequest()))
	filtered, err := plugin.FilterKeys(filterCtx, schemas.Azure, "gpt-4o-ptu", keys)
	require.NoError(t, err)
	require.Equal(t, []string{"key-2", "untracked"}, keyIDs(filtered))

	tripKey("key-2")
	openReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), openReq))
	require.Equal(t, "gpt-4o-paygo", openReq.ChatRequest.Model)

	now = now.Add(31 * time.Second)
	probeCtx := requestContext()
	probeReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(probeCtx, probeReq))
	_, _, err = plugin.PreLLMHook(probeCtx, probeReq)
	require.NoError(t, err)
	probeKeys, err := plugin.FilterKeys(probeCtx, schemas.Azure, "gpt-4o-ptu", keys)
	require.NoError(t, err)
	require.Len(t, probeKeys, 1)
	require.Contains(t, []string{"key-1", "key-2"}, probeKeys[0].ID)

	concurrentReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), concurrentReq))
	require.Equal(t, "gpt-4o-paygo", concurrentReq.ChatRequest.Model)
}

func TestHalfOpenCandidateShortCircuitedBeforePreLLMDoesNotClaimProbe(t *testing.T) {
	plugin, err := Init(testConfig(basePolicy()), nil)
	require.NoError(t, err)
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }

	tripCtx := requestContext()
	require.NoError(t, plugin.PreRequestHook(tripCtx, chatRequest()))
	_, _, err = plugin.PostLLMHook(tripCtx, chatResponse(map[string]string{"X-Ms-Is-Spilled-Over": "true"}), nil)
	require.NoError(t, err)
	now = now.Add(31 * time.Second)

	// This request represents an earlier plugin cache hit: PreRequest ran, but
	// circuit breaker's PreLLM/PostLLM pair never executes.
	cacheHitCtx := requestContext()
	cacheHitReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(cacheHitCtx, cacheHitReq))
	require.Equal(t, "gpt-4o-ptu", cacheHitReq.ChatRequest.Model)

	// A later real provider-bound request must still be able to claim the probe.
	probeCtx := requestContext()
	probeReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(probeCtx, probeReq))
	_, _, err = plugin.PreLLMHook(probeCtx, probeReq)
	require.NoError(t, err)
	concurrentReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), concurrentReq))
	require.Equal(t, "gpt-4o-paygo", concurrentReq.ChatRequest.Model)
}

func TestKeyHalfOpenRetryRemainsPinnedToProbeKey(t *testing.T) {
	policy := basePolicy()
	policy.PrimaryKeyIDs = []string{"key-1"}
	plugin, err := Init(testConfig(policy), nil)
	require.NoError(t, err)
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }

	tripCtx := requestContext()
	require.NoError(t, plugin.PreRequestHook(tripCtx, chatRequest()))
	tripCtx.SetValue(schemas.BifrostContextKeySelectedKeyID, "key-1")
	_, _, err = plugin.PostLLMHook(tripCtx, chatResponse(map[string]string{"X-Ms-Is-Spilled-Over": "true"}), nil)
	require.NoError(t, err)
	now = now.Add(31 * time.Second)

	probeCtx := requestContext()
	require.NoError(t, plugin.PreRequestHook(probeCtx, chatRequest()))
	keys := []schemas.Key{{ID: "key-1"}, {ID: "untracked"}}
	selected, err := plugin.FilterKeys(probeCtx, schemas.Azure, "gpt-4o-ptu", keys)
	require.NoError(t, err)
	require.Equal(t, []string{"key-1"}, keyIDs(selected))

	selected, err = plugin.FilterKeys(probeCtx, schemas.Azure, "gpt-4o-ptu", keys)
	require.NoError(t, err)
	require.Equal(t, []string{"key-1"}, keyIDs(selected))
}

func TestInitValidatesPoliciesAndDefaultsEnabled(t *testing.T) {
	policy := basePolicy()
	plugin, err := Init(testConfig(policy), nil)
	require.NoError(t, err)
	require.Len(t, plugin.matchingPolicies(schemas.Azure, "gpt-4o-ptu"), 1)

	policy.Condition.Signals[0].HeaderContains = stringPtr("spill")
	_, err = Init(testConfig(policy), nil)
	require.ErrorContains(t, err, "both header_value and header_contains")

	policy = basePolicy()
	policy.DefaultCooldown = "0s"
	_, err = Init(testConfig(policy), nil)
	require.ErrorContains(t, err, "positive Go duration")
}

func keyIDs(keys []schemas.Key) []string {
	ids := make([]string, len(keys))
	for i := range keys {
		ids[i] = keys[i].ID
	}
	return ids
}
