package lib

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func assertNoPrivateResponseHeaders(t *testing.T, ctx *fasthttp.RequestCtx) {
	t.Helper()
	ctx.Response.Header.VisitAll(func(key, _ []byte) {
		name := strings.ToLower(string(key))
		assert.False(t, strings.HasPrefix(name, "x-bifrost-"), "leaked Bifrost header %q", name)
		assert.False(t, strings.HasPrefix(name, "x-moon-"), "leaked Moon header %q", name)
		assert.NotContains(t, name, "provider")
		assert.NotContains(t, name, "routing")
		assert.NotContains(t, name, "fallback")
		assert.NotContains(t, name, "resolved-model")
		assert.NotContains(t, name, "key-alias")
		assert.NotEqual(t, "x-amzn-requestid", name)
		assert.NotEqual(t, "x-request-id", name)
	})
}

func TestApplyBifrostResponseHeadersKeepsRoutingDetailsInternal(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Response.Header.SetContentType("application/json")
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="result.json"`)
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	routingInfo := schemas.RoutingInfo{
		Provider: schemas.Bedrock,
		Model:    "claude-sonnet-4-6",
		Key:      "prod-key-1",
	}
	bifrostCtx.SetValue(schemas.BifrostContextKeyRoutingInfo, routingInfo)
	bifrostCtx.SetValue(schemas.BifrostContextKeyFallbackIndex, 1)
	bifrostCtx.ResetUpstreamLatency()
	schemas.AddUpstreamLatency(bifrostCtx, 150*time.Millisecond)

	ApplyBifrostResponseHeaders(ctx, bifrostCtx, schemas.BifrostResponseExtraFields{
		Provider:               schemas.Bedrock,
		OriginalModelRequested: "claude-sonnet-4-6",
		ResolvedModelUsed:      "us.anthropic.claude-sonnet-4-6",
		RequestType:            schemas.ChatCompletionRequest,
		ProviderResponseHeaders: map[string]string{
			"x-amzn-requestid":   "req-789",
			"x-request-id":       "provider-request-id",
			"x-bifrost-provider": "spoofed-provider",
		},
		RoutingInfo: routingInfo,
	})

	assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))
	assert.Equal(t, `attachment; filename="result.json"`, string(ctx.Response.Header.Peek("Content-Disposition")))
	assertNoPrivateResponseHeaders(t, ctx)

	stored, ok := bifrostCtx.Value(schemas.BifrostContextKeyRoutingInfo).(schemas.RoutingInfo)
	require.True(t, ok)
	assert.Equal(t, routingInfo, stored)
	upstream, ok := schemas.GetUpstreamLatency(bifrostCtx)
	require.True(t, ok)
	assert.Equal(t, 150*time.Millisecond, upstream)
}

func TestStreamAndErrorResponseHeadersKeepRoutingDetailsInternal(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bifrostCtx.SetValue(schemas.BifrostContextKeyRoutingInfo, schemas.RoutingInfo{
		Provider: schemas.Bedrock,
		Model:    "claude-sonnet-4-6",
		Key:      "prod-key-1",
	})

	ApplyBifrostStreamResponseHeaders(ctx, bifrostCtx, schemas.ChatCompletionStreamRequest)
	ApplyBifrostErrorResponseHeaders(ctx, bifrostCtx, schemas.BifrostErrorExtraFields{
		Provider: schemas.Bedrock,
		RoutingInfo: schemas.RoutingInfo{
			Provider: schemas.Bedrock,
			Model:    "claude-sonnet-4-6",
		},
	})

	assertNoPrivateResponseHeaders(t, ctx)
}
