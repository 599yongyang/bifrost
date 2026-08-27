package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/providers/gemini"
	providerutils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/kvstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

var bifrostContextKeyProvider = schemas.BifrostContextKey("provider")

var availableIntegrations = []string{
	"openai",
	"anthropic",
	"genai",
	"litellm",
	"langchain",
	"bedrock",
	"pydantic",
	"cohere",
}

// newBifrostErrorWithCode is like newBifrostError but sets an explicit HTTP status code.
func newBifrostErrorWithCode(err error, message string, statusCode int) *schemas.BifrostError {
	e := newBifrostError(err, message)
	e.StatusCode = &statusCode
	return e
}

// newBifrostError wraps a standard error into a BifrostError with IsBifrostError set to false.
// This helper function reduces code duplication when handling non-Bifrost errors.
func newBifrostError(err error, message string) *schemas.BifrostError {
	if err == nil {
		return &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: message,
			},
		}
	}

	return &schemas.BifrostError{
		IsBifrostError: false,
		Error: &schemas.ErrorField{
			Message: message,
			Error:   err,
		},
	}
}

// safeGetRequestType safely obtains the request type from a BifrostStreamChunk chunk.
// It checks multiple sources in order of preference:
// 1. Response ExtraFields if any response is available
// 2. BifrostError ExtraFields if error is available and not nil
// 3. Falls back to "unknown" if no source is available
func safeGetRequestType(chunk *schemas.BifrostStreamChunk) string {
	if chunk == nil {
		return "unknown"
	}

	// Try to get RequestType from response ExtraFields (preferred source)
	switch {
	case chunk.BifrostTextCompletionResponse != nil:
		return string(chunk.BifrostTextCompletionResponse.ExtraFields.RequestType)
	case chunk.BifrostChatResponse != nil:
		return string(chunk.BifrostChatResponse.ExtraFields.RequestType)
	case chunk.BifrostResponsesStreamResponse != nil:
		return string(chunk.BifrostResponsesStreamResponse.ExtraFields.RequestType)
	case chunk.BifrostSpeechStreamResponse != nil:
		return string(chunk.BifrostSpeechStreamResponse.ExtraFields.RequestType)
	case chunk.BifrostTranscriptionStreamResponse != nil:
		return string(chunk.BifrostTranscriptionStreamResponse.ExtraFields.RequestType)
	}

	// Try to get RequestType from error ExtraFields (fallback)
	if chunk.BifrostError != nil && chunk.BifrostError.ExtraFields.RequestType != "" {
		return string(chunk.BifrostError.ExtraFields.RequestType)
	}

	// Final fallback
	return "unknown"
}

// extractHeadersFromRequest extracts headers from the request and returns them as a map.
// It uses the fasthttp.RequestCtx.Header.All() method to iterate over all headers.
func extractHeadersFromRequest(ctx *fasthttp.RequestCtx) map[string][]string {
	headers := make(map[string][]string)

	for key, value := range ctx.Request.Header.All() {
		keyStr := string(key)
		headers[keyStr] = append(headers[keyStr], string(value))
	}

	return headers
}

// extractExactPath returns the request path *after* the integration prefix,
// preserving the original query string exactly as sent by the client.
//
// Example:
//
//	/openai/v1/chat/completions?model=gpt-4o  ->  v1/chat/completions?model=gpt-4o
func extractExactPath(ctx *fasthttp.RequestCtx) string {
	// ctx.Path() returns only the path (no query) as a []byte backed by fasthttp’s internal buffers.
	// Treat it as read-only; don’t append to it directly.
	path := ctx.Path() // e.g. "/openai/v1/chat/completions"

	// Strip the integration prefix only if it’s at the start.
	for _, integration := range availableIntegrations {
		if bytes.HasPrefix(path, []byte("/"+integration+"/")) {
			path = path[len("/"+integration+"/"):]
			break
		}
	}

	// Raw query string as sent by client (unparsed, preserves ordering/duplicates/encoding).
	q := ctx.URI().QueryString() // e.g. "model=gpt-4o&stream=true"

	if len(q) == 0 {
		// No query → just return the (possibly trimmed) path.
		return string(path)
	}

	// --- Build "<path>?<query>" efficiently and safely ---
	//
	// Why not do: return string(path) + "?" + string(q) ?
	//   - That allocates multiple temporary strings and may copy data more than necessary.
	//
	// Why not append into 'path' directly?
	//   - 'path' may alias fasthttp’s internal buffers; mutating/expanding it could corrupt request state.
	//
	// We instead allocate a new buffer with exact capacity and copy into it,
	// staying in []byte until the final string conversion (1 allocation for the new slice).
	out := make([]byte, 0, len(path)+1+len(q)) // pre-size: path + "?" + query
	out = append(out, path...)                 // copy path bytes
	out = append(out, '?')                     // separator
	out = append(out, q...)                    // copy raw query bytes

	return string(out)
}

