package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecognizeFailureCategories(t *testing.T) {
	tests := []struct {
		name        string
		status      *int
		typeValue   string
		code        string
		message     string
		want        schemas.FailureCategory
		wantPattern string
	}{
		{name: "rate limit", status: Ptr(429), want: schemas.FailureCategoryRateLimit},
		{name: "authentication", status: Ptr(401), want: schemas.FailureCategoryAuthentication},
		{name: "billing", typeValue: "insufficient_quota", want: schemas.FailureCategoryBilling},
		{name: "permission", status: Ptr(403), want: schemas.FailureCategoryPermission},
		{name: "timeout", message: "upstream deadline exceeded", want: schemas.FailureCategoryTimeout},
		{name: "network", message: schemas.ErrProviderNetworkError, want: schemas.FailureCategoryNetwork},
		{name: "provider unavailable", status: Ptr(503), want: schemas.FailureCategoryProviderUnavailable},
		{name: "other 5xx", status: Ptr(500), want: schemas.FailureCategoryInternal},
		{name: "unsupported", message: "operation is not supported by this model", want: schemas.FailureCategoryUnsupportedOperation},
		{name: "content policy", code: "safety_violations", want: schemas.FailureCategoryContentPolicy},
		{
			name:        "content policy chinese provider message",
			status:      Ptr(400),
			typeValue:   "invalid_request_error",
			code:        "bad_request",
			message:     "非常抱歉，生成的图片可能违反了我们的内容政策。如果你认为此判断有误，请重试或修改提示语。",
			want:        schemas.FailureCategoryContentPolicy,
			wantPattern: "content_policy",
		},
		{name: "invalid image response", typeValue: "invalid_image_response", want: schemas.FailureCategoryProviderUnavailable, wantPattern: "invalid_image_response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var typ, code *string
			if tt.typeValue != "" {
				typ = Ptr(tt.typeValue)
			}
			if tt.code != "" {
				code = Ptr(tt.code)
			}
			err := &schemas.BifrostError{StatusCode: tt.status, Type: typ, Error: &schemas.ErrorField{Type: typ, Code: code, Message: tt.message}}
			got := RecognizeFailure(FailureSignal{Provider: schemas.OpenAI, RequestType: schemas.ImageGenerationRequest, Error: err})
			assert.Equal(t, tt.want, got.Category)
			if tt.wantPattern != "" {
				assert.Equal(t, tt.wantPattern, got.PatternID)
			}
		})
	}
}

func TestFirstMatchingErrorFallbackRuleUsesFirstMatch(t *testing.T) {
	req := errorFallbackChatRequest()
	req.ChatRequest.ErrorFallbacks = []schemas.ErrorFallbackRule{
		{Name: "first", Scenario: schemas.FailureCategoryRateLimit, Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "first"}}},
		{Name: "second", Scenario: schemas.FailureCategoryRateLimit, Fallbacks: []schemas.Fallback{{Provider: schemas.Azure, Model: "second"}}},
	}
	rule, failure := firstMatchingErrorFallbackRule(req, testFallbackError(429, "rate_limit_error", "limited"), req.GetErrorFallbacks())
	require.NotNil(t, rule)
	assert.Equal(t, "first", rule.Name)
	assert.Equal(t, schemas.FailureCategoryRateLimit, failure.category)
}

func TestSuccessfulRequestNeverMatchesUnknownErrorFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"success","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer primary.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	client := newStreamTestClient(t, account)

	tests := []struct {
		name string
		rule schemas.ErrorFallbackRule
	}{
		{
			name: "scenario unknown",
			rule: schemas.ErrorFallbackRule{
				Scenario:  schemas.FailureCategoryUnknown,
				Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "unused"}},
			},
		},
		{
			name: "legacy unknown category",
			rule: schemas.ErrorFallbackRule{
				When:      schemas.ErrorFallbackCondition{Categories: []schemas.FailureCategory{schemas.FailureCategoryUnknown}},
				Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "unused"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
			response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
				Provider:       schemas.OpenAI,
				Model:          "gpt-4o-mini",
				Input:          []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
				ErrorFallbacks: []schemas.ErrorFallbackRule{tt.rule},
			})

			require.Nil(t, bifrostErr)
			require.NotNil(t, response)
			_, recorded := ctx.Value(schemas.BifrostContextKeyErrorFallbackRuleName).(string)
			assert.False(t, recorded)
		})
	}
}

