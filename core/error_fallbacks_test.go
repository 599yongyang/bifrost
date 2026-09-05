package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
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
		{name: "APIMart content moderation", status: Ptr(400), typeValue: "content_policy", code: "content_moderation", want: schemas.FailureCategoryContentPolicy, wantPattern: "content_policy"},
		{
			name:        "content policy chinese provider message",
			status:      Ptr(400),
			typeValue:   "invalid_request_error",
			code:        "bad_request",
			message:     "非常抱歉，生成的图片可能违反了我们的内容政策。如果你认为此判断有误，请重试或修改提示语。",
			want:        schemas.FailureCategoryContentPolicy,
			wantPattern: "content_policy",
		},
		{
			name:        "content policy chinese nudity protection message",
			status:      Ptr(400),
			typeValue:   "invalid_request_error",
			code:        "bad_request",
			message:     "非常抱歉，该提示可能违反了关于裸露、色情或情色内容的防护限制。如果你认为此判断有误，请重试或修改提示语。",
			want:        schemas.FailureCategoryContentPolicy,
			wantPattern: "sexual_content_protection",
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

func TestBuiltInContentSafetySignalsExposeEveryClassifierPath(t *testing.T) {
	catalog := BuiltInContentSafetySignals()
	assert.ElementsMatch(t, contentPolicyStructuredSignals, catalog.Structured)
	assert.ElementsMatch(t, contentPolicyFinishReasons, catalog.FinishReasons)
	assert.Contains(t, catalog.FinishReasons, "contentfilter")
	assert.Contains(t, catalog.FinishReasons, "image_safety")
	assert.Contains(t, catalog.Messages, "generated images appear to be unsafe")
	assert.Contains(t, catalog.Messages, "裸露、色情或情色内容")

	catalog.Structured[0] = "mutated"
	assert.NotEqual(t, "mutated", BuiltInContentSafetySignals().Structured[0], "callers must not mutate the runtime catalog")
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

	unscopedRule := rule
	unscopedSupplement := *rule.Supplement
	unscopedSupplement.Providers = nil
	unscopedRule.Supplement = &unscopedSupplement
	_, ok = matchErrorFallbackRule(classifyBifrostFailure(err, schemas.OpenAI, schemas.ImageGenerationRequest), unscopedRule)
	assert.True(t, ok, "custom clues without a provider scope must apply to every provider")

	builtInErr := testFallbackError(400, "content_filter", "blocked by content policy")
	_, ok = matchErrorFallbackRule(classifyBifrostFailure(builtInErr, schemas.OpenAI, schemas.ImageGenerationRequest), rule)
	assert.True(t, ok, "provider scopes must not restrict built-in content-safety recognition")
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

func TestContentSafetyWithoutDedicatedRuleBlocksOrdinaryFallbacks(t *testing.T) {
	req := errorFallbackChatRequest()
	req.ChatRequest.Fallbacks = []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}

	selected, ordinary, rule, failure := (&Bifrost{}).resolveFallbackChain(req, testFallbackError(400, "content_filter", "blocked by content policy"))

	require.Nil(t, rule)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, failure.category)
	assert.Empty(t, selected)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}, ordinary)
}

func TestContentSafetyWithDedicatedRuleUsesOnlyDedicatedFallbacks(t *testing.T) {
	req := errorFallbackChatRequest()
	req.ChatRequest.Fallbacks = []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}
	req.ChatRequest.ErrorFallbacks = []schemas.ErrorFallbackRule{{
		Name:      "content-safety",
		Scenario:  schemas.FailureCategoryContentPolicy,
		Fallbacks: []schemas.Fallback{{Provider: schemas.Azure, Model: "safe"}},
	}}

	selected, ordinary, rule, failure := (&Bifrost{}).resolveFallbackChain(req, testFallbackError(400, "content_filter", "blocked by content policy"))

	require.NotNil(t, rule)
	assert.Equal(t, "content-safety", rule.Name)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, failure.category)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.Azure, Model: "safe"}}, selected)
	assert.Equal(t, []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}, ordinary)
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

