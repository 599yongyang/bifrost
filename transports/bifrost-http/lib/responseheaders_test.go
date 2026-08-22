package lib

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestResponseHeadersDoNotExposeRoutingDetails(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	ApplyBifrostResponseHeaders(ctx, bifrostCtx, schemas.BifrostResponseExtraFields{
		Provider:               schemas.Bedrock,
		OriginalModelRequested: "claude-sonnet-4-6",
		ResolvedModelUsed:      "us.anthropic.claude-sonnet-4-6",
		RequestType:            schemas.ChatCompletionRequest,
		ProviderResponseHeaders: map[string]string{
			"x-amzn-requestid": "req-789",
		},
		RoutingInfo: schemas.RoutingInfo{
			Provider: schemas.Bedrock,
			Model:    "claude-sonnet-4-6",
			Key:      "prod-key-1",
		},
	})

	ctx.Response.Header.VisitAll(func(key, _ []byte) {
		assert.NotContains(t, string(key), "bifrost")
		assert.NotContains(t, string(key), "moon")
		assert.NotEqual(t, "x-amzn-requestid", string(key))
	})
}

func TestStreamAndErrorResponsesDoNotExposeRoutingDetails(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	ApplyBifrostStreamResponseHeaders(ctx, bifrostCtx, schemas.ChatCompletionStreamRequest)
	ApplyBifrostErrorResponseHeaders(ctx, bifrostCtx, schemas.BifrostErrorExtraFields{
		Provider: schemas.Bedrock,
		RoutingInfo: schemas.RoutingInfo{
			Provider: schemas.Bedrock,
			Model:    "claude-sonnet-4-6",
		},
	})

	assert.Zero(t, ctx.Response.Header.Len())
}
