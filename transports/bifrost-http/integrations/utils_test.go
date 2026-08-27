package integrations

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/bedrock"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// testLogger implements schemas.Logger for testing (all no-ops)
type testLogger struct{}

func (t *testLogger) Debug(msg string, args ...any)                     {}
func (t *testLogger) Info(msg string, args ...any)                      {}
func (t *testLogger) Warn(msg string, args ...any)                      {}
func (t *testLogger) Error(msg string, args ...any)                     {}
func (t *testLogger) Fatal(msg string, args ...any)                     {}
func (t *testLogger) SetLevel(level schemas.LogLevel)                   {}
func (t *testLogger) SetOutputType(outputType schemas.LoggerOutputType) {}
func (t *testLogger) LogHTTPRequest(level schemas.LogLevel, msg string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

var _ schemas.Logger = (*testLogger)(nil)

func ptr(i int) *int {
	return &i
}

func strPtr(s string) *string {
	return &s
}

func newTestGenericRouter() *GenericRouter {
	return NewGenericRouter(nil, &mockHandlerStore{}, nil, nil, &testLogger{})
}

func newTestBifrostContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
}

func TestExtractAndParseFallbacks_GeminiGenerationRequest(t *testing.T) {
	router := newTestGenericRouter()
	geminiReq := &gemini.GeminiGenerationRequest{
		Model:     "gemini/gemini-3-flash-preview",
		Fallbacks: []string{"vertex/gemini-3-flash-preview"},
	}
	bifrostReq := &schemas.BifrostRequest{
		ResponsesRequest: &schemas.BifrostResponsesRequest{
			Provider: schemas.Gemini,
			Model:    "gemini-3-flash-preview",
		},
	}

	err := router.extractAndParseFallbacks(newTestBifrostContext(), geminiReq, bifrostReq)

	require.NoError(t, err)
	require.NotNil(t, bifrostReq.ResponsesRequest)
	require.Len(t, bifrostReq.ResponsesRequest.Fallbacks, 1)
	assert.Equal(t, schemas.Vertex, bifrostReq.ResponsesRequest.Fallbacks[0].Provider)
	assert.Equal(t, "gemini-3-flash-preview", bifrostReq.ResponsesRequest.Fallbacks[0].Model)
}

func TestExtractErrorFallbacksFromRequestAndApply(t *testing.T) {
	type requestCarrier struct {
		ErrorFallbacks json.RawMessage `json:"error_fallbacks,omitempty"`
	}
	type fallbackRule struct {
		Name      string             `json:"name,omitempty"`
		Fallbacks []schemas.Fallback `json:"fallbacks"`
	}
	type targetCarrier struct {
		ErrorFallbacks []fallbackRule `json:"error_fallbacks,omitempty"`
	}

	raw := json.RawMessage(`[{"name":"unsafe-images","when":{"message_contains":["unsafe"]},"fallbacks":["openai/gpt-image-1"]}]`)

	extracted, err := extractErrorFallbacksFromRequest(&requestCarrier{ErrorFallbacks: raw})
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(extracted))

	var target targetCarrier
	require.NoError(t, setErrorFallbacksOnValue(&target, extracted))
	require.Len(t, target.ErrorFallbacks, 1)
	assert.Equal(t, "unsafe-images", target.ErrorFallbacks[0].Name)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.OpenAI, Model: "gpt-image-1"}}, target.ErrorFallbacks[0].Fallbacks)
}