func TestErrorFallbackSupplementAndLegacyWhen(t *testing.T) {
	req := errorFallbackChatRequest()
	err := testFallbackError(418, "vendor_policy", "custom safety rejection")
	err.Error.Code = Ptr("vendor_safety")

	supplement := schemas.ErrorFallbackRule{
		Name:       "supplement",
		Supplement: &schemas.ErrorFallbackSupplement{Providers: []schemas.ModelProvider{schemas.OpenAI}, ErrorCodes: []string{"vendor_safety"}},
		Fallbacks:  []schemas.Fallback{{Provider: schemas.Anthropic, Model: "safe"}},
	}
	match, ok := matchErrorFallbackRule(classifyBifrostFailure(err, schemas.OpenAI, req.RequestType), supplement)
	require.True(t, ok)
	assert.Equal(t, failureMatchedBySupplement, match.matchedBy)

	legacy := schemas.ErrorFallbackRule{
		When:      schemas.ErrorFallbackCondition{StatusCodes: []int{418}, ErrorTypes: []string{"vendor_policy"}, MessageContains: []string{"safety"}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.Azure, Model: "safe"}},
	}
	match, ok = matchErrorFallbackRule(classifyBifrostFailure(err, schemas.OpenAI, req.RequestType), legacy)
	require.True(t, ok)
	assert.Equal(t, failureMatchedByLegacyWhen, match.matchedBy)

	legacy.When.StatusCodes = []int{400}
	_, ok = matchErrorFallbackRule(classifyBifrostFailure(err, schemas.OpenAI, req.RequestType), legacy)
	assert.False(t, ok, "populated legacy fields are ANDed")
}

func TestContentPolicySupplementMatchesProviderSpecificMessageWithoutCodeChange(t *testing.T) {
	provider := schemas.ModelProvider("custom-image-provider")
	err := testFallbackError(400, "invalid_request_error", "vendor moderation gate 781 rejected this image")
	err.Error.Code = Ptr("bad_request")
	rule := schemas.ErrorFallbackRule{
		Scenario: schemas.FailureCategoryContentPolicy,
		Supplement: &schemas.ErrorFallbackSupplement{
			Providers:          []schemas.ModelProvider{provider},
			MessageContainsAny: []string{"moderation gate 781"},
		},
		Fallbacks: []schemas.Fallback{{Provider: schemas.OpenAI, Model: "safe"}},
	}

	match, ok := matchErrorFallbackRule(classifyBifrostFailure(err, provider, schemas.ImageGenerationRequest), rule)
	require.True(t, ok)
	assert.Equal(t, failureMatchedBySupplement, match.matchedBy)

	_, ok = matchErrorFallbackRule(classifyBifrostFailure(err, schemas.OpenAI, schemas.ImageGenerationRequest), rule)
	assert.False(t, ok, "provider-scoped message clues must not affect other providers")
}

func TestResolveFallbackChainDedicatedReplacesOrdinaryAndDeduplicates(t *testing.T) {
	req := errorFallbackChatRequest()
	req.ChatRequest.Fallbacks = []schemas.Fallback{
		{Provider: schemas.Anthropic, Model: "ordinary"},
		{Provider: schemas.Anthropic, Model: "ordinary"},
	}
	req.ChatRequest.ErrorFallbacks = []schemas.ErrorFallbackRule{{
		Name:     "rate-limit",
		Scenario: schemas.FailureCategoryRateLimit,
		Fallbacks: []schemas.Fallback{
			{Provider: schemas.OpenAI, Model: "primary"},
			{Provider: schemas.Azure, Model: "dedicated"},
			{Provider: schemas.Azure, Model: "dedicated"},
		},
	}}
	client := &Bifrost{}
	selected, ordinary, rule, _ := client.resolveFallbackChain(req, testFallbackError(429, "rate_limit_error", "limited"))
	require.NotNil(t, rule)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.Azure, Model: "dedicated"}}, selected)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}, ordinary)
}