// sendStreamError sends an error response for a streaming request that failed before streaming started.
// It propagates the provider's HTTP status code and returns a JSON error body (not SSE format),
// since no streaming has begun and clients should receive a standard error response.
func (g *GenericRouter) sendStreamError(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext, config RouteConfig, bifrostErr *schemas.BifrostError) {
	bifrostErr = lib.SanitizeBifrostErrorForClient(bifrostErr)
	if bifrostErr == nil {
		bifrostErr = newBifrostErrorWithCode(nil, lib.ClientSafeInternalErrorMessage, fasthttp.StatusInternalServerError)
	}

	// Provider and routed-identity headers are intentionally hidden from clients.
	lib.ApplyBifrostErrorResponseHeaders(ctx, bifrostCtx, bifrostErr.ExtraFields)

	// Set the HTTP status code from the provider error
	if bifrostErr.StatusCode != nil {
		ctx.SetStatusCode(*bifrostErr.StatusCode)
	} else {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
	}
	ctx.SetContentType("application/json")

	// Always use the route-level ErrorConverter (not StreamConfig.ErrorConverter) because
	// sendStreamError returns JSON, not SSE. StreamConfig.ErrorConverter is designed for
	// in-stream SSE errors (e.g., Anthropic's returns a raw SSE string that would be
	// double-escaped by JSON marshaling).
	errorResponse := config.ErrorConverter(bifrostCtx, bifrostErr)
	errorResponse = lib.ClientErrorPayload(errorResponse)

	errorJSON, err := sonic.Marshal(errorResponse)
	if err != nil {
		g.logger.Error("failed to marshal error response", "err", err, "path", extractExactPath(ctx))
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("text/plain; charset=utf-8")
		ctx.SetBodyString(fmt.Sprintf("failed to encode error response: %v", err))
		return
	}

	ctx.SetBody(errorJSON)
}

// sendError sends an error response with the appropriate status code and JSON body.
// It handles different error types (string, error interface, or arbitrary objects).
func (g *GenericRouter) sendError(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext, errorConverter ErrorConverter, bifrostErr *schemas.BifrostError) {
	bifrostErr = lib.SanitizeBifrostErrorForClient(bifrostErr)
	if bifrostErr == nil {
		bifrostErr = newBifrostErrorWithCode(nil, lib.ClientSafeInternalErrorMessage, fasthttp.StatusInternalServerError)
	}

	// Provider and routed-identity headers are intentionally hidden from clients.
	lib.ApplyBifrostErrorResponseHeaders(ctx, bifrostCtx, bifrostErr.ExtraFields)

	if bifrostErr.StatusCode != nil {
		ctx.SetStatusCode(*bifrostErr.StatusCode)
	} else if !bifrostErr.IsBifrostError {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
	} else {
		if bifrostErr.Error != nil &&
			(bifrostErr.Error.Message == bifrost.ProviderAutoResolveErrorMessage ||
				bifrostErr.Error.Message == bifrost.ModelAutoResolveErrorMessage) {
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
		} else {
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		}
	}
	ctx.SetContentType("application/json")

	// Marshal the error for response and log the error for diagnostics
	responseObj := errorConverter(bifrostCtx, bifrostErr)
	responseObj = lib.ClientErrorPayload(responseObj)
	errorBody, err := sonic.Marshal(responseObj)
	if err != nil {
		// Log the marshal failure and return a plain text error
		g.logger.Error("failed to marshal error response", "err", err, "path", extractExactPath(ctx))
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("text/plain; charset=utf-8")
		ctx.SetBodyString(fmt.Sprintf("failed to encode error response: %v", err))
		return
	}

	ctx.SetBody(errorBody)
}

// sendSuccess sends a successful response with HTTP 200 status and JSON body.
func (g *GenericRouter) sendSuccess(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext, errorConverter ErrorConverter, response interface{}, extraHeaders map[string]string) {
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")

	if extraHeaders != nil {
		for key, value := range extraHeaders {
			ctx.Response.Header.Set(key, value)
		}
	}

	responseBody, err := sonic.Marshal(response)
	if err != nil {
		g.sendError(ctx, bifrostCtx, errorConverter, newBifrostError(err, "failed to encode response"))
		return
	}

	ctx.SetBody(responseBody)
}

// tryStreamLargeResponse checks if large response mode was activated by the provider,
// sets the transport marker, and streams the response directly to the client.
// Returns true if the response was handled (caller should return).
// extra carries the routed identity of the response being streamed; callers
// without one (pre-chunk streaming paths) pass the zero value, which emits nothing.
func (g *GenericRouter) tryStreamLargeResponse(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext, extra schemas.BifrostResponseExtraFields) bool {
	isLargeResponse, ok := bifrostCtx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool)
	if !ok || !isLargeResponse {
		return false
	}
	// Routed identity is intentionally not exposed on public responses.
	lib.ApplyBifrostResponseHeaders(ctx, bifrostCtx, extra)
	if g.streamLargeResponse(ctx, bifrostCtx) {
		ctx.SetUserValue(lib.FastHTTPUserValueLargeResponseMode, true)
	}
	return true
}