func TestSuccessfulContentPolicyResponseBecomesErrorWithoutDedicatedFallback(t *testing.T) {
	req := errorFallbackChatRequest()
	response := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Choices: []schemas.BifrostResponseChoice{{FinishReason: Ptr("content_filter")}}}}

	err := successfulContentPolicyError(req, response)
	require.NotNil(t, err)
	require.NotNil(t, err.Type)
	assert.Equal(t, "content_filter", *err.Type)
}

func TestSuccessfulContentPolicyResponseRecognizesGuardrailFinishReason(t *testing.T) {
	req := errorFallbackChatRequest()
	response := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Choices: []schemas.BifrostResponseChoice{{FinishReason: Ptr("guardrail_intervened")}}}}

	err := successfulContentPolicyError(req, response)
	require.NotNil(t, err)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: err}).Category)
}

func TestTextStreamWhitespaceCountsAsVisibleOutput(t *testing.T) {
	whitespace := "\n"
	chunk := &schemas.BifrostStreamChunk{BifrostTextCompletionResponse: &schemas.BifrostTextCompletionResponse{
		Choices: []schemas.BifrostResponseChoice{{TextCompletionResponseChoice: &schemas.TextCompletionResponseChoice{Text: &whitespace}}},
	}}

	assert.True(t, streamChunkHasVisibleOutput(chunk))
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

func TestContentPolicyHaltClearsPreviousDedicatedRuleMetadata(t *testing.T) {
	req := errorFallbackChatRequest()
	req.ChatRequest.ErrorFallbacks = []schemas.ErrorFallbackRule{{
		Name:      "rate-limit",
		Scenario:  schemas.FailureCategoryRateLimit,
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "rate-fallback"}},
	}}
	rateFailure := classifiedFailure{category: schemas.FailureCategoryRateLimit}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	recordErrorFallbackDecision(ctx, &req.ChatRequest.ErrorFallbacks[0], rateFailure)
	state := newFallbackChainState(schemas.OpenAI, "gpt-4o-mini", req.ChatRequest.ErrorFallbacks[0].Fallbacks, &req.ChatRequest.ErrorFallbacks[0], rateFailure, testFallbackError(429, "rate_limit_error", "limited"))
	fallbackErr := testFallbackError(400, "content_filter", "blocked by content policy")
	decision := state.decideFailure(req, req, fallbackErr, 0, true)

	client := &Bifrost{logger: NewDefaultLogger(schemas.LogLevelError)}
	halted := client.applyFallbackFailureDecision(ctx, &schemas.NoOpTracer{}, nil, schemas.Fallback{Provider: schemas.XAI, Model: "rate-fallback"}, fallbackErr, decision)
	require.True(t, halted)
	assert.Empty(t, GetStringFromContext(ctx, schemas.BifrostContextKeyErrorFallbackRuleName))
	assert.Equal(t, string(schemas.FailureCategoryContentPolicy), GetStringFromContext(ctx, schemas.BifrostContextKeyErrorFallbackCategory))
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

func TestHandleRequestContentPolicyWithoutDedicatedRuleSkipsOrdinaryFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer primary.Close()
	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ordinaryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ordinary","choices":[{"message":{"role":"assistant","content":"ordinary"},"finish_reason":"stop"}]}`)
	}))
	defer ordinary.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, ordinary.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary-model"}},
	})

	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	assert.Zero(t, ordinaryHits.Load())
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
}

