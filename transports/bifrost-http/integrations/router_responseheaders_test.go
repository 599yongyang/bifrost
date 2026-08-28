package integrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestForwardPublicPassthroughContentHeadersUsesStrictAllowlist(t *testing.T) {
	providerHeaders := map[string]string{
		"Content-Type":               "application/octet-stream",
		"Content-Disposition":        `attachment; filename="result.bin"`,
		"Content-Range":              "bytes 0-99/1000",
		"Accept-Ranges":              "bytes",
		"X-Bifrost-Provider":         "openai",
		"X-Bifrost-Resolved-Model":   "secret-model",
		"X-Amzn-RequestId":           "req-123",
		"X-Request-Id":               "provider-request-id",
		"X-Provider-Key":             "secret-key",
		"X-Routing-Fallback-Index":   "1",
		"Set-Cookie":                 "session=secret",
		"WWW-Authenticate":           "Bearer secret",
		"X-Arbitrary-Upstream-Value": "secret",
	}

	t.Run("non-stream permits content type and disposition", func(t *testing.T) {
		var header fasthttp.ResponseHeader
		forwardPublicPassthroughContentHeaders(&header, providerHeaders, true)

		assert.Equal(t, "application/octet-stream", string(header.ContentType()))
		assert.Equal(t, `attachment; filename="result.bin"`, string(header.Peek("Content-Disposition")))
		assert.Equal(t, "bytes 0-99/1000", string(header.Peek("Content-Range")))
		assert.Equal(t, "bytes", string(header.Peek("Accept-Ranges")))
		assert.Equal(t, 4, header.Len())
	})

	t.Run("stream permits disposition without overriding resolved content type", func(t *testing.T) {
		var header fasthttp.ResponseHeader
		header.SetContentType("text/event-stream")
		forwardPublicPassthroughContentHeaders(&header, providerHeaders, false)

		assert.Equal(t, "text/event-stream", string(header.ContentType()))
		assert.Equal(t, `attachment; filename="result.bin"`, string(header.Peek("Content-Disposition")))
		assert.Equal(t, "bytes 0-99/1000", string(header.Peek("Content-Range")))
		assert.Equal(t, "bytes", string(header.Peek("Accept-Ranges")))
		assert.Equal(t, 4, header.Len())
	})
}