// streamLargeResponse streams the large response body directly from the upstream provider to the client.
// This bypasses the normal serialize → set body path, piping the response bytes unchanged.
func (g *GenericRouter) streamLargeResponse(ctx *fasthttp.RequestCtx, bifrostCtx *schemas.BifrostContext) bool {
	// Enterprise hook: wrap the reader with Phase B scanning (e.g., usage extraction
	// from the full response stream) before streaming to client.
	if g.largeResponseHook != nil {
		g.largeResponseHook(ctx, bifrostCtx)
	}

	if !lib.StreamLargeResponseBody(ctx, bifrostCtx) {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetBodyString("large response reader not available")
		return false
	}
	return true
}

// extractAndParseFallbacks extracts fallbacks from the integration request and adds them to the BifrostRequest.
func (g *GenericRouter) extractAndParseFallbacks(ctx *schemas.BifrostContext, req interface{}, bifrostReq *schemas.BifrostRequest) error {
	// Check if the request has a fallbacks field ([]string)
	fallbacks, err := g.extractFallbacksFromRequest(req)
	if err != nil {
		return fmt.Errorf("failed to extract fallbacks: %w", err)
	}

	if len(fallbacks) == 0 {
		return nil // No fallbacks to process
	}

	provider, _, _ := bifrostReq.GetRequestFields()

	// Parse fallbacks from strings to Fallback structs
	parsedFallbacks := make([]schemas.Fallback, 0, len(fallbacks))
	for _, fallbackStr := range fallbacks {
		if fallbackStr == "" {
			continue // Skip empty strings
		}

		// Use ParseModelString to extract provider and model
		provider, model := schemas.ParseModelString(fallbackStr, provider)

		parsedFallback := schemas.Fallback{
			Provider: provider,
			Model:    model,
		}
		parsedFallbacks = append(parsedFallbacks, parsedFallback)
	}

	if len(parsedFallbacks) == 0 {
		return nil // No valid fallbacks found
	}

	// Add fallbacks to the main BifrostRequest
	bifrostReq.SetFallbacks(parsedFallbacks)

	// Also add fallbacks to the specific request type if it exists
	switch bifrostReq.RequestType {
	case schemas.TextCompletionRequest, schemas.TextCompletionStreamRequest:
		if bifrostReq.TextCompletionRequest != nil {
			bifrostReq.TextCompletionRequest.Fallbacks = parsedFallbacks
		}
	case schemas.ChatCompletionRequest, schemas.ChatCompletionStreamRequest:
		if bifrostReq.ChatRequest != nil {
			bifrostReq.ChatRequest.Fallbacks = parsedFallbacks
		}
	case schemas.ResponsesRequest, schemas.ResponsesStreamRequest:
		if bifrostReq.ResponsesRequest != nil {
			bifrostReq.ResponsesRequest.Fallbacks = parsedFallbacks
		}
	case schemas.EmbeddingRequest:
		if bifrostReq.EmbeddingRequest != nil {
			bifrostReq.EmbeddingRequest.Fallbacks = parsedFallbacks
		}
	case schemas.RerankRequest:
		if bifrostReq.RerankRequest != nil {
			bifrostReq.RerankRequest.Fallbacks = parsedFallbacks
		}
	case schemas.SpeechRequest, schemas.SpeechStreamRequest:
		if bifrostReq.SpeechRequest != nil {
			bifrostReq.SpeechRequest.Fallbacks = parsedFallbacks
		}
	case schemas.TranscriptionRequest, schemas.TranscriptionStreamRequest:
		if bifrostReq.TranscriptionRequest != nil {
			bifrostReq.TranscriptionRequest.Fallbacks = parsedFallbacks
		}
	case schemas.ImageGenerationRequest, schemas.ImageGenerationStreamRequest:
		if bifrostReq.ImageGenerationRequest != nil {
			bifrostReq.ImageGenerationRequest.Fallbacks = parsedFallbacks
		}
	}

	return nil
}

func copyRawJSONMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	copied := make(json.RawMessage, len(raw))
	copy(copied, raw)
	return copied
}

func stripTransportOnlyJSONFields(rawBody []byte) ([]byte, error) {
	if len(rawBody) == 0 || !json.Valid(rawBody) {
		return rawBody, nil
	}
	if !providerutils.JSONFieldExists(rawBody, "error_fallbacks") {
		return rawBody, nil
	}
	stripped, err := providerutils.DeleteJSONField(rawBody, "error_fallbacks")
	if err != nil {
		return nil, err
	}
	return stripped, nil
}

