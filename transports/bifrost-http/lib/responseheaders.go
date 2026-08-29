package lib

import (
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// ApplyBifrostStreamResponseHeaders intentionally emits no provider or routing
// identity headers. RoutingInfo remains available on the BifrostContext for
// tracing, logging, and overhead accounting.
func ApplyBifrostStreamResponseHeaders(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext, _ schemas.RequestType) {
	ApplyBifrostResponseHeaders(ctx, bifrostCtx, schemas.BifrostResponseExtraFields{})
}

// ApplyBifrostErrorResponseHeaders intentionally emits no provider or routing
// identity headers. Error metadata remains available internally to plugins and
// observability exporters.
func ApplyBifrostErrorResponseHeaders(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext, _ schemas.BifrostErrorExtraFields) {
	ApplyBifrostResponseHeaders(ctx, bifrostCtx, schemas.BifrostResponseExtraFields{})
}

// ApplyBifrostResponseHeaders records the v2 transport response-header phase
// without exposing provider response headers or gateway routing details. Public
// responses must not reveal providers, resolved models, keys, aliases, fallback
// decisions, or upstream request identifiers.
func ApplyBifrostResponseHeaders(ctx *fasthttp.RequestCtx, _ *schemas.BifrostContext, _ schemas.BifrostResponseExtraFields) {
	// Keep the v2 overhead phase even though the public routing-header contract is
	// intentionally empty. This preserves tracing shape across Moon and stock v2.
	if t, ok := ctx.UserValue(schemas.BifrostContextKeyTracer).(schemas.Tracer); ok && t != nil {
		if _, h := t.StartSpanID(ctx, "transport-response-headers", schemas.SpanKindInternal); h != nil {
			defer t.EndSpan(h, schemas.SpanStatusOk, "")
		}
	}
}
