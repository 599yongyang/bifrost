package lib

import (
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// ApplyBifrostStreamResponseHeaders intentionally emits no routed-identity
// headers. The public response contract exposes only request and trace IDs,
// which are written by the tracing middleware before a stream begins.
func ApplyBifrostStreamResponseHeaders(_ *fasthttp.RequestCtx, _ *schemas.BifrostContext, _ schemas.RequestType) {
}

// ApplyBifrostErrorResponseHeaders intentionally emits no provider or routing
// headers. Error responses use the same public response-header contract as
// successful responses.
func ApplyBifrostErrorResponseHeaders(_ *fasthttp.RequestCtx, _ *schemas.BifrostContext, _ schemas.BifrostErrorExtraFields) {
}

// ApplyBifrostResponseHeaders intentionally does not forward provider response
// headers or expose gateway routing details. This prevents callers from seeing
// providers, resolved models, key aliases, fallback decisions, or provider
// request identifiers.
func ApplyBifrostResponseHeaders(_ *fasthttp.RequestCtx, _ *schemas.BifrostContext, _ schemas.BifrostResponseExtraFields) {
}
