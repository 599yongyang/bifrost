package circuitbreaker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func stringPtr(value string) *string { return &value }

func basePolicy() Policy {
	return Policy{
		Name: "azure-ptu-spillover", PrimaryProvider: schemas.Azure, PrimaryModel: "gpt-4o-ptu",
		FallbackProvider: schemas.Azure, FallbackModel: "gpt-4o-paygo",
		Condition: Condition{Signals: []Signal{{
			Source: "response_header", HeaderName: "X-Ms-Is-Spilled-Over", HeaderValue: stringPtr("true"),
		}}}, DefaultCooldown: "30s",
	}
}

func requestContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
}

func chatRequest() *schemas.BifrostRequest {
	return &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest, ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.Azure, Model: "gpt-4o-ptu"}}
}

func chatResponse(headers map[string]string) *schemas.BifrostResponse {
	return &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{ExtraFields: schemas.BifrostResponseExtraFields{
		RoutingInfo: schemas.RoutingInfo{Provider: schemas.Azure, Model: "gpt-4o-ptu"}, ProviderResponseHeaders: headers,
	}}}
}

func initPlugin(t *testing.T, policy Policy) *Plugin {
	t.Helper()
	plugin, err := Init(&Config{Policies: []Policy{policy}}, nil)
	require.NoError(t, err)
	return plugin
}

func trip(t *testing.T, plugin *Plugin, keyID string) {
	t.Helper()
	ctx := requestContext()
	require.NoError(t, plugin.PreRequestHook(ctx, chatRequest()))
	ctx.SetValue(schemas.BifrostContextKeySelectedKeyID, keyID)
	_, _, err := plugin.PostLLMHook(ctx, chatResponse(map[string]string{"X-Ms-Is-Spilled-Over": "true"}), nil)
	require.NoError(t, err)
}

func TestSharedCircuitTripsReroutesAndUsesSingleHalfOpenProbe(t *testing.T) {
	policy := basePolicy()
	policy.CooldownHeader = "retry-after-ms"
	plugin := initPlugin(t, policy)
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }

	ctx := requestContext()
	require.NoError(t, plugin.PreRequestHook(ctx, chatRequest()))
	_, _, err := plugin.PostLLMHook(ctx, chatResponse(map[string]string{
		"x-ms-is-spilled-over": "TRUE", "Retry-After-Ms": "1500",
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

	concurrentReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), concurrentReq))
	require.Equal(t, "gpt-4o-paygo", concurrentReq.ChatRequest.Model)

	_, _, err = plugin.PostLLMHook(probeCtx, chatResponse(nil), nil)
	require.NoError(t, err)
	closedReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), closedReq))
	require.Equal(t, "gpt-4o-ptu", closedReq.ChatRequest.Model)
}

func TestKeySubCircuitsSuppressOnlyDegradedKeysThenOpenMainCircuit(t *testing.T) {
	policy := basePolicy()
	policy.PrimaryKeyIDs = []string{"key-1", "key-2"}
	plugin := initPlugin(t, policy)
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }
	keys := []schemas.Key{{ID: "key-1"}, {ID: "key-2"}, {ID: "untracked"}}

	trip(t, plugin, "key-1")
	filterCtx := requestContext()
	require.NoError(t, plugin.PreRequestHook(filterCtx, chatRequest()))
	filtered, err := plugin.FilterKeys(filterCtx, schemas.Azure, "gpt-4o-ptu", keys)
	require.NoError(t, err)
	require.Equal(t, []string{"key-2", "untracked"}, keyIDs(filtered))

	trip(t, plugin, "key-2")
	openReq := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), openReq))
	require.Equal(t, "gpt-4o-paygo", openReq.ChatRequest.Model)

	now = now.Add(31 * time.Second)
	probeCtx := requestContext()
	require.NoError(t, plugin.PreRequestHook(probeCtx, chatRequest()))
	probeKeys, err := plugin.FilterKeys(probeCtx, schemas.Azure, "gpt-4o-ptu", keys)
	require.NoError(t, err)
	require.Len(t, probeKeys, 1)
	require.Contains(t, []string{"key-1", "key-2"}, probeKeys[0].ID)
}