func TestHandleRequestRecognitionOnlyContentSafetyRuleSkipsOrdinaryFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"vendor moderation gate rejected this prompt","type":"invalid_request_error","code":"bad_request"}}`)
	}))
	defer primary.Close()
	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ordinaryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ordinary","choices":[{"message":{"role":"assistant","content":"ordinary"},"finish_reason":"stop"}]}`)
	}))
	defer ordinary.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, ordinary.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary-model"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name:     "custom-recognition",
			Scenario: schemas.FailureCategoryContentPolicy,
			Supplement: &schemas.ErrorFallbackSupplement{
				MessageContainsAny: []string{"vendor moderation gate"},
			},
			Fallbacks: nil,
		}},
	})

	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	assert.Contains(t, bifrostErr.GetErrorString(), "vendor moderation gate")
	assert.Zero(t, ordinaryHits.Load())
	assert.Equal(t, string(schemas.FailureCategoryContentPolicy), GetStringFromContext(ctx, schemas.BifrostContextKeyErrorFallbackCategory))
}

func TestHandleRequestContentPolicyDuringOrdinaryChainStopsRemainingFallbacks(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"temporary internal failure","type":"server_error"}}`)
	}))
	defer primary.Close()
	firstOrdinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"generated image blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer firstOrdinary.Close()
	var skippedOrdinaryHits atomic.Int32
	skippedOrdinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		skippedOrdinaryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"skipped","choices":[{"message":{"role":"assistant","content":"skipped"},"finish_reason":"stop"}]}`)
	}))
	defer skippedOrdinary.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, firstOrdinary.URL)
	configureFallbackTestProvider(account, schemas.Cerebras, skippedOrdinary.URL)
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
	})

	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	assert.Zero(t, skippedOrdinaryHits.Load())
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
}

func TestHandleRequestContentPolicyDuringOrdinaryChainReturnsSafetyErrorAfterDedicatedChainExhausts(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"temporary internal failure","type":"server_error"}}`)
	}))
	defer primary.Close()
	contentBlocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"generated image blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer contentBlocked.Close()
	dedicatedFailure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"safety fallback unavailable","type":"server_error"}}`)
	}))
	defer dedicatedFailure.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, contentBlocked.URL)
	configureFallbackTestProvider(account, schemas.Groq, dedicatedFailure.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary-one"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "safe-model"}},
		}},
	})

	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
	assert.Contains(t, bifrostErr.GetErrorString(), "content policy")
}

func TestHandleRequestContentPolicyOverridesActiveNonSafetyDedicatedChain(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer primary.Close()
	contentBlocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer contentBlocked.Close()
	var staleDedicatedHits atomic.Int32
	staleDedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		staleDedicatedHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"stale","choices":[{"message":{"role":"assistant","content":"stale"},"finish_reason":"stop"}]}`)
	}))
	defer staleDedicated.Close()
	var safetyHits atomic.Int32
	safety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		safetyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"safe","choices":[{"message":{"role":"assistant","content":"safe"},"finish_reason":"stop"}]}`)
	}))
	defer safety.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, contentBlocked.URL)
	configureFallbackTestProvider(account, schemas.Cerebras, staleDedicated.URL)
	configureFallbackTestProvider(account, schemas.Groq, safety.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input:    []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{
			{Name: "rate-limit", Scenario: schemas.FailureCategoryRateLimit, Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "rate-one"}, {Provider: schemas.Cerebras, Model: "rate-two"}}},
			{Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy, Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "safe"}}},
		},
	})

	require.Nil(t, bifrostErr)
	require.NotNil(t, response)
	assert.Zero(t, staleDedicatedHits.Load())
	assert.Equal(t, int32(1), safetyHits.Load())
	assert.Equal(t, "content-safety", GetStringFromContext(ctx, schemas.BifrostContextKeyErrorFallbackRuleName))
}

type disableFallbacksOnErrorPlugin struct {
	messageContains string
}