func parseErrorFallbacksFormValue(values []string) (json.RawMessage, error) {
	if len(values) == 0 {
		return nil, nil
	}
	nonEmpty := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}
	if len(nonEmpty) == 0 {
		return nil, nil
	}
	if len(nonEmpty) == 1 {
		raw := json.RawMessage(nonEmpty[0])
		if !json.Valid(raw) {
			return nil, fmt.Errorf("error_fallbacks must be valid JSON")
		}
		return copyRawJSONMessage(raw), nil
	}
	elements := make([]json.RawMessage, 0, len(nonEmpty))
	for _, value := range nonEmpty {
		raw := json.RawMessage(value)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("error_fallbacks must be valid JSON")
		}
		elements = append(elements, raw)
	}
	encoded, err := json.Marshal(elements)
	if err != nil {
		return nil, fmt.Errorf("failed to encode error_fallbacks: %w", err)
	}
	return encoded, nil
}

var validErrorFallbackCategories = map[string]schemas.FailureCategory{
	string(schemas.FailureCategoryContentPolicy):        schemas.FailureCategoryContentPolicy,
	string(schemas.FailureCategoryUnsupportedOperation): schemas.FailureCategoryUnsupportedOperation,
	string(schemas.FailureCategoryRateLimit):            schemas.FailureCategoryRateLimit,
	string(schemas.FailureCategoryAuthentication):       schemas.FailureCategoryAuthentication,
	string(schemas.FailureCategoryBilling):              schemas.FailureCategoryBilling,
	string(schemas.FailureCategoryPermission):           schemas.FailureCategoryPermission,
	string(schemas.FailureCategoryTimeout):              schemas.FailureCategoryTimeout,
	string(schemas.FailureCategoryProviderUnavailable):  schemas.FailureCategoryProviderUnavailable,
	string(schemas.FailureCategoryNetwork):              schemas.FailureCategoryNetwork,
	string(schemas.FailureCategoryInvalidRequest):       schemas.FailureCategoryInvalidRequest,
	string(schemas.FailureCategoryInternal):             schemas.FailureCategoryInternal,
	string(schemas.FailureCategoryUnknown):              schemas.FailureCategoryUnknown,
}