func TestContentSafetyErrorFallbackLeavesOtherErrorsOnOrdinaryChain(t *testing.T) {
	req := errorFallbackChatRequest()
	req.ChatRequest.Fallbacks = []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}
	req.ChatRequest.ErrorFallbacks = []schemas.ErrorFallbackRule{{
		Name:      "content-safety",
		Scenario:  schemas.FailureCategoryContentPolicy,
		Fallbacks: []schemas.Fallback{{Provider: schemas.Azure, Model: "safe"}},
	}}

	client := &Bifrost{}
	selected, ordinary, rule, failure := client.resolveFallbackChain(req, testFallbackError(429, "rate_limit_error", "limited"))

	require.Nil(t, rule)
	assert.Equal(t, schemas.FailureCategoryRateLimit, failure.category)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}, selected)
	assert.Equal(t, ordinary, selected)
}

func TestCaptureFailureSignalsSurvivesRawResponseDrop(t *testing.T) {
	err := testFallbackError(400, "provider_error", "request failed")
	err.ExtraFields.RawResponse = []byte(`{"error":{"code":"safety_violations","type":"policy_error","message":"blocked by policy"}}`)
	captureFailureRecognitionSignals(err)
	err.ExtraFields.RawResponse = nil

	got := RecognizeFailure(FailureSignal{Provider: schemas.OpenAI, RequestType: schemas.ImageGenerationRequest, Error: err})
	assert.Equal(t, schemas.FailureCategoryContentPolicy, got.Category)
	assert.NotEmpty(t, err.ExtraFields.FailureSignals.ErrorCodes)
}

func TestSuccessfulContentPolicyResponseBecomesFallbackEligibleError(t *testing.T) {
	req := errorFallbackChatRequest()
	req.ChatRequest.ErrorFallbacks = []schemas.ErrorFallbackRule{{Scenario: schemas.FailureCategoryContentPolicy, Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "safe"}}}}
	response := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Choices: []schemas.BifrostResponseChoice{{FinishReason: Ptr("content_filter")}}}}

	err := successfulContentPolicyError(req, response)
	require.NotNil(t, err)
	require.NotNil(t, err.AllowFallbacks)
	assert.True(t, *err.AllowFallbacks)
}

func TestStreamFallbackSelectionBoundary(t *testing.T) {
	// handleStreamRequest can select a dedicated chain for an error returned by
	// tryStreamRequest before a channel escapes. Once a non-nil channel is returned,
	// later errors are delivered in-band and are intentionally not replayed here.
	req := errorFallbackChatRequest()
	req.RequestType = schemas.ChatCompletionStreamRequest
	req.ChatRequest.ErrorFallbacks = []schemas.ErrorFallbackRule{{Scenario: schemas.FailureCategoryRateLimit, Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "stream-fallback"}}}}
	selected, _, rule, _ := (&Bifrost{}).resolveFallbackChain(req, testFallbackError(429, "rate_limit_error", "limited before first chunk"))
	require.NotNil(t, rule)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.Anthropic, Model: "stream-fallback"}}, selected)
}

func TestRecordErrorFallbackDecisionClearsStaleState(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackRuleName, "stale")
	recordErrorFallbackDecision(ctx, nil, classifiedFailure{})
	_, ok := ctx.Value(schemas.BifrostContextKeyErrorFallbackRuleName).(string)
	assert.False(t, ok)
}

func TestShouldContinueWithFallbacksIsNilSafe(t *testing.T) {
	client := &Bifrost{logger: NewDefaultLogger(schemas.LogLevelError)}
	assert.False(t, client.shouldContinueWithFallbacks(schemas.Fallback{}, nil))
	assert.True(t, client.shouldContinueWithFallbacks(schemas.Fallback{}, &schemas.BifrostError{Error: nil}))
}