func TestKeyHalfOpenAllowsExactlyOneConcurrentProbe(t *testing.T) {
	policy := basePolicy()
	policy.PrimaryKeyIDs = []string{"key-1"}
	plugin := initPlugin(t, policy)
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }
	trip(t, plugin, "key-1")
	now = now.Add(31 * time.Second)

	keys := []schemas.Key{{ID: "key-1"}}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := requestContext()
			require.NoError(t, plugin.PreRequestHook(ctx, chatRequest()))
			filtered, err := plugin.FilterKeys(ctx, schemas.Azure, "gpt-4o-ptu", keys)
			require.NoError(t, err)
			if len(filtered) == 1 {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), admitted.Load())
}

func TestFailedSharedHalfOpenProbeKeepsCircuitOpen(t *testing.T) {
	plugin := initPlugin(t, basePolicy())
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }
	trip(t, plugin, "")
	now = now.Add(31 * time.Second)

	probeCtx := requestContext()
	probeRequest := chatRequest()
	require.NoError(t, plugin.PreRequestHook(probeCtx, probeRequest))
	_, _, err := plugin.PreLLMHook(probeCtx, probeRequest)
	require.NoError(t, err)
	_, _, err = plugin.PostLLMHook(probeCtx, nil, probeFailure())
	require.NoError(t, err)

	nextRequest := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), nextRequest))
	require.Equal(t, "gpt-4o-paygo", nextRequest.ChatRequest.Model)
}

func TestFailedKeyHalfOpenProbeKeepsSubCircuitOpen(t *testing.T) {
	policy := basePolicy()
	policy.PrimaryKeyIDs = []string{"key-1"}
	plugin := initPlugin(t, policy)
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	plugin.now = func() time.Time { return now }
	trip(t, plugin, "key-1")
	now = now.Add(31 * time.Second)

	probeCtx := requestContext()
	probeRequest := chatRequest()
	require.NoError(t, plugin.PreRequestHook(probeCtx, probeRequest))
	probeKeys, err := plugin.FilterKeys(probeCtx, schemas.Azure, "gpt-4o-ptu", []schemas.Key{{ID: "key-1"}})
	require.NoError(t, err)
	require.Equal(t, []string{"key-1"}, keyIDs(probeKeys))
	_, _, err = plugin.PostLLMHook(probeCtx, nil, probeFailure())
	require.NoError(t, err)

	nextRequest := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), nextRequest))
	require.Equal(t, "gpt-4o-paygo", nextRequest.ChatRequest.Model)
}

func probeFailure() *schemas.BifrostError {
	status := 503
	errorType := "provider_unavailable"
	return &schemas.BifrostError{
		StatusCode: &status,
		Type:       &errorType,
		Error:      &schemas.ErrorField{Type: &errorType, Message: "probe failed"},
		ExtraFields: schemas.BifrostErrorExtraFields{RoutingInfo: schemas.RoutingInfo{
			Provider: schemas.Azure,
			Model:    "gpt-4o-ptu",
		}},
	}
}

func TestConditionSupportsANDContainsAndExists(t *testing.T) {
	policy := basePolicy()
	policy.Condition = Condition{Operator: "AND", Signals: []Signal{
		{Source: "response_header", HeaderName: "x-capacity", HeaderContains: stringPtr("spill")},
		{Source: "response_header", HeaderName: "x-degraded"},
	}}
	plugin := initPlugin(t, policy)
	ctx := requestContext()
	require.NoError(t, plugin.PreRequestHook(ctx, chatRequest()))
	_, _, err := plugin.PostLLMHook(ctx, chatResponse(map[string]string{"X-Capacity": "PTU-SPILLOVER", "X-Degraded": ""}), nil)
	require.NoError(t, err)
	request := chatRequest()
	require.NoError(t, plugin.PreRequestHook(requestContext(), request))
	require.Equal(t, "gpt-4o-paygo", request.ChatRequest.Model)
}

func TestInitValidatesPoliciesAndDefaultsEnabled(t *testing.T) {
	policy := basePolicy()
	plugin := initPlugin(t, policy)
	require.Len(t, plugin.matchingPolicies(schemas.Azure, "gpt-4o-ptu"), 1)

	policy.Condition.Signals[0].HeaderContains = stringPtr("spill")
	_, err := Init(&Config{Policies: []Policy{policy}}, nil)
	require.ErrorContains(t, err, "both header_value and header_contains")
	policy = basePolicy()
	policy.DefaultCooldown = "0s"
	_, err = Init(&Config{Policies: []Policy{policy}}, nil)
	require.ErrorContains(t, err, "positive Go duration")
}

func keyIDs(keys []schemas.Key) []string {
	ids := make([]string, len(keys))
	for i := range keys {
		ids[i] = keys[i].ID
	}
	return ids
}