func strictDecodeJSON(raw []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func currentErrorFallbackModel(model string) string {
	_, currentModel := schemas.ParseModelString(strings.TrimSpace(model), "")
	return strings.TrimSpace(currentModel)
}

func normalizeErrorFallbacksRaw(raw json.RawMessage, currentModel string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	currentModel = currentErrorFallbackModel(currentModel)
	type rawErrorFallbackSupplement struct {
		Providers          []string `json:"providers,omitempty"`
		ErrorCodes         []string `json:"error_codes,omitempty"`
		ErrorTypes         []string `json:"error_types,omitempty"`
		StatusCodes        []int    `json:"status_codes,omitempty"`
		MessageContainsAny []string `json:"message_contains_any,omitempty"`
	}
	type rawErrorFallbackRule struct {
		Name       string                      `json:"name,omitempty"`
		Scenario   string                      `json:"scenario,omitempty"`
		Supplement *rawErrorFallbackSupplement `json:"supplement,omitempty"`
		When       json.RawMessage             `json:"when,omitempty"`
		Fallbacks  []json.RawMessage           `json:"fallbacks,omitempty"`
	}
	var rawRules []rawErrorFallbackRule
	if err := strictDecodeJSON(raw, &rawRules); err != nil {
		return nil, err
	}
	if len(rawRules) == 0 {
		return nil, fmt.Errorf("error_fallbacks must contain at least one rule")
	}
	type rawErrorFallbackCondition struct {
		Categories      []string `json:"categories,omitempty"`
		ErrorCodes      []string `json:"error_codes,omitempty"`
		ErrorTypes      []string `json:"error_types,omitempty"`
		StatusCodes     []int    `json:"status_codes,omitempty"`
		MessageContains []string `json:"message_contains,omitempty"`
	}
	normalizedRules := make([]schemas.ErrorFallbackRule, 0, len(rawRules))
	for ruleIndex, rule := range rawRules {
		normalizedRule := schemas.ErrorFallbackRule{Name: strings.TrimSpace(rule.Name)}

		scenario := strings.ToLower(strings.TrimSpace(rule.Scenario))
		if scenario != "" {
			normalizedScenario, ok := validErrorFallbackCategories[scenario]
			if !ok {
				return nil, fmt.Errorf("error_fallbacks[%d].scenario %q is invalid", ruleIndex, scenario)
			}
			normalizedRule.Scenario = normalizedScenario
		}
		if normalizedRule.Scenario != "" && len(rule.When) > 0 {
			return nil, fmt.Errorf("error_fallbacks[%d] cannot define both scenario and when", ruleIndex)
		}

		if rule.Supplement != nil {
			if normalizedRule.Scenario == "" {
				return nil, fmt.Errorf("error_fallbacks[%d].supplement requires scenario", ruleIndex)
			}
			normalizedSupplement := &schemas.ErrorFallbackSupplement{}
			for providerIndex, provider := range rule.Supplement.Providers {
				trimmedProvider := strings.ToLower(strings.TrimSpace(provider))
				if trimmedProvider == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].supplement.providers[%d] must not be empty", ruleIndex, providerIndex)
				}
				normalizedSupplement.Providers = append(normalizedSupplement.Providers, schemas.ModelProvider(trimmedProvider))
			}
			for codeIndex, code := range rule.Supplement.ErrorCodes {
				trimmedCode := strings.TrimSpace(code)
				if trimmedCode == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].supplement.error_codes[%d] must not be empty", ruleIndex, codeIndex)
				}
				normalizedSupplement.ErrorCodes = append(normalizedSupplement.ErrorCodes, trimmedCode)
			}
			for typeIndex, errorType := range rule.Supplement.ErrorTypes {
				trimmedType := strings.TrimSpace(errorType)
				if trimmedType == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].supplement.error_types[%d] must not be empty", ruleIndex, typeIndex)
				}
				normalizedSupplement.ErrorTypes = append(normalizedSupplement.ErrorTypes, trimmedType)
			}
			for statusIndex, statusCode := range rule.Supplement.StatusCodes {
				if statusCode < 100 || statusCode > 599 {
					return nil, fmt.Errorf("error_fallbacks[%d].supplement.status_codes[%d] must be between 100 and 599", ruleIndex, statusIndex)
				}
				normalizedSupplement.StatusCodes = append(normalizedSupplement.StatusCodes, statusCode)
			}
			for messageIndex, messageContains := range rule.Supplement.MessageContainsAny {
				trimmedMessage := strings.TrimSpace(messageContains)
				if trimmedMessage == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].supplement.message_contains_any[%d] must not be empty", ruleIndex, messageIndex)
				}
				normalizedSupplement.MessageContainsAny = append(normalizedSupplement.MessageContainsAny, trimmedMessage)
			}
			if len(normalizedSupplement.ErrorCodes) == 0 &&
				len(normalizedSupplement.ErrorTypes) == 0 &&
				len(normalizedSupplement.StatusCodes) == 0 &&
				len(normalizedSupplement.MessageContainsAny) == 0 {
				return nil, fmt.Errorf("error_fallbacks[%d].supplement must define at least one non-provider matcher", ruleIndex)
			}
			hasNonProviderMatchers := len(normalizedSupplement.ErrorCodes) > 0 ||
				len(normalizedSupplement.ErrorTypes) > 0 ||
				len(normalizedSupplement.StatusCodes) > 0 ||
				len(normalizedSupplement.MessageContainsAny) > 0
			hasAnyMatchers := len(normalizedSupplement.Providers) > 0 || hasNonProviderMatchers
			if len(normalizedSupplement.Providers) > 0 && !hasNonProviderMatchers {
				return nil, fmt.Errorf("error_fallbacks[%d].supplement must define at least one non-provider matcher", ruleIndex)
			}
			if hasAnyMatchers {
				normalizedRule.Supplement = normalizedSupplement
			}
		}

		if len(rule.When) > 0 {
			var rawCondition rawErrorFallbackCondition
			if err := strictDecodeJSON(rule.When, &rawCondition); err != nil {
				return nil, fmt.Errorf("error_fallbacks[%d].when: %w", ruleIndex, err)
			}
			matcherCount := 0
			for categoryIndex, category := range rawCondition.Categories {
				trimmedCategory := strings.TrimSpace(category)
				if trimmedCategory == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].when.categories[%d] must not be empty", ruleIndex, categoryIndex)
				}
				normalizedCategory, ok := validErrorFallbackCategories[trimmedCategory]
				if !ok {
					return nil, fmt.Errorf("error_fallbacks[%d].when.categories[%d] %q is invalid", ruleIndex, categoryIndex, trimmedCategory)
				}
				normalizedRule.When.Categories = append(normalizedRule.When.Categories, normalizedCategory)
				matcherCount++
			}
			for codeIndex, code := range rawCondition.ErrorCodes {
				trimmedCode := strings.TrimSpace(code)
				if trimmedCode == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].when.error_codes[%d] must not be empty", ruleIndex, codeIndex)
				}
				normalizedRule.When.ErrorCodes = append(normalizedRule.When.ErrorCodes, trimmedCode)
				matcherCount++
			}
			for typeIndex, errorType := range rawCondition.ErrorTypes {
				trimmedType := strings.TrimSpace(errorType)
				if trimmedType == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].when.error_types[%d] must not be empty", ruleIndex, typeIndex)
				}
				normalizedRule.When.ErrorTypes = append(normalizedRule.When.ErrorTypes, trimmedType)
				matcherCount++
			}
			for statusIndex, statusCode := range rawCondition.StatusCodes {
				if statusCode < 100 || statusCode > 599 {
					return nil, fmt.Errorf("error_fallbacks[%d].when.status_codes[%d] must be between 100 and 599", ruleIndex, statusIndex)
				}
				normalizedRule.When.StatusCodes = append(normalizedRule.When.StatusCodes, statusCode)
				matcherCount++
			}
			for messageIndex, messageContains := range rawCondition.MessageContains {
				trimmedMessage := strings.TrimSpace(messageContains)
				if trimmedMessage == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].when.message_contains[%d] must not be empty", ruleIndex, messageIndex)
				}
				normalizedRule.When.MessageContains = append(normalizedRule.When.MessageContains, trimmedMessage)
				matcherCount++
			}
			if matcherCount == 0 {
				return nil, fmt.Errorf("error_fallbacks[%d].when must define at least one matcher", ruleIndex)
			}
		}

		if normalizedRule.Scenario == "" && len(rule.When) == 0 {
			return nil, fmt.Errorf("error_fallbacks[%d] must define either scenario or when", ruleIndex)
		}
		if len(rule.Fallbacks) == 0 {
			return nil, fmt.Errorf("error_fallbacks[%d].fallbacks must contain at least one fallback", ruleIndex)
		}
		if len(rule.Fallbacks) > 0 {
			normalizedRule.Fallbacks = make([]schemas.Fallback, 0, len(rule.Fallbacks))
		}
		for fallbackIndex, fallbackRaw := range rule.Fallbacks {
			trimmed := strings.TrimSpace(string(fallbackRaw))
			if trimmed == "" || trimmed == "null" {
				return nil, fmt.Errorf("error_fallbacks[%d].fallbacks[%d] must not be empty", ruleIndex, fallbackIndex)
			}
			if strings.HasPrefix(trimmed, "\"") {
				var fallbackString string
				if err := strictDecodeJSON(fallbackRaw, &fallbackString); err != nil {
					return nil, fmt.Errorf("error_fallbacks[%d].fallbacks[%d]: %w", ruleIndex, fallbackIndex, err)
				}
				fallbackProvider, fallbackModel := schemas.ParseModelString(strings.TrimSpace(fallbackString), "")
				if fallbackProvider == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].fallbacks[%d] %q must include a known provider prefix", ruleIndex, fallbackIndex, fallbackString)
				}
				if strings.TrimSpace(fallbackModel) == "" {
					if currentModel == "" {
						return nil, fmt.Errorf("error_fallbacks[%d].fallbacks[%d] %q requires a current model to inherit", ruleIndex, fallbackIndex, fallbackString)
					}
					fallbackModel = currentModel
				}
				normalizedRule.Fallbacks = append(normalizedRule.Fallbacks, schemas.Fallback{
					Provider: fallbackProvider,
					Model:    strings.TrimSpace(fallbackModel),
				})
				continue
			}
			var fallback struct {
				Provider schemas.ModelProvider `json:"provider"`
				Model    string                `json:"model"`
			}
			if err := strictDecodeJSON(fallbackRaw, &fallback); err != nil {
				return nil, fmt.Errorf("error_fallbacks[%d].fallbacks[%d]: %w", ruleIndex, fallbackIndex, err)
			}
			if fallback.Provider == "" || !schemas.IsKnownProvider(string(fallback.Provider)) {
				return nil, fmt.Errorf("error_fallbacks[%d].fallbacks[%d].provider must be a known provider", ruleIndex, fallbackIndex)
			}
			fallbackModel := strings.TrimSpace(fallback.Model)
			if fallbackModel == "" {
				if currentModel == "" {
					return nil, fmt.Errorf("error_fallbacks[%d].fallbacks[%d] requires a current model to inherit", ruleIndex, fallbackIndex)
				}
				fallbackModel = currentModel
			}
			normalizedRule.Fallbacks = append(normalizedRule.Fallbacks, schemas.Fallback{
				Provider: fallback.Provider,
				Model:    fallbackModel,
			})
		}
		normalizedRules = append(normalizedRules, normalizedRule)
	}
	normalized, err := json.Marshal(normalizedRules)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func setErrorFallbacksOnValue(target interface{}, raw json.RawMessage) error {
	if len(raw) == 0 || target == nil {
		return nil
	}
	targetValue := reflect.ValueOf(target)
	if !targetValue.IsValid() || targetValue.Kind() != reflect.Ptr || targetValue.IsNil() {
		return nil
	}
	currentModel := ""
	if targetValue.Elem().IsValid() && targetValue.Elem().Kind() == reflect.Struct {
		modelField := targetValue.Elem().FieldByName("Model")
		if modelField.IsValid() && modelField.Kind() == reflect.String {
			currentModel = modelField.String()
		}
	}
	normalized, err := normalizeErrorFallbacksRaw(raw, currentModel)
	if err != nil {
		return err
	}
	targetValue = targetValue.Elem()
	if !targetValue.IsValid() || targetValue.Kind() != reflect.Struct {
		return nil
	}
	field := targetValue.FieldByName("ErrorFallbacks")
	if !field.IsValid() {
		targetType := targetValue.Type()
		for i := 0; i < targetValue.NumField(); i++ {
			structField := targetType.Field(i)
			if strings.Split(structField.Tag.Get("json"), ",")[0] == "error_fallbacks" {
				field = targetValue.Field(i)
				break
			}
		}
	}
	if !field.IsValid() || !field.CanSet() {
		return nil
	}
	if field.Kind() == reflect.Ptr {
		value := reflect.New(field.Type().Elem())
		if err := json.Unmarshal(normalized, value.Interface()); err != nil {
			return err
		}
		field.Set(value)
		return nil
	}
	value := reflect.New(field.Type())
	if err := json.Unmarshal(normalized, value.Interface()); err != nil {
		return err
	}
	field.Set(value.Elem())
	return nil
}