func TestHandleRequestUsesDedicatedChainInsteadOfOrdinaryFallbacks(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer primary.Close()

	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ordinaryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ordinary","choices":[{"message":{"role":"assistant","content":"ordinary"},"finish_reason":"stop"}]}`)
	}))
	defer ordinary.Close()

	var dedicatedHits atomic.Int32
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dedicatedHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"dedicated","choices":[{"message":{"role":"assistant","content":"dedicated"},"finish_reason":"stop"}]}`)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, ordinary.URL)
	configureFallbackTestProvider(account, schemas.Groq, dedicated.URL)
	client := newStreamTestClient(t, account)

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary-model"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "rate-limit-dedicated", Scenario: schemas.FailureCategoryRateLimit,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "dedicated-model"}},
		}},
	})
	require.Nil(t, bifrostErr)
	require.NotNil(t, response)
	assert.Zero(t, ordinaryHits.Load())
	assert.Equal(t, int32(1), dedicatedHits.Load())
	assert.True(t, response.ExtraFields.RoutingInfo.IsFallback)
	assert.Equal(t, "rate-limit-dedicated", GetStringFromContext(ctx, schemas.BifrostContextKeyErrorFallbackRuleName))
}

func TestHandleStreamRequestUsesDedicatedChainForPreReturnError(t *testing.T) {
	primary := httptest.NewServer(sseHandler(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	defer primary.Close()
	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		sseHandler(`{"id":"ordinary","choices":[{"index":0,"delta":{"content":"ordinary"},"finish_reason":"stop"}]}`)(w, r)
	}))
	defer ordinary.Close()
	var dedicatedHits atomic.Int32
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dedicatedHits.Add(1)
		anthropicMessagesHandler()(w, r)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, ordinary.URL)
	configureFallbackTestProvider(account, schemas.Anthropic, dedicated.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	stream, bifrostErr := client.ChatCompletionStreamRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary-model"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "stream-rate-limit", Scenario: schemas.FailureCategoryRateLimit,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "claude-3-5-haiku-20241022"}},
		}},
	})
	require.Nil(t, bifrostErr)
	content, streamErrors := drainChatStream(stream)
	assert.Equal(t, "hello", content)
	assert.Empty(t, streamErrors)
	assert.Zero(t, ordinaryHits.Load())
	assert.Equal(t, int32(1), dedicatedHits.Load())
}

func TestHandleRequestFallbackFailureActivatesDedicatedChainAndDropsRemainingOrdinary(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"temporary internal failure","type":"server_error"}}`)
	}))
	defer primary.Close()
	firstOrdinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer firstOrdinary.Close()
	var skippedOrdinaryHits atomic.Int32
	skippedOrdinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		skippedOrdinaryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"skipped","choices":[{"message":{"role":"assistant","content":"skipped"},"finish_reason":"stop"}]}`)
	}))
	defer skippedOrdinary.Close()
	var dedicatedHits atomic.Int32
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dedicatedHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"dedicated","choices":[{"message":{"role":"assistant","content":"dedicated"},"finish_reason":"stop"}]}`)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, firstOrdinary.URL)
	configureFallbackTestProvider(account, schemas.Cerebras, skippedOrdinary.URL)
	configureFallbackTestProvider(account, schemas.Groq, dedicated.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input:    []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{
			{Provider: schemas.XAI, Model: "ordinary-one"},
			{Provider: schemas.Cerebras, Model: "ordinary-two"},
		},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "fallback-rate-limit", Scenario: schemas.FailureCategoryRateLimit,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "dedicated-model"}},
		}},
	})
	require.Nil(t, bifrostErr)
	require.NotNil(t, response)
	assert.Zero(t, skippedOrdinaryHits.Load())
	assert.Equal(t, int32(1), dedicatedHits.Load())
	assert.Equal(t, "fallback-rate-limit", GetStringFromContext(ctx, schemas.BifrostContextKeyErrorFallbackRuleName))
}

func configureFallbackTestProvider(account *MockAccount, provider schemas.ModelProvider, baseURL string) {
	account.AddProviderWithBaseURL(provider, 1, 1, baseURL)
	account.configs[provider].NetworkConfig.MaxRetries = 0
	account.SetKeysForProvider(provider, []schemas.Key{{
		ID:     string(provider) + "-key",
		Value:  *schemas.NewSecretVar("sk-test"),
		Models: schemas.WhiteList{"*"},
		Weight: 100,
	}})
}

func errorFallbackChatRequest() *schemas.BifrostRequest {
	return &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "primary"},
	}
}

func testFallbackError(status int, errorType, message string) *schemas.BifrostError {
	allow := true
	return &schemas.BifrostError{StatusCode: &status, Type: Ptr(errorType), AllowFallbacks: &allow, Error: &schemas.ErrorField{Type: Ptr(errorType), Message: message}}
}