func (*disableFallbacksOnErrorPlugin) GetName() string { return "disable-fallbacks-on-error" }
func (*disableFallbacksOnErrorPlugin) Cleanup() error  { return nil }
func (*disableFallbacksOnErrorPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (*disableFallbacksOnErrorPlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (plugin *disableFallbacksOnErrorPlugin) PostLLMHook(_ *schemas.BifrostContext, response *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	shouldDisable := bifrostErr != nil && ((plugin.messageContains == "" && RecognizeFailure(FailureSignal{Error: bifrostErr}).Category == schemas.FailureCategoryContentPolicy) ||
		(plugin.messageContains != "" && strings.Contains(bifrostErr.GetErrorString(), plugin.messageContains)))
	if shouldDisable {
		bifrostErr.AllowFallbacks = Ptr(false)
	}
	return response, bifrostErr, nil
}

func TestHandleRequestContentSafetyRespectsAllowFallbacksFalse(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer primary.Close()
	var safetyHits atomic.Int32
	safety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		safetyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"safe","choices":[{"message":{"role":"assistant","content":"safe"},"finish_reason":"stop"}]}`)
	}))
	defer safety.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.Groq, safety.URL)
	client, err := Init(context.Background(), schemas.BifrostConfig{
		Account:    account,
		Logger:     NewDefaultLogger(schemas.LogLevelError),
		LLMPlugins: []schemas.LLMPlugin{&disableFallbacksOnErrorPlugin{}},
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input:    []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "safe"}},
		}},
	})

	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	assert.Zero(t, safetyHits.Load())
}

func TestHandleRequestContentSafetySkipsSameProviderRetries(t *testing.T) {
	var primaryHits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer primary.Close()
	var safetyHits atomic.Int32
	safety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		safetyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"safe","choices":[{"message":{"role":"assistant","content":"safe"},"finish_reason":"stop"}]}`)
	}))
	defer safety.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.Groq, safety.URL)
	account.configs[schemas.OpenAI].NetworkConfig.MaxRetries = 3
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input:    []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "safe"}},
		}},
	})

	require.Nil(t, bifrostErr)
	require.NotNil(t, response)
	assert.Equal(t, int32(1), primaryHits.Load())
	assert.Equal(t, int32(1), safetyHits.Load())
}

func TestHandleRequestContentSafetyDuringOrdinaryChainRespectsAllowFallbacksFalse(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"temporary internal failure","type":"server_error"}}`)
	}))
	defer primary.Close()
	contentBlocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer contentBlocked.Close()
	var safetyHits atomic.Int32
	safety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		safetyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"safe","choices":[{"message":{"role":"assistant","content":"safe"},"finish_reason":"stop"}]}`)
	}))
	defer safety.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, contentBlocked.URL)
	configureFallbackTestProvider(account, schemas.Groq, safety.URL)
	client, err := Init(context.Background(), schemas.BifrostConfig{
		Account:    account,
		Logger:     NewDefaultLogger(schemas.LogLevelError),
		LLMPlugins: []schemas.LLMPlugin{&disableFallbacksOnErrorPlugin{}},
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "safe"}},
		}},
	})

	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	assert.Zero(t, safetyHits.Load())
}

