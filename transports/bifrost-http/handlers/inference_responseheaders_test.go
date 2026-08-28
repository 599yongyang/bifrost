package handlers

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestForwardProviderHeadersKeepsUpstreamHeadersInternal(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	headers := map[string]string{
		"x-amzn-requestid":   "req-123",
		"x-request-id":       "provider-request-id",
		"x-bifrost-provider": "spoofed-provider",
	}

	forwardProviderHeaders(ctx, headers)
	for name := range headers {
		assert.Empty(t, string(ctx.Response.Header.Peek(name)), "provider header %q leaked", name)
	}

	bifrostCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bifrostCtx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, headers)
	forwardProviderHeadersFromContext(ctx, bifrostCtx)
	for name := range headers {
		assert.Empty(t, string(ctx.Response.Header.Peek(name)), "provider header %q leaked from context", name)
	}
}