func extractErrorFallbacksFromRequest(req interface{}) (json.RawMessage, error) {
	if req == nil {
		return nil, nil
	}
	reqValue := reflect.ValueOf(req)
	if reqValue.Kind() == reflect.Ptr {
		if reqValue.IsNil() {
			return nil, nil
		}
		reqValue = reqValue.Elem()
	}
	if reqValue.Kind() != reflect.Struct {
		return nil, nil
	}
	field := reqValue.FieldByName("ErrorFallbacks")
	if !field.IsValid() {
		reqType := reqValue.Type()
		for i := 0; i < reqValue.NumField(); i++ {
			structField := reqType.Field(i)
			if strings.Split(structField.Tag.Get("json"), ",")[0] == "error_fallbacks" {
				field = reqValue.Field(i)
				break
			}
		}
	}
	if !field.IsValid() {
		return nil, nil
	}
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return nil, nil
		}
		field = field.Elem()
	}
	if !field.IsValid() {
		return nil, nil
	}
	if raw, ok := field.Interface().(json.RawMessage); ok {
		if len(raw) == 0 {
			return nil, nil
		}
		return copyRawJSONMessage(raw), nil
	}
	if field.Kind() == reflect.String {
		trimmed := strings.TrimSpace(field.String())
		if trimmed == "" {
			return nil, nil
		}
		raw := json.RawMessage(trimmed)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("error_fallbacks must be valid JSON")
		}
		return copyRawJSONMessage(raw), nil
	}
	if field.Kind() == reflect.Slice && field.IsNil() {
		return nil, nil
	}
	raw, err := sonic.Marshal(field.Interface())
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func applyErrorFallbacksToBifrostRequest(bifrostReq *schemas.BifrostRequest, raw json.RawMessage) error {
	if len(raw) == 0 || bifrostReq == nil {
		return nil
	}
	_, currentModel, _ := bifrostReq.GetRequestFields()
	normalized, err := normalizeErrorFallbacksRaw(raw, currentModel)
	if err != nil {
		return err
	}
	var rules []schemas.ErrorFallbackRule
	if err := strictDecodeJSON(normalized, &rules); err != nil {
		return err
	}
	bifrostReq.SetErrorFallbacks(rules)
	reqValue := reflect.ValueOf(bifrostReq)
	if reqValue.Kind() != reflect.Ptr || reqValue.IsNil() {
		return nil
	}
	reqValue = reqValue.Elem()
	if reqValue.Kind() != reflect.Struct {
		return nil
	}
	for i := 0; i < reqValue.NumField(); i++ {
		field := reqValue.Field(i)
		if field.Kind() == reflect.Ptr && !field.IsNil() {
			if err := setErrorFallbacksOnValue(field.Interface(), normalized); err != nil {
				return err
			}
		}
	}
	return nil
}