func TestHandleRequestContentSafetyChainStopsOnFallbackVeto(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`)
	}))
	defer primary.Close()
	firstSafety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"safety target unavailable","type":"server_error"}}`)
	}))
	defer firstSafety.Close()
	var skippedSafetyHits atomic.Int32
	skippedSafety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		skippedSafetyHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"safe","choices":[{"message":{"role":"assistant","content":"safe"},"finish_reason":"stop"}]}`)
	}))
	defer skippedSafety.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, firstSafety.URL)
	configureFallbackTestProvider(account, schemas.Groq, skippedSafety.URL)
	client, err := Init(context.Background(), schemas.BifrostConfig{
		Account:    account,
		Logger:     NewDefaultLogger(schemas.LogLevelError),
		LLMPlugins: []schemas.LLMPlugin{&disableFallbacksOnErrorPlugin{messageContains: "safety target unavailable"}},
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	response, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input:    []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "safe-one"}, {Provider: schemas.Groq, Model: "safe-two"}},
		}},
	})

	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	assert.Contains(t, bifrostErr.GetErrorString(), "safety target unavailable")
	assert.Zero(t, skippedSafetyHits.Load())
}

func TestHandleStreamRequestContentPolicyWithoutDedicatedRuleSkipsOrdinaryFallback(t *testing.T) {
	primary := httptest.NewServer(sseHandler(`{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`))
	defer primary.Close()
	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		sseHandler(`{"id":"ordinary","choices":[{"index":0,"delta":{"content":"ordinary"},"finish_reason":"stop"}]}`)(w, r)
	}))
	defer ordinary.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, ordinary.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	stream, bifrostErr := client.ChatCompletionStreamRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary-model"}},
	})

	require.Nil(t, stream)
	require.NotNil(t, bifrostErr)
	assert.Zero(t, ordinaryHits.Load())
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
}

func TestHandleStreamRequestContentPolicyDuringOrdinaryChainReturnsSafetyErrorAfterDedicatedChainExhausts(t *testing.T) {
	primary := httptest.NewServer(sseHandler(`{"error":{"message":"temporary internal failure","type":"server_error"}}`))
	defer primary.Close()
	contentBlocked := httptest.NewServer(sseHandler(`{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`))
	defer contentBlocked.Close()
	dedicatedFailure := httptest.NewServer(sseHandler(`{"error":{"message":"safety fallback unavailable","type":"server_error"}}`))
	defer dedicatedFailure.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, contentBlocked.URL)
	configureFallbackTestProvider(account, schemas.Cerebras, dedicatedFailure.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	stream, bifrostErr := client.ChatCompletionStreamRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "ordinary-one"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.Cerebras, Model: "safe-model"}},
		}},
	})

	require.Nil(t, stream)
	require.NotNil(t, bifrostErr)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
	assert.Contains(t, bifrostErr.GetErrorString(), "content policy")
}

func TestHandleStreamRequestContentPolicyOverridesActiveNonSafetyDedicatedChain(t *testing.T) {
	primary := httptest.NewServer(sseHandler(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	defer primary.Close()
	contentBlocked := httptest.NewServer(sseHandler(`{"error":{"message":"blocked by content policy","type":"content_filter","code":"content_filter"}}`))
	defer contentBlocked.Close()
	var staleDedicatedHits atomic.Int32
	staleDedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staleDedicatedHits.Add(1)
		sseHandler(`{"id":"stale","choices":[{"index":0,"delta":{"content":"stale"},"finish_reason":"stop"}]}`)(w, r)
	}))
	defer staleDedicated.Close()
	var safetyHits atomic.Int32
	safety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safetyHits.Add(1)
		anthropicMessagesHandler()(w, r)
	}))
	defer safety.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.XAI, contentBlocked.URL)
	configureFallbackTestProvider(account, schemas.Cerebras, staleDedicated.URL)
	configureFallbackTestProvider(account, schemas.Anthropic, safety.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	stream, bifrostErr := client.ChatCompletionStreamRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input:    []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{
			{Name: "rate-limit", Scenario: schemas.FailureCategoryRateLimit, Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "rate-one"}, {Provider: schemas.Cerebras, Model: "rate-two"}}},
			{Name: "content-safety", Scenario: schemas.FailureCategoryContentPolicy, Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "safe"}}},
		},
	})

	require.Nil(t, bifrostErr)
	content, streamErrors := drainChatStream(stream)
	assert.Equal(t, "hello", content)
	assert.Empty(t, streamErrors)
	assert.Zero(t, staleDedicatedHits.Load())
	assert.Equal(t, int32(1), safetyHits.Load())
	assert.Equal(t, "content-safety", GetStringFromContext(ctx, schemas.BifrostContextKeyErrorFallbackRuleName))
}

func TestHandleStreamRequestContentPolicyTerminalChunkWithoutDedicatedRuleReturnsError(t *testing.T) {
	primary := httptest.NewServer(sseHandler(
		`{"id":"blocked","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"blocked","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`,
	))
	defer primary.Close()
	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		anthropicMessagesHandler()(w, r)
	}))
	defer ordinary.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, primary.URL)
	configureFallbackTestProvider(account, schemas.Anthropic, ordinary.URL)
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	stream, bifrostErr := client.ChatCompletionStreamRequest(ctx, &schemas.BifrostChatRequest{
		Provider:  schemas.OpenAI,
		Model:     "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary-model"}},
	})

	if bifrostErr == nil {
		_, _ = drainChatStream(stream)
		t.Fatal("expected terminal content_filter chunk to become a synchronous content-policy error")
	}
	require.Nil(t, stream)
	assert.Zero(t, ordinaryHits.Load())
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
}

func TestHandleStreamRequestEarlyCustomSafetyErrorUsesDedicatedFallback(t *testing.T) {
	primary := httptest.NewServer(sseHandler(
		`{"id":"blocked","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"error":{"message":"moon-safety-block: prompt rejected","type":"provider_error"}}`,
	))
	defer primary.Close()
	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		anthropicMessagesHandler()(w, r)
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
			Name:     "content-safety",
			Scenario: schemas.FailureCategoryContentPolicy,
			Supplement: &schemas.ErrorFallbackSupplement{
				MessageContainsAny: []string{"moon-safety-block"},
			},
			Fallbacks: []schemas.Fallback{{Provider: schemas.Anthropic, Model: "safe-model"}},
		}},
	})

	require.Nil(t, bifrostErr)
	content, streamErrors := drainChatStream(stream)
	assert.Equal(t, "hello", content)
	assert.Empty(t, streamErrors)
	assert.Zero(t, ordinaryHits.Load())
	assert.Equal(t, int32(1), dedicatedHits.Load())
}