func TestSetErrorFallbacksOnValue_ValidatesMalformedRules(t *testing.T) {
	type targetCarrier struct {
		Model          string                      `json:"model"`
		ErrorFallbacks []schemas.ErrorFallbackRule `json:"error_fallbacks,omitempty"`
	}

	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name:    "empty matcher rejected",
			raw:     `[{"when":{},"fallbacks":["openai/gpt-4o"]}]`,
			wantErr: "must define at least one matcher",
		},
		{
			name:    "unknown category rejected",
			raw:     `[{"when":{"categories":["not-real"]},"fallbacks":["openai/gpt-4o"]}]`,
			wantErr: "is invalid",
		},
		{
			name:    "invalid status rejected",
			raw:     `[{"when":{"status_codes":[99]},"fallbacks":["openai/gpt-4o"]}]`,
			wantErr: "must be between 100 and 599",
		},
		{
			name:    "unknown field rejected",
			raw:     `[{"when":{"message_contains":["unsafe"],"bogus":true},"fallbacks":["openai/gpt-4o"]}]`,
			wantErr: "unknown field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &targetCarrier{Model: "gpt-4o"}
			err := setErrorFallbacksOnValue(target, json.RawMessage(tt.raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSetErrorFallbacksOnValue_InheritsProviderOnlyShorthand(t *testing.T) {
	type targetCarrier struct {
		Model          string                      `json:"model"`
		ErrorFallbacks []schemas.ErrorFallbackRule `json:"error_fallbacks,omitempty"`
	}

	target := &targetCarrier{Model: "openai/gpt-image-1"}
	raw := json.RawMessage(`[{"when":{"message_contains":["unsafe"]},"fallbacks":["azure/"]}]`)

	require.NoError(t, setErrorFallbacksOnValue(target, raw))
	require.Len(t, target.ErrorFallbacks, 1)
	require.Len(t, target.ErrorFallbacks[0].Fallbacks, 1)
	assert.Equal(t, schemas.Azure, target.ErrorFallbacks[0].Fallbacks[0].Provider)
	assert.Equal(t, "gpt-image-1", target.ErrorFallbacks[0].Fallbacks[0].Model)
}

func TestSetErrorFallbacksOnValue_AcceptsScenarioAndSupplement(t *testing.T) {
	type targetCarrier struct {
		Model          string                      `json:"model"`
		ErrorFallbacks []schemas.ErrorFallbackRule `json:"error_fallbacks,omitempty"`
	}

	target := &targetCarrier{Model: "gpt-image-1"}
	raw := json.RawMessage(`[{"scenario":"content_policy","supplement":{"providers":["custom-provider"],"message_contains_any":["unsafe"]},"fallbacks":["azure/"]}]`)

	require.NoError(t, setErrorFallbacksOnValue(target, raw))
	require.Len(t, target.ErrorFallbacks, 1)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, target.ErrorFallbacks[0].Scenario)
	require.NotNil(t, target.ErrorFallbacks[0].Supplement)
	assert.Equal(t, []schemas.ModelProvider{schemas.ModelProvider("custom-provider")}, target.ErrorFallbacks[0].Supplement.Providers)
	assert.Equal(t, []string{"unsafe"}, target.ErrorFallbacks[0].Supplement.MessageContainsAny)
}

func TestSetErrorFallbacksOnValue_RejectsMixedScenarioAndWhen(t *testing.T) {
	type targetCarrier struct {
		Model          string                      `json:"model"`
		ErrorFallbacks []schemas.ErrorFallbackRule `json:"error_fallbacks,omitempty"`
	}

	target := &targetCarrier{Model: "gpt-image-1"}
	raw := json.RawMessage(`[{"scenario":"content_policy","when":{"message_contains":["unsafe"]},"fallbacks":["azure/"]}]`)

	err := setErrorFallbacksOnValue(target, raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot define both scenario and when")
}

func TestStripTransportOnlyJSONFields_RemovesErrorFallbacks(t *testing.T) {
	raw := []byte(`{"model":"openai/gpt-4o","error_fallbacks":[{"when":{"message_contains":["unsafe"]},"fallbacks":["openai/gpt-image-1"]}],"messages":[{"role":"user","content":"hi"}]}`)

	stripped, err := stripTransportOnlyJSONFields(raw)
	require.NoError(t, err)
	assert.NotContains(t, string(stripped), `"error_fallbacks"`)
	assert.Contains(t, string(stripped), `"model":"openai/gpt-4o"`)
	assert.Contains(t, string(stripped), `"messages"`)
}

// TestSendStreamError_PropagatesProviderStatusCode verifies that sendStreamError
// sets the HTTP status code from the provider's BifrostError.StatusCode field.
// All three providers (OpenAI, Anthropic, Bedrock) return actual HTTP error codes
// for pre-stream errors, so Bifrost must propagate them faithfully.
func TestSendStreamError_PropagatesProviderStatusCode(t *testing.T) {
	tests := []struct {
		name               string
		statusCode         *int
		expectedStatusCode int
	}{
		{
			name:               "provider 400 - Bedrock ValidationException / OpenAI invalid_request_error",
			statusCode:         ptr(400),
			expectedStatusCode: 400,
		},
		{
			name:               "provider 429 - rate limiting (all providers)",
			statusCode:         ptr(429),
			expectedStatusCode: 429,
		},
		{
			name:               "provider 503 - Bedrock ServiceUnavailableException",
			statusCode:         ptr(503),
			expectedStatusCode: 503,
		},
		{
			name:               "provider 529 - Anthropic overloaded_error",
			statusCode:         ptr(529),
			expectedStatusCode: 529,
		},
		{
			name:               "nil StatusCode defaults to 500",
			statusCode:         nil,
			expectedStatusCode: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newTestGenericRouter()
			ctx := &fasthttp.RequestCtx{}
			bifrostCtx := newTestBifrostContext()

			bifrostErr := &schemas.BifrostError{
				StatusCode: tt.statusCode,
				Error: &schemas.ErrorField{
					Message: "test error",
				},
			}

			config := RouteConfig{
				ErrorConverter: func(ctx *schemas.BifrostContext, err *schemas.BifrostError) interface{} {
					return err
				},
			}

			router.sendStreamError(ctx, bifrostCtx, config, bifrostErr)

			assert.Equal(t, tt.expectedStatusCode, ctx.Response.StatusCode())
			assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))

			body := string(ctx.Response.Body())
			assert.True(t, sonic.Valid(ctx.Response.Body()), "response body should be valid JSON, got: %s", body)
			assert.False(t, strings.HasPrefix(body, "data: "), "response should not be SSE format")
		})
	}
}