// extractFallbacksFromRequest uses reflection to extract fallbacks field from any request type
func (g *GenericRouter) extractFallbacksFromRequest(req interface{}) ([]string, error) {
	if req == nil {
		return nil, nil
	}

	// Try to use reflection to find a fallbacks field.
	reqValue := reflect.ValueOf(req)
	if reqValue.Kind() == reflect.Ptr {
		reqValue = reqValue.Elem()
	}

	if reqValue.Kind() != reflect.Struct {
		return nil, nil // Not a struct, no fallbacks
	}

	fallbacksField := reqValue.FieldByName("Fallbacks")
	if !fallbacksField.IsValid() {
		// Some integrations may expose the field under a different Go name, so
		// fall back to the JSON wire name used in request payloads.
		reqType := reqValue.Type()
		for i := 0; i < reqValue.NumField(); i++ {
			field := reqType.Field(i)
			jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
			if jsonName == "fallbacks" {
				fallbacksField = reqValue.Field(i)
				break
			}
		}
	}
	if !fallbacksField.IsValid() {
		return nil, nil
	}

	// Handle different types of fallbacks field
	switch fallbacksField.Kind() {
	case reflect.Slice:
		if fallbacksField.Type().Elem().Kind() == reflect.String {
			// []string case
			fallbacks := make([]string, fallbacksField.Len())
			for i := 0; i < fallbacksField.Len(); i++ {
				fallbacks[i] = fallbacksField.Index(i).String()
			}
			return fallbacks, nil
		}
	case reflect.String:
		// Single string case - treat as one fallback
		return []string{fallbacksField.String()}, nil
	}

	return nil, nil
}