func TestInitialResponsesStreamContentPolicyBeforeOutputReturnsError(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 3)
	stream <- &schemas.BifrostStreamChunk{BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{Type: schemas.ResponsesStreamResponseTypeCreated}}
	stream <- &schemas.BifrostStreamChunk{BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{Type: schemas.ResponsesStreamResponseTypeInProgress}}
	stream <- &schemas.BifrostStreamChunk{BifrostResponsesStreamResponse: &schemas.BifrostResponsesStreamResponse{
		Type: schemas.ResponsesStreamResponseTypeIncomplete,
		Response: &schemas.BifrostResponsesResponse{
			IncompleteDetails: &schemas.ResponsesResponseIncompleteDetails{Reason: schemas.ResponsesResponseIncompleteReasonContentFilter},
		},
	}}
	close(stream)

	req := errorFallbackChatRequest()
	checked, done, bifrostErr := providerUtils.CheckFirstStreamChunkForError(context.Background(), schemas.ResponsesStreamRequest, stream, func(chunk *schemas.BifrostStreamChunk) (*schemas.BifrostError, bool) {
		return initialStreamError(req, chunk), streamChunkHasVisibleOutput(chunk)
	})
	<-done
	require.Nil(t, checked)
	require.NotNil(t, bifrostErr)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
}

func TestInitialImageStreamPreservesNestedContentPolicyError(t *testing.T) {
	safetyErr := testFallbackError(400, "content_filter", "generated image blocked by content policy")
	stream := make(chan *schemas.BifrostStreamChunk, 1)
	stream <- &schemas.BifrostStreamChunk{BifrostImageGenerationStreamResponse: &schemas.BifrostImageGenerationStreamResponse{
		Type:  schemas.ImageGenerationEventTypeCompleted,
		Error: safetyErr,
	}}
	close(stream)

	req := errorFallbackChatRequest()
	checked, done, bifrostErr := providerUtils.CheckFirstStreamChunkForError(context.Background(), schemas.ImageGenerationStreamRequest, stream, func(chunk *schemas.BifrostStreamChunk) (*schemas.BifrostError, bool) {
		return initialStreamError(req, chunk), streamChunkHasVisibleOutput(chunk)
	})
	<-done
	require.Nil(t, checked)
	require.Same(t, safetyErr, bifrostErr)
	assert.Equal(t, schemas.FailureCategoryContentPolicy, RecognizeFailure(FailureSignal{Error: bifrostErr}).Category)
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