// TestSendStreamError_OpenAIErrorFormat verifies the response body matches the
// public OpenAI error format without exposing internal gateway identity.
func TestSendStreamError_OpenAIErrorFormat(t *testing.T) {
	router := newTestGenericRouter()
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := newTestBifrostContext()

	bifrostErr := &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     ptr(400),
		Error: &schemas.ErrorField{
			Type:    strPtr("invalid_request_error"),
			Message: "content is empty",
		},
	}

	config := RouteConfig{
		ErrorConverter: func(ctx *schemas.BifrostContext, err *schemas.BifrostError) interface{} {
			return err
		},
	}

	router.sendStreamError(ctx, bifrostCtx, config, bifrostErr)

	assert.Equal(t, 400, ctx.Response.StatusCode())

	// Unmarshal and verify the structure
	var result map[string]interface{}
	err := sonic.Unmarshal(ctx.Response.Body(), &result)
	require.NoError(t, err)

	assert.NotContains(t, result, "is_bifrost_error")
	assert.Contains(t, result, "status_code")
	assert.Contains(t, result, "error")

	errorObj, ok := result["error"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "invalid_request_error", errorObj["type"])
	assert.Equal(t, "content is empty", errorObj["message"])
}

func TestSendError_ConfiguredTimeoutHidesGatewayIdentity(t *testing.T) {
	router := newTestGenericRouter()
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := newTestBifrostContext()

	bifrostErr := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     ptr(504),
		Error: &schemas.ErrorField{
			Message: schemas.TimeoutSourceBifrostHTTPClient.SafeMessage(),
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			TimeoutSource: schemas.TimeoutSourceBifrostHTTPClient,
		},
	}

	router.sendError(ctx, bifrostCtx, func(_ *schemas.BifrostContext, err *schemas.BifrostError) interface{} {
		return err
	}, bifrostErr)

	assert.Equal(t, 504, ctx.Response.StatusCode())
	body := strings.ToLower(string(ctx.Response.Body()))
	assert.NotContains(t, body, "bifrost")
	assert.NotContains(t, body, "is_bifrost_error")
	assert.Contains(t, body, `"status_code":504`)
	assert.Contains(t, body, `"message":"provider request reached the configured timeout"`)
	assert.Contains(t, body, `"timeout_source":"configured_provider_timeout"`)
}

// TestSendStreamError_AnthropicErrorFormat verifies the response body matches the
// Anthropic error format: {"type":"error","error":{"type":"...","message":"..."}}.
// Critically, it also verifies that the StreamConfig.ErrorConverter (which returns
// raw SSE strings) is NOT used — sendStreamError must use the route-level ErrorConverter.
func TestSendStreamError_AnthropicErrorFormat(t *testing.T) {
	router := newTestGenericRouter()
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := newTestBifrostContext()

	bifrostErr := &schemas.BifrostError{
		StatusCode: ptr(429),
		Error: &schemas.ErrorField{
			Type:    strPtr("rate_limit_error"),
			Message: "rate limited",
		},
	}

	config := RouteConfig{
		// Route-level: returns JSON-marshallable *AnthropicMessageError
		ErrorConverter: func(ctx *schemas.BifrostContext, err *schemas.BifrostError) interface{} {
			return anthropic.ToAnthropicChatCompletionError(err)
		},
		// Stream-level: returns raw SSE string — should NOT be used by sendStreamError
		StreamConfig: &StreamConfig{
			ErrorConverter: func(ctx *schemas.BifrostContext, err *schemas.BifrostError) interface{} {
				return anthropic.ToAnthropicResponsesStreamError(err)
			},
		},
	}

	router.sendStreamError(ctx, bifrostCtx, config, bifrostErr)

	assert.Equal(t, 429, ctx.Response.StatusCode())
	assert.Equal(t, "application/json", string(ctx.Response.Header.ContentType()))

	body := string(ctx.Response.Body())

	// Must NOT contain SSE markers — that would mean StreamConfig.ErrorConverter was used
	assert.NotContains(t, body, "event: error", "response should not contain SSE event markers")

	// Unmarshal and verify Anthropic error structure
	var result anthropic.AnthropicMessageError
	err := sonic.Unmarshal(ctx.Response.Body(), &result)
	require.NoError(t, err)

	assert.Equal(t, "error", result.Type)
	assert.Equal(t, "rate_limit_error", result.Error.Type)
	assert.Equal(t, "rate limited", result.Error.Message)
}