// getVirtualKeyFromBifrostContext extracts the virtual key value from bifrost context.
// Returns nil if no VK is present (e.g., direct key mode or no governance).
func getVirtualKeyFromBifrostContext(ctx *schemas.BifrostContext) *string {
	vkValue := bifrost.GetStringFromContext(ctx, schemas.BifrostContextKeyVirtualKey)
	if vkValue == "" {
		return nil
	}
	return &vkValue
}

// getResultTTLFromHeaderWithDefault extracts the result TTL from the x-bf-async-job-result-ttl header.
// Returns the default TTL if the header is not present or invalid.
func getResultTTLFromHeaderWithDefault(ctx *fasthttp.RequestCtx, defaultTTL int) int {
	resultTTL := string(ctx.Request.Header.Peek(schemas.AsyncHeaderResultTTL))
	if resultTTL == "" {
		return defaultTTL
	}
	resultTTLInt, err := strconv.Atoi(resultTTL)
	if err != nil || resultTTLInt < 0 {
		return defaultTTL
	}
	return resultTTLInt
}

// isAnthropicAPIKeyAuth checks if the request uses standard API key authentication.
// Returns true for API key auth (x-api-key header), false for OAuth (Bearer sk-ant-oat*).
// This is required for Claude Code specifically, which may use OAuth authentication.
// Default behavior is to assume API mode when neither x-api-key nor OAuth token is present.
func isAnthropicAPIKeyAuth(ctx *fasthttp.RequestCtx) bool {
	// If x-api-key header is present - this is definitely API mode
	if apiKey := string(ctx.Request.Header.Peek("x-api-key")); apiKey != "" {
		return true
	}
	// Check for OAuth token in Authorization header
	if authHeader := string(ctx.Request.Header.Peek("Authorization")); authHeader != "" {
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer sk-ant-oat") {
			return false // OAuth mode, NOT API
		}
	}
	// Default to API mode
	return true
}

// resolveLargePayloadMetadata returns metadata from the sync context key,
// falling back to a non-blocking read from the deferred channel.
// If deferred metadata is resolved, it is cached in the sync key for later readers.
func resolveLargePayloadMetadata(bifrostCtx *schemas.BifrostContext) *schemas.LargePayloadMetadata {
	if bifrostCtx == nil {
		return nil
	}
	if metadata, ok := bifrostCtx.Value(schemas.BifrostContextKeyLargePayloadMetadata).(*schemas.LargePayloadMetadata); ok && metadata != nil {
		return metadata
	}
	ch, ok := bifrostCtx.Value(schemas.BifrostContextKeyDeferredLargePayloadMetadata).(<-chan *schemas.LargePayloadMetadata)
	if !ok || ch == nil {
		return nil
	}
	select {
	case metadata := <-ch:
		if metadata != nil {
			bifrostCtx.SetValue(schemas.BifrostContextKeyLargePayloadMetadata, metadata)
		}
		return metadata
	default:
		return nil
	}
}

// ParseProviderScopedVideoID parses a provider-scoped video ID in the form "id:provider".
// The ID portion is automatically URL-decoded to restore the original ID.
func ParseProviderScopedVideoID(videoID string) (schemas.ModelProvider, string, error) {
	parts := strings.SplitN(videoID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("video_id must be in id:provider format")
	}
	provider := schemas.ModelProvider(parts[1])
	rawID := parts[0]

	// URL decode the ID to restore original characters (e.g., %2F -> /)
	// This handles IDs from all providers that may contain special characters
	if decoded, err := url.PathUnescape(rawID); err == nil {
		rawID = decoded
	}

	return provider, rawID, nil
}

func getProviderFromHeader(ctx *fasthttp.RequestCtx, defaultProvider schemas.ModelProvider) schemas.ModelProvider {
	providerHeader := string(ctx.Request.Header.Peek("x-model-provider"))
	if providerHeader == "" {
		return defaultProvider
	}
	return schemas.ModelProvider(providerHeader)
}

func RegisterKVDecoders(store *kvstore.Store) {
	store.RegisterDecoder("genai_upload_session:", func(data []byte) (any, error) {
		var v gemini.GeminiResumableUploadSession
		return &v, sonic.Unmarshal(data, &v)
	})
}