// TestSendStreamError_BedrockErrorFormat verifies the response body matches the
// Bedrock error format: {"__type":"ValidationException","message":"..."}.
func TestSendStreamError_BedrockErrorFormat(t *testing.T) {
	router := newTestGenericRouter()
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := newTestBifrostContext()

	bifrostErr := &schemas.BifrostError{
		StatusCode: ptr(400),
		Error: &schemas.ErrorField{
			Code:    strPtr("ValidationException"),
			Message: "validation error",
		},
	}

	config := RouteConfig{
		ErrorConverter: func(ctx *schemas.BifrostContext, err *schemas.BifrostError) interface{} {
			return bedrock.ToBedrockError(err)
		},
	}

	router.sendStreamError(ctx, bifrostCtx, config, bifrostErr)

	assert.Equal(t, 400, ctx.Response.StatusCode())

	// Unmarshal and verify Bedrock error structure
	var result bedrock.BedrockError
	err := sonic.Unmarshal(ctx.Response.Body(), &result)
	require.NoError(t, err)

	assert.Equal(t, "ValidationException", result.Type)
	assert.Equal(t, "validation error", result.Message)
}

// TestSendStreamError_HidesProviderHeaders verifies that provider response headers
// stored in the BifrostContext are not exposed to clients.
func TestSendStreamError_HidesProviderHeaders(t *testing.T) {
	router := newTestGenericRouter()
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := newTestBifrostContext()

	// Set provider response headers on the context
	bifrostCtx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, map[string]string{
		"x-amzn-requestid": "req-123",
		"x-amzn-errortype": "ValidationException",
	})

	bifrostErr := &schemas.BifrostError{
		StatusCode: ptr(400),
		Error: &schemas.ErrorField{
			Message: "validation error",
		},
	}

	config := RouteConfig{
		ErrorConverter: func(ctx *schemas.BifrostContext, err *schemas.BifrostError) interface{} {
			return err
		},
	}

	router.sendStreamError(ctx, bifrostCtx, config, bifrostErr)

	assert.Equal(t, 400, ctx.Response.StatusCode())
	assert.Empty(t, string(ctx.Response.Header.Peek("x-amzn-requestid")))
	assert.Empty(t, string(ctx.Response.Header.Peek("x-amzn-errortype")))
}

// TestTryStreamLargeResponse_HidesRoutedIdentityHeaders verifies that
// large-response early returns (speech audio, transcription, image bytes) do
// not expose provider or routing details.
func TestTryStreamLargeResponse_HidesRoutedIdentityHeaders(t *testing.T) {
	router := newTestGenericRouter()
	ctx := &fasthttp.RequestCtx{}
	bifrostCtx := newTestBifrostContext()
	bifrostCtx.SetValue(schemas.BifrostContextKeyLargeResponseMode, true)
	bifrostCtx.SetValue(schemas.BifrostContextKeyLargeResponseReader, io.NopCloser(strings.NewReader("audio-bytes")))

	extra := schemas.BifrostResponseExtraFields{
		Provider:          schemas.OpenAI,
		ResolvedModelUsed: "tts-1",
		RequestType:       schemas.SpeechRequest,
		RoutingInfo: schemas.RoutingInfo{
			Provider: schemas.OpenAI,
			Model:    "tts-1",
			Key:      "openai-key",
		},
	}

	handled := router.tryStreamLargeResponse(ctx, bifrostCtx, extra)

	require.True(t, handled, "large response mode active — call must handle the response")
	assert.Zero(t, ctx.Response.Header.Len())
}
