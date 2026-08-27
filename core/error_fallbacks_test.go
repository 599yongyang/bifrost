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

	"github.com/maximhq/bifrost/core/schemas"
)

const contentPolicySafetyBody = `{"error":{"message":"Your request was rejected by the safety system. If you believe this is an error, contact us at Azure support ticket and include the safety_violations=[sexual].","type":"invalid_request_error","code":"content_policy_violation"}}`

const unsafeImage429Body = `{"error":{"message":"The generated images appear to be unsafe. Try modifying the prompt or seeds.","type":"content_filter","code":"content_filtered"}}`

const fallbackChatBody = `{"id":"chatcmpl-fallback","object":"chat.completion","created":1724200001,"model":"grok-4-fast","choices":[{"index":0,"message":{"role":"assistant","content":"handled by safety fallback"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`

const emptyContentFilteredChatBody = `{"id":"chatcmpl-filtered","object":"chat.completion","created":1724200001,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":4,"completion_tokens":0,"total_tokens":4}}`

func TestErrorFallbacksReplaceOrdinaryFallbacksForContentPolicy(t *testing.T) {
	var primaryHits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		writeJSON(w, http.StatusBadRequest, contentPolicySafetyBody)
	}))
	defer primary.Close()

	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer ordinary.Close()

	var safetyFallbackHits atomic.Int32
	safetyFallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safetyFallbackHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer safetyFallback.Close()

	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 1, primary.URL)
	account.AddProviderWithBaseURL(schemas.Groq, 1, 1, ordinary.URL)
	account.AddProviderWithBaseURL(schemas.XAI, 1, 1, safetyFallback.URL)
	account.configs[schemas.OpenAI].NetworkConfig.MaxRetries = 0
	account.configs[schemas.Groq].NetworkConfig.MaxRetries = 0
	account.configs[schemas.XAI].NetworkConfig.MaxRetries = 0
	account.SetKeysForProvider(schemas.OpenAI, []schemas.Key{{ID: "primary-key", Value: *schemas.NewSecretVar("sk-openai"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	account.SetKeysForProvider(schemas.Groq, []schemas.Key{{ID: "ordinary-key", Value: *schemas.NewSecretVar("sk-groq"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	account.SetKeysForProvider(schemas.XAI, []schemas.Key{{ID: "error-key", Value: *schemas.NewSecretVar("sk-xai"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	client := newStreamTestClient(t, account)

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	resp, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input: []schemas.ChatMessage{
			{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("draw something explicit")}},
		},
		Fallbacks: []schemas.Fallback{
			{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
		},
		ErrorFallbacks: []schemas.ErrorFallbackRule{
			{
				Name: "content-policy",
				When: schemas.ErrorFallbackCondition{
					Categories: []schemas.FailureCategory{schemas.FailureCategoryContentPolicy},
				},
				Fallbacks: []schemas.Fallback{
					{Provider: schemas.XAI, Model: "grok-4-fast"},
				},
			},
		},
	})
	if bifrostErr != nil {
		t.Fatalf("expected safety fallback to succeed, got %v", bifrostErr)
	}
	if got := primaryHits.Load(); got != 1 {
		t.Fatalf("primary hits = %d, want 1", got)
	}
	if got := ordinaryHits.Load(); got != 0 {
		t.Fatalf("ordinary fallback hits = %d, want 0", got)
	}
	if got := safetyFallbackHits.Load(); got != 1 {
		t.Fatalf("error fallback hits = %d, want 1", got)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].ChatNonStreamResponseChoice == nil || resp.Choices[0].ChatNonStreamResponseChoice.Message == nil || resp.Choices[0].ChatNonStreamResponseChoice.Message.Content == nil || resp.Choices[0].ChatNonStreamResponseChoice.Message.Content.ContentStr == nil || *resp.Choices[0].ChatNonStreamResponseChoice.Message.Content.ContentStr != "handled by safety fallback" {
		t.Fatalf("unexpected fallback response: %+v", resp)
	}
	if !resp.ExtraFields.RoutingInfo.IsFallback {
		t.Fatal("expected response to be marked as fallback-served")
	}
	if got, _ := ctx.Value(schemas.BifrostContextKeyErrorFallbackRuleName).(string); got != "content-policy" {
		t.Fatalf("matched rule context = %q, want content-policy", got)
	}
	if got, _ := ctx.Value(schemas.BifrostContextKeyErrorFallbackCategory).(string); got != string(schemas.FailureCategoryContentPolicy) {
		t.Fatalf("matched category context = %q, want %q", got, schemas.FailureCategoryContentPolicy)
	}
}

func TestRawOnlySafetySignalSurvivesClientRawResponseStripping(t *testing.T) {
	customProvider := schemas.ModelProvider("custom-openai")
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, `{"fault":{"reason":"request rejected by moderation","type":"content_policy_error"}}`)
	}))
	defer primary.Close()
	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer ordinary.Close()
	var dedicatedHits atomic.Int32
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dedicatedHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	account.AddProviderWithBaseURL(customProvider, 1, 1, primary.URL)
	account.configs[customProvider].CustomProviderConfig = &schemas.CustomProviderConfig{
		CustomProviderKey: string(customProvider),
		BaseProviderType:  schemas.OpenAI,
	}
	account.AddProviderWithBaseURL(schemas.Groq, 1, 1, ordinary.URL)
	account.AddProviderWithBaseURL(schemas.XAI, 1, 1, dedicated.URL)
	for _, provider := range []schemas.ModelProvider{customProvider, schemas.Groq, schemas.XAI} {
		account.configs[provider].NetworkConfig.MaxRetries = 0
		account.configs[provider].SendBackRawResponse = false
		account.configs[provider].StoreRawRequestResponse = false
		account.SetKeysForProvider(provider, []schemas.Key{{ID: string(provider) + "-key", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	}
	client := newStreamTestClient(t, account)
	resp, bifrostErr := client.ChatCompletionRequest(schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second)), &schemas.BifrostChatRequest{
		Provider:  customProvider,
		Model:     "gpt-image",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("draw")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "llama"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Scenario:  schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok"}},
		}},
	})
	if bifrostErr != nil || resp == nil {
		t.Fatalf("expected dedicated fallback success, response=%v error=%v", resp, bifrostErr)
	}
	if ordinaryHits.Load() != 0 || dedicatedHits.Load() != 1 {
		t.Fatalf("ordinary/dedicated hits=%d/%d, want 0/1", ordinaryHits.Load(), dedicatedHits.Load())
	}
}

func TestOrdinaryFallbackContentPolicyFailureActivatesDedicatedChain(t *testing.T) {
	var primaryHits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		writeJSON(w, http.StatusBadRequest, `{"error":{"message":"invalid image request","type":"image_generation_user_error","code":"bad_request"}}`)
	}))
	defer primary.Close()

	var ordinarySafetyHits atomic.Int32
	ordinarySafety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinarySafetyHits.Add(1)
		writeJSON(w, http.StatusUnavailableForLegalReasons, `{"error":{"message":"content rejected","type":"content_policy_error","code":"content_policy_violation"}}`)
	}))
	defer ordinarySafety.Close()

	var skippedOrdinaryHits atomic.Int32
	skippedOrdinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skippedOrdinaryHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer skippedOrdinary.Close()

	var dedicatedHits atomic.Int32
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dedicatedHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	for provider, baseURL := range map[schemas.ModelProvider]string{
		schemas.OpenAI:   primary.URL,
		schemas.Groq:     ordinarySafety.URL,
		schemas.Cerebras: skippedOrdinary.URL,
		schemas.XAI:      dedicated.URL,
	} {
		account.AddProviderWithBaseURL(provider, 1, 1, baseURL)
		account.configs[provider].NetworkConfig.MaxRetries = 0
		account.SetKeysForProvider(provider, []schemas.Key{{ID: string(provider) + "-key", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	}
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	resp, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input: []schemas.ChatMessage{{
			Role: schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{
				ContentStr: Ptr("draw something"),
			},
		}},
		Fallbacks: []schemas.Fallback{
			{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
			{Provider: schemas.OpenAI, Model: "gpt-4o-mini"}, // duplicate primary must not be retried
			{Provider: schemas.Cerebras, Model: "llama-3.3-70b"},
		},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name:      "content-policy",
			Scenario:  schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
		}},
	})
	if bifrostErr != nil || resp == nil {
		t.Fatalf("expected dedicated fallback success, response=%v error=%v", resp, bifrostErr)
	}
	if got := primaryHits.Load(); got != 1 {
		t.Fatalf("primary hits = %d, want 1", got)
	}
	if got := ordinarySafetyHits.Load(); got != 1 {
		t.Fatalf("ordinary safety hits = %d, want 1", got)
	}
	if got := skippedOrdinaryHits.Load(); got != 0 {
		t.Fatalf("remaining ordinary hits = %d, want 0", got)
	}
	if got := dedicatedHits.Load(); got != 1 {
		t.Fatalf("dedicated hits = %d, want 1", got)
	}
	if got, _ := ctx.Value(schemas.BifrostContextKeyErrorFallbackRuleName).(string); got != "content-policy" {
		t.Fatalf("matched rule context = %q, want content-policy", got)
	}
	var activationLogged bool
	for _, entry := range ctx.GetRoutingEngineLogs() {
		if strings.Contains(entry.Message, "replacing 1 remaining ordinary fallback(s) with 1 dedicated fallback(s)") {
			activationLogged = true
			break
		}
	}
	if !activationLogged {
		t.Fatalf("routing logs do not explain mid-chain activation: %+v", ctx.GetRoutingEngineLogs())
	}
}

func TestOrdinaryStreamFallbackContentPolicyFailureActivatesDedicatedChain(t *testing.T) {
	primary := httptest.NewServer(sseHandler(`{"error":{"message":"invalid request","type":"image_generation_user_error","code":"bad_request"}}`))
	defer primary.Close()

	var ordinarySafetyHits atomic.Int32
	ordinarySafety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinarySafetyHits.Add(1)
		sseHandler(`{"error":{"message":"content rejected","type":"content_policy_error","code":"content_policy_violation"}}`)(w, r)
	}))
	defer ordinarySafety.Close()

	var skippedOrdinaryHits atomic.Int32
	skippedOrdinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skippedOrdinaryHits.Add(1)
		sseHandler(`{"id":"skip","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"wrong"},"finish_reason":"stop"}]}`)(w, r)
	}))
	defer skippedOrdinary.Close()

	var dedicatedHits atomic.Int32
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dedicatedHits.Add(1)
		sseHandler(
			`{"id":"dedicated","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"he"}}]}`,
			`{"id":"dedicated","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
			`{"id":"dedicated","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)(w, r)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	for provider, baseURL := range map[schemas.ModelProvider]string{
		schemas.OpenAI:   primary.URL,
		schemas.Groq:     ordinarySafety.URL,
		schemas.Cerebras: skippedOrdinary.URL,
		schemas.XAI:      dedicated.URL,
	} {
		account.AddProviderWithBaseURL(provider, 1, 1, baseURL)
		account.configs[provider].NetworkConfig.MaxRetries = 0
		account.SetKeysForProvider(provider, []schemas.Key{{ID: string(provider) + "-key", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	}
	client := newStreamTestClient(t, account)
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	stream, bifrostErr := client.ChatCompletionStreamRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input: []schemas.ChatMessage{{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: Ptr("draw something")},
		}},
		Fallbacks: []schemas.Fallback{
			{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
			{Provider: schemas.Cerebras, Model: "llama-3.3-70b"},
		},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name:      "content-policy",
			Scenario:  schemas.FailureCategoryContentPolicy,
			Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
		}},
	})
	if bifrostErr != nil {
		t.Fatalf("expected dedicated stream fallback success, got %v", bifrostErr)
	}
	content, errs := drainChatStream(stream)
	if len(errs) > 0 || content != "hello" {
		t.Fatalf("stream content=%q errors=%v, want hello", content, errs)
	}
	if ordinarySafetyHits.Load() != 1 || skippedOrdinaryHits.Load() != 0 || dedicatedHits.Load() != 1 {
		t.Fatalf("hits safety/skipped/dedicated=%d/%d/%d, want 1/0/1", ordinarySafetyHits.Load(), skippedOrdinaryHits.Load(), dedicatedHits.Load())
	}
}

func TestMidChainDedicatedExhaustionReturnsOriginalPrimaryError(t *testing.T) {
	const primaryMessage = "original primary failure"
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, `{"error":{"message":"`+primaryMessage+`","type":"invalid_request_error"}}`)
	}))
	defer primary.Close()
	ordinarySafety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnavailableForLegalReasons, `{"error":{"message":"content rejected","type":"content_policy_error"}}`)
	}))
	defer ordinarySafety.Close()
	var skippedHits atomic.Int32
	skipped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skippedHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer skipped.Close()
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, `{"error":{"message":"dedicated unavailable","type":"server_error"}}`)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	for provider, baseURL := range map[schemas.ModelProvider]string{
		schemas.OpenAI: primary.URL, schemas.Groq: ordinarySafety.URL,
		schemas.Cerebras: skipped.URL, schemas.XAI: dedicated.URL,
	} {
		account.AddProviderWithBaseURL(provider, 1, 1, baseURL)
		account.configs[provider].NetworkConfig.MaxRetries = 0
		account.SetKeysForProvider(provider, []schemas.Key{{ID: string(provider) + "-key", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	}
	client := newStreamTestClient(t, account)
	resp, bifrostErr := client.ChatCompletionRequest(schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second)), &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI, Model: "gpt-4o-mini",
		Input:          []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks:      []schemas.Fallback{{Provider: schemas.Groq, Model: "llama"}, {Provider: schemas.Cerebras, Model: "llama"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{Scenario: schemas.FailureCategoryContentPolicy, Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok"}}}},
	})
	if resp != nil || bifrostErr == nil || bifrostErr.Error == nil || bifrostErr.Error.Message != primaryMessage {
		t.Fatalf("response=%v error=%v, want original primary error %q", resp, bifrostErr, primaryMessage)
	}
	if skippedHits.Load() != 0 {
		t.Fatalf("remaining ordinary fallback hits=%d, want 0", skippedHits.Load())
	}
}

func TestRequestActivatesAtMostOneDedicatedChain(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, `{"error":{"message":"invalid request","type":"invalid_request_error"}}`)
	}))
	defer primary.Close()
	ordinarySafety := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnavailableForLegalReasons, `{"error":{"message":"content rejected","type":"content_policy_error"}}`)
	}))
	defer ordinarySafety.Close()
	rateLimitedDedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusTooManyRequests, `{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`)
	}))
	defer rateLimitedDedicated.Close()
	var continuedDedicatedHits atomic.Int32
	continuedDedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		continuedDedicatedHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer continuedDedicated.Close()
	var secondRuleHits atomic.Int32
	secondRule := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondRuleHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer secondRule.Close()

	account := NewMockAccount()
	for provider, baseURL := range map[schemas.ModelProvider]string{
		schemas.OpenAI: primary.URL, schemas.Groq: ordinarySafety.URL,
		schemas.XAI: rateLimitedDedicated.URL, schemas.OpenRouter: continuedDedicated.URL,
		schemas.Cerebras: secondRule.URL,
	} {
		account.AddProviderWithBaseURL(provider, 1, 1, baseURL)
		account.configs[provider].NetworkConfig.MaxRetries = 0
		account.SetKeysForProvider(provider, []schemas.Key{{ID: string(provider) + "-key", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	}
	client := newStreamTestClient(t, account)
	resp, bifrostErr := client.ChatCompletionRequest(schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second)), &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI, Model: "gpt-4o-mini",
		Input:     []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "llama"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{
			{Scenario: schemas.FailureCategoryContentPolicy, Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok"}, {Provider: schemas.OpenRouter, Model: "open-model"}}},
			{Scenario: schemas.FailureCategoryRateLimit, Fallbacks: []schemas.Fallback{{Provider: schemas.Cerebras, Model: "llama"}}},
		},
	})
	if bifrostErr != nil || resp == nil {
		t.Fatalf("expected first dedicated chain to continue to success, response=%v error=%v", resp, bifrostErr)
	}
	if continuedDedicatedHits.Load() != 1 || secondRuleHits.Load() != 0 {
		t.Fatalf("continued-first/second-rule hits=%d/%d, want 1/0", continuedDedicatedHits.Load(), secondRuleHits.Load())
	}
}

func TestSuccessfulEmptyContentPolicyResponseUsesDedicatedFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, emptyContentFilteredChatBody)
	}))
	defer primary.Close()

	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer ordinary.Close()

	var dedicatedHits atomic.Int32
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dedicatedHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer dedicated.Close()

	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 1, primary.URL)
	account.AddProviderWithBaseURL(schemas.Groq, 1, 1, ordinary.URL)
	account.AddProviderWithBaseURL(schemas.XAI, 1, 1, dedicated.URL)
	for _, provider := range []schemas.ModelProvider{schemas.OpenAI, schemas.Groq, schemas.XAI} {
		account.configs[provider].NetworkConfig.MaxRetries = 0
		account.SetKeysForProvider(provider, []schemas.Key{{ID: string(provider) + "-key", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	}
	client := newStreamTestClient(t, account)

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	resp, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input: []schemas.ChatMessage{{
			Role:    schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{ContentStr: Ptr("filtered prompt")},
		}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"}},
		ErrorFallbacks: []schemas.ErrorFallbackRule{{
			Name:      "content-policy",
			When:      schemas.ErrorFallbackCondition{Categories: []schemas.FailureCategory{schemas.FailureCategoryContentPolicy}},
			Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
		}},
	})
	if bifrostErr != nil || resp == nil {
		t.Fatalf("expected dedicated fallback success, response=%v error=%v", resp, bifrostErr)
	}
	if ordinaryHits.Load() != 0 || dedicatedHits.Load() != 1 {
		t.Fatalf("ordinary hits=%d dedicated hits=%d, want 0/1", ordinaryHits.Load(), dedicatedHits.Load())
	}
}

func TestSuccessfulContentPolicyResponseWithUsableOutputIsPreserved(t *testing.T) {
	finishReason := "content_filter"
	content := "partial usable output"
	response := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{{
			FinishReason: &finishReason,
			ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{Message: &schemas.ChatMessage{
				Role:    schemas.ChatMessageRoleAssistant,
				Content: &schemas.ChatMessageContent{ContentStr: &content},
			}},
		}},
	}}
	req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{ErrorFallbacks: []schemas.ErrorFallbackRule{{
		When:      schemas.ErrorFallbackCondition{Categories: []schemas.FailureCategory{schemas.FailureCategoryContentPolicy}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
	}}}}
	if err := successfulContentPolicyError(req, response); err != nil {
		t.Fatalf("usable partial output must not be converted into a fallback error: %v", err)
	}
}

func TestSuccessfulEmptyResponsesContentFilterIsNormalized(t *testing.T) {
	status := schemas.ResponsesResponseStatusIncomplete
	response := &schemas.BifrostResponse{ResponsesResponse: &schemas.BifrostResponsesResponse{
		Status: &status,
		IncompleteDetails: &schemas.ResponsesResponseIncompleteDetails{
			Reason: schemas.ResponsesResponseIncompleteReasonContentFilter,
		},
	}}
	req := &schemas.BifrostRequest{ResponsesRequest: &schemas.BifrostResponsesRequest{ErrorFallbacks: []schemas.ErrorFallbackRule{{
		When:      schemas.ErrorFallbackCondition{Categories: []schemas.FailureCategory{schemas.FailureCategoryContentPolicy}},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
	}}}}
	err := successfulContentPolicyError(req, response)
	if err == nil || err.Error == nil || err.Error.Type == nil || *err.Error.Type != "content_filter" {
		t.Fatalf("expected normalized content-filter error, got %v", err)
	}
}

func TestSuccessfulEmptyImageContentFilterIsNormalizedBeforeImageValidation(t *testing.T) {
	finishReason := "content_filtered"
	response := &schemas.BifrostResponse{ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{
		ImageGenerationResponseParameters: &schemas.ImageGenerationResponseParameters{
			FinishReasons: []*string{&finishReason},
		},
	}}
	req := &schemas.BifrostRequest{
		RequestType: schemas.ImageGenerationRequest,
		ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{
			Provider: schemas.Bedrock,
			Model:    "stability.stable-image",
			ErrorFallbacks: []schemas.ErrorFallbackRule{{
				Scenario:  schemas.FailureCategoryContentPolicy,
				Fallbacks: []schemas.Fallback{{Provider: schemas.OpenAI, Model: "gpt-image-1"}},
			}},
		},
	}

	err := successfulContentPolicyError(req, response)
	if err == nil || err.Error == nil || err.Error.Code == nil || *err.Error.Code != "content_filter" {
		t.Fatalf("expected normalized image content-filter error, got %v", err)
	}
	recognition := RecognizeFailure(FailureSignal{Response: response})
	if recognition.Category != schemas.FailureCategoryContentPolicy || recognition.MatchedBy != FailureMatchedByResponseSignal {
		t.Fatalf("image recognition = %#v", recognition)
	}
}

func TestImageContentPolicyFinishReasonVariants(t *testing.T) {
	for _, finishReason := range []string{"content_filter", "CONTENT_FILTERED", "ContentFiltered", "IMAGE_SAFETY", "guardrail_intervened"} {
		t.Run(finishReason, func(t *testing.T) {
			response := &schemas.BifrostResponse{ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{
				ImageGenerationResponseParameters: &schemas.ImageGenerationResponseParameters{
					FinishReasons: []*string{&finishReason},
				},
			}}
			if recognition := RecognizeFailure(FailureSignal{Response: response}); recognition.Category != schemas.FailureCategoryContentPolicy {
				t.Fatalf("recognition = %#v, want content policy", recognition)
			}
		})
	}
}

func TestShouldTryFallbacksHonorsHardStopsForDedicatedChains(t *testing.T) {
	client := &Bifrost{logger: NewNoOpLogger()}
	fallbacks := []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}}
	allowFallbacks := false
	if client.shouldTryFallbacks(fallbacks, &schemas.BifrostError{
		AllowFallbacks: &allowFallbacks,
		Error:          &schemas.ErrorField{Message: "blocked"},
	}) {
		t.Fatal("AllowFallbacks=false must block a dedicated fallback chain")
	}
	cancelled := schemas.RequestCancelled
	if client.shouldTryFallbacks(fallbacks, &schemas.BifrostError{
		Error: &schemas.ErrorField{Type: &cancelled, Message: "cancelled"},
	}) {
		t.Fatal("request cancellation must block a dedicated fallback chain")
	}
}

func TestErrorFallbacksDoNotFallThroughToOrdinaryFallbacksWhenDedicatedChainFails(t *testing.T) {
	var primaryHits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		writeJSON(w, http.StatusBadRequest, contentPolicySafetyBody)
	}))
	defer primary.Close()

	var ordinaryHits atomic.Int32
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer ordinary.Close()

	var safetyFallbackHits atomic.Int32
	safetyFallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safetyFallbackHits.Add(1)
		writeJSON(w, http.StatusInternalServerError, `{"error":{"message":"temporary outage","type":"server_error"}}`)
	}))
	defer safetyFallback.Close()

	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 1, primary.URL)
	account.AddProviderWithBaseURL(schemas.Groq, 1, 1, ordinary.URL)
	account.AddProviderWithBaseURL(schemas.XAI, 1, 1, safetyFallback.URL)
	account.configs[schemas.OpenAI].NetworkConfig.MaxRetries = 0
	account.configs[schemas.Groq].NetworkConfig.MaxRetries = 0
	account.configs[schemas.XAI].NetworkConfig.MaxRetries = 0
	account.SetKeysForProvider(schemas.OpenAI, []schemas.Key{{ID: "primary-key", Value: *schemas.NewSecretVar("sk-openai"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	account.SetKeysForProvider(schemas.Groq, []schemas.Key{{ID: "ordinary-key", Value: *schemas.NewSecretVar("sk-groq"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	account.SetKeysForProvider(schemas.XAI, []schemas.Key{{ID: "error-key", Value: *schemas.NewSecretVar("sk-xai"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	client := newStreamTestClient(t, account)

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	resp, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input: []schemas.ChatMessage{
			{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("draw something explicit")}},
		},
		Fallbacks: []schemas.Fallback{
			{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
		},
		ErrorFallbacks: []schemas.ErrorFallbackRule{
			{
				Name: "content-policy",
				When: schemas.ErrorFallbackCondition{
					Categories: []schemas.FailureCategory{schemas.FailureCategoryContentPolicy},
				},
				Fallbacks: []schemas.Fallback{
					{Provider: schemas.XAI, Model: "grok-4-fast"},
				},
			},
		},
	})
	if resp != nil {
		t.Fatalf("expected no response when dedicated error fallback chain fails, got %+v", resp)
	}
	if bifrostErr == nil || bifrostErr.Error == nil {
		t.Fatal("expected the original primary error to be returned")
	}
	if bifrostErr.Error.Message != "Your request was rejected by the safety system. If you believe this is an error, contact us at Azure support ticket and include the safety_violations=[sexual]." {
		t.Fatalf("returned error = %q, want the original primary safety error", bifrostErr.Error.Message)
	}
	if got := primaryHits.Load(); got != 1 {
		t.Fatalf("primary hits = %d, want 1", got)
	}
	if got := safetyFallbackHits.Load(); got != 1 {
		t.Fatalf("error fallback hits = %d, want 1", got)
	}
	if got := ordinaryHits.Load(); got != 0 {
		t.Fatalf("ordinary fallback hits = %d, want 0", got)
	}
}

func TestContentPolicySignalsOverrideHTTP429Retries(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := hits.Add(1)
		w.Header().Set("X-Attempt", fmt.Sprintf("%d", attempt))
		writeJSON(w, http.StatusTooManyRequests, unsafeImage429Body)
	}))
	defer server.Close()

	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 1, server.URL)
	account.configs[schemas.OpenAI].NetworkConfig.MaxRetries = 2
	account.configs[schemas.OpenAI].NetworkConfig.RetryBackoffInitial = time.Millisecond
	account.configs[schemas.OpenAI].NetworkConfig.RetryBackoffMax = 2 * time.Millisecond
	account.SetKeysForProvider(schemas.OpenAI, []schemas.Key{
		{ID: "key-1", Value: *schemas.NewSecretVar("sk-1"), Models: schemas.WhiteList{"*"}, Weight: 100},
		{ID: "key-2", Value: *schemas.NewSecretVar("sk-2"), Models: schemas.WhiteList{"*"}, Weight: 100},
	})
	client := newStreamTestClient(t, account)

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	resp, bifrostErr := client.ImageGenerationRequest(ctx, &schemas.BifrostImageGenerationRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-image-1",
		Input:    &schemas.ImageGenerationInput{Prompt: "unsafe image"},
	})
	if resp != nil {
		t.Fatalf("expected no image response, got %+v", resp)
	}
	if bifrostErr == nil || bifrostErr.Error == nil {
		t.Fatal("expected a content policy error")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hits = %d, want 1 (content policy must not retry or rotate keys)", got)
	}
	trail, _ := ctx.Value(schemas.BifrostContextKeyAttemptTrail).([]schemas.KeyAttemptRecord)
	if len(trail) != 1 {
		t.Fatalf("attempt trail length = %d, want 1", len(trail))
	}
	if trail[0].FailReason == nil || *trail[0].FailReason != string(schemas.FailureCategoryContentPolicy) {
		t.Fatalf("attempt fail reason = %v, want %q", trail[0].FailReason, schemas.FailureCategoryContentPolicy)
	}
}

func TestSanitizeMatchedErrorFallbackChainRemovesPrimaryAndDuplicates(t *testing.T) {
	filtered := sanitizeMatchedErrorFallbackChain(
		schemas.OpenAI,
		"gpt-4o-mini",
		[]schemas.Fallback{
			{Provider: schemas.OpenAI, Model: "gpt-4o-mini"},
			{Provider: schemas.XAI, Model: "grok-4-fast"},
			{Provider: schemas.XAI, Model: "grok-4-fast"},
			{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
			{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
		},
	)

	if len(filtered) != 2 {
		t.Fatalf("filtered length = %d, want 2", len(filtered))
	}
	if filtered[0].Provider != schemas.XAI || filtered[0].Model != "grok-4-fast" {
		t.Fatalf("filtered[0] = %+v, want xai/grok-4-fast", filtered[0])
	}
	if filtered[1].Provider != schemas.Groq || filtered[1].Model != "llama-3.3-70b-versatile" {
		t.Fatalf("filtered[1] = %+v, want groq/llama-3.3-70b-versatile", filtered[1])
	}
}

func TestErrorFallbacksSkipPrimaryAndDuplicateTargets(t *testing.T) {
	var primaryHits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		writeJSON(w, http.StatusBadRequest, contentPolicySafetyBody)
	}))
	defer primary.Close()

	var xaiHits atomic.Int32
	xai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xaiHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer xai.Close()

	var groqHits atomic.Int32
	groq := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groqHits.Add(1)
		writeJSON(w, http.StatusOK, fallbackChatBody)
	}))
	defer groq.Close()

	account := NewMockAccount()
	account.AddProviderWithBaseURL(schemas.OpenAI, 1, 1, primary.URL)
	account.AddProviderWithBaseURL(schemas.XAI, 1, 1, xai.URL)
	account.AddProviderWithBaseURL(schemas.Groq, 1, 1, groq.URL)
	account.configs[schemas.OpenAI].NetworkConfig.MaxRetries = 0
	account.configs[schemas.XAI].NetworkConfig.MaxRetries = 0
	account.configs[schemas.Groq].NetworkConfig.MaxRetries = 0
	account.SetKeysForProvider(schemas.OpenAI, []schemas.Key{{ID: "primary-key", Value: *schemas.NewSecretVar("sk-openai"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	account.SetKeysForProvider(schemas.XAI, []schemas.Key{{ID: "xai-key", Value: *schemas.NewSecretVar("sk-xai"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	account.SetKeysForProvider(schemas.Groq, []schemas.Key{{ID: "groq-key", Value: *schemas.NewSecretVar("sk-groq"), Models: schemas.WhiteList{"*"}, Weight: 100}})
	client := newStreamTestClient(t, account)

	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(30*time.Second))
	resp, bifrostErr := client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI,
		Model:    "gpt-4o-mini",
		Input: []schemas.ChatMessage{
			{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("draw something explicit")}},
		},
		ErrorFallbacks: []schemas.ErrorFallbackRule{
			{
				Name: "content-policy",
				When: schemas.ErrorFallbackCondition{
					Categories: []schemas.FailureCategory{schemas.FailureCategoryContentPolicy},
				},
				Fallbacks: []schemas.Fallback{
					{Provider: schemas.OpenAI, Model: "gpt-4o-mini"},
					{Provider: schemas.XAI, Model: "grok-4-fast"},
					{Provider: schemas.XAI, Model: "grok-4-fast"},
					{Provider: schemas.Groq, Model: "llama-3.3-70b-versatile"},
				},
			},
		},
	})
	if bifrostErr != nil {
		t.Fatalf("expected sanitized error fallback chain to succeed, got %v", bifrostErr)
	}
	if resp == nil {
		t.Fatal("expected fallback response")
	}
	if got := primaryHits.Load(); got != 1 {
		t.Fatalf("primary hits = %d, want 1", got)
	}
	if got := xaiHits.Load(); got != 1 {
		t.Fatalf("xai hits = %d, want 1", got)
	}
	if got := groqHits.Load(); got != 0 {
		t.Fatalf("groq hits = %d, want 0 because the first unique dedicated fallback already succeeded", got)
	}
}

func TestErrorFallbackSupplementMatchesAcrossDifferentSignalFields(t *testing.T) {
	failure := classifiedFailure{
		Provider:   schemas.OpenAI,
		StatusCode: Ptr(http.StatusBadRequest),
		ErrorCodes: []string{"content_policy_violation"},
	}

	match, ok := matchSupplement(failure, &schemas.ErrorFallbackSupplement{
		Providers:          []schemas.ModelProvider{schemas.OpenAI},
		ErrorTypes:         []string{"content_filter"},
		MessageContainsAny: []string{"unsafe"},
		ErrorCodes:         []string{"content_policy_violation"},
	})
	if !ok {
		t.Fatal("expected supplement to match when any non-provider signal matches")
	}
	if match.Source != "supplement.error_codes" || match.Detail != "content_policy_violation" {
		t.Fatalf("unexpected supplement match: %+v", match)
	}
}

func TestErrorFallbackSupplementRequiresRealSignalBeyondProviderScope(t *testing.T) {
	failure := classifiedFailure{Provider: schemas.OpenAI}
	if _, ok := matchSupplement(failure, &schemas.ErrorFallbackSupplement{
		Providers: []schemas.ModelProvider{schemas.OpenAI},
	}); ok {
		t.Fatal("providers alone must not count as a supplement matcher")
	}
	if supplementHasMatchers(schemas.ErrorFallbackSupplement{
		Providers: []schemas.ModelProvider{schemas.OpenAI},
	}) {
		t.Fatal("supplementHasMatchers must ignore providers-only supplements")
	}
}

func TestErrorFallbackScenarioRecognizesAzureSafetyMessages(t *testing.T) {
	req := &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{
			Provider: schemas.Azure,
			Model:    "gpt-4o-mini",
		},
	}
	err := &schemas.BifrostError{
		StatusCode: Ptr(http.StatusBadRequest),
		Error: &schemas.ErrorField{
			Type:    Ptr("invalid_request_error"),
			Code:    Ptr("content_policy_violation"),
			Message: "Your request was rejected by the safety system. If you believe this is an error, contact us at Azure support ticket and include the safety_violations=[sexual].",
		},
	}

	rule, failure := firstMatchingErrorFallbackRule(req, err, []schemas.ErrorFallbackRule{{
		Name:      "content-policy",
		Scenario:  schemas.FailureCategoryContentPolicy,
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
	}})
	if rule == nil {
		t.Fatal("expected Azure safety rejection to match scenario rule")
	}
	if failure.Category != schemas.FailureCategoryContentPolicy {
		t.Fatalf("failure category = %q, want %q", failure.Category, schemas.FailureCategoryContentPolicy)
	}
	if failure.CategorySource != FailureMatchedByProviderPack {
		t.Fatalf("category source = %q, want provider_pack", failure.CategorySource)
	}
	if failure.MatchSource != FailureMatchedByProviderPack || failure.RuleMatch.Pack != "azure_content_policy" {
		t.Fatalf("match explanation = %#v, want Azure provider pack", failure.RuleMatch)
	}
}

func TestErrorFallbackScenarioRecognizesStructuredAndChineseSafetyVariants(t *testing.T) {
	rules := []schemas.ErrorFallbackRule{{
		Scenario:  schemas.FailureCategoryContentPolicy,
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
	}}
	tests := []struct {
		name     string
		provider schemas.ModelProvider
		code     *string
		message  string
	}{
		{
			name:     "structured code does not depend on message language",
			provider: schemas.Azure,
			code:     Ptr("content_policy_violation"),
			message:  "请求处理失败，请稍后重试",
		},
		{
			name:     "Chinese unsafe image message",
			provider: schemas.ModelProvider("custom-provider"),
			message:  "生成的图片可能不安全，请修改提示词后重试。",
		},
		{
			name:     "Chinese safety system rejection",
			provider: schemas.ModelProvider("custom-provider"),
			message:  "请求被安全系统拒绝。",
		},
		{
			name:     "Chinese nudity and erotic-content guardrail",
			provider: schemas.ModelProvider("custom-provider"),
			message:  "非常抱歉，该提示可能违反了关于裸露、色情或情色内容的防护限制。如果你认为此判断有误，请重试或修改提示语。",
		},
		{
			name:     "moderation blocked code without recognizable message",
			provider: schemas.ModelProvider("custom-provider"),
			code:     Ptr("moderation_blocked"),
			message:  "request could not be processed",
		},
		{
			name:     "unsafe image message with request id suffix",
			provider: schemas.ModelProvider("custom-provider"),
			message:  "The generated images appear to be unsafe. Try modifying the prompt or seeds. (request id: 202608270943173936198778268d9d66C5w1vEo)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &schemas.BifrostRequest{
				RequestType: schemas.ImageGenerationRequest,
				ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{
					Provider: tt.provider,
					Model:    "image-model",
				},
			}
			err := &schemas.BifrostError{Error: &schemas.ErrorField{Code: tt.code, Message: tt.message}}
			rule, failure := firstMatchingErrorFallbackRule(req, err, rules)
			if rule == nil || failure.Category != schemas.FailureCategoryContentPolicy {
				t.Fatalf("expected content-policy recognition, rule=%v category=%q", rule, failure.Category)
			}
		})
	}
}

func TestErrorFallbackScenarioRecognizesGeminiSafetyCodes(t *testing.T) {
	req := &schemas.BifrostRequest{
		RequestType: schemas.ImageGenerationRequest,
		ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{
			Provider: schemas.Gemini,
			Model:    "gemini-2.5-flash-image",
		},
	}
	err := &schemas.BifrostError{
		Error: &schemas.ErrorField{
			Type:    Ptr("IMAGE_SAFETY"),
			Code:    Ptr("IMAGE_SAFETY"),
			Message: "IMAGE_SAFETY",
		},
	}

	rule, failure := firstMatchingErrorFallbackRule(req, err, []schemas.ErrorFallbackRule{{
		Name:      "content-policy",
		Scenario:  schemas.FailureCategoryContentPolicy,
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
	}})
	if rule == nil {
		t.Fatal("expected Gemini safety finish reason to match scenario rule")
	}
	if failure.CategorySource != FailureMatchedByProviderPack || failure.RuleMatch.Pack != "gemini_safety" {
		t.Fatalf("match explanation = %#v, want Gemini provider pack", failure.RuleMatch)
	}
}

func TestCustomProviderUsesBaseProviderSafetyPack(t *testing.T) {
	recognition := RecognizeFailure(FailureSignal{
		Provider: schemas.ModelProvider("custom-gemini"),
		Error: &schemas.BifrostError{
			Error: &schemas.ErrorField{Type: Ptr("IMAGE_SAFETY"), Message: "generation failed"},
			ExtraFields: schemas.BifrostErrorExtraFields{
				BaseProvider: schemas.Gemini,
			},
		},
	})
	if recognition.Category != schemas.FailureCategoryContentPolicy || recognition.MatchedBy != FailureMatchedByProviderPack || recognition.Pack != "gemini_safety" {
		t.Fatalf("recognition = %#v, want Gemini provider-pack content policy", recognition)
	}
}

func TestErrorFallbackScenarioRecognizesBedrockGuardrailSignals(t *testing.T) {
	req := &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{
			Provider: schemas.Bedrock,
			Model:    "anthropic.claude-3-7-sonnet",
		},
	}
	err := &schemas.BifrostError{
		Error: &schemas.ErrorField{
			Type:    Ptr("guardrail_intervened"),
			Code:    Ptr("guardrail_intervened"),
			Message: "guardrail intervened",
		},
	}

	rule, failure := firstMatchingErrorFallbackRule(req, err, []schemas.ErrorFallbackRule{{
		Name:      "content-policy",
		Scenario:  schemas.FailureCategoryContentPolicy,
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-4-fast"}},
	}})
	if rule == nil {
		t.Fatal("expected Bedrock guardrail rejection to match scenario rule")
	}
	if failure.CategorySource != FailureMatchedByProviderPack || failure.RuleMatch.Pack != "bedrock_guardrail" {
		t.Fatalf("match explanation = %#v, want Bedrock provider pack", failure.RuleMatch)
	}
}

func TestRecognizeFailureExplainsResponseAndMultilingualSignals(t *testing.T) {
	finishReason := "content_filter"
	responseRecognition := RecognizeFailure(FailureSignal{Response: &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{Choices: []schemas.BifrostResponseChoice{{FinishReason: &finishReason}}},
	}})
	if responseRecognition.MatchedBy != FailureMatchedByResponseSignal || responseRecognition.PatternID != "empty_content_filter" {
		t.Fatalf("response recognition = %#v", responseRecognition)
	}

	messageRecognition := RecognizeFailure(FailureSignal{
		Provider: schemas.ModelProvider("custom-provider"),
		Error:    &schemas.BifrostError{Error: &schemas.ErrorField{Message: "生成的图片可能不安全，请修改提示词后重试。"}},
	})
	if messageRecognition.Category != schemas.FailureCategoryContentPolicy || messageRecognition.MatchedBy != FailureMatchedByMessagePack || messageRecognition.PatternID != "unsafe_image" {
		t.Fatalf("message recognition = %#v", messageRecognition)
	}
}

func TestRecognizeFailureUsesBoundedRawErrorMetadata(t *testing.T) {
	recognition := RecognizeFailure(FailureSignal{Error: &schemas.BifrostError{
		StatusCode: Ptr(http.StatusBadRequest),
		Error:      &schemas.ErrorField{Type: Ptr("image_generation_user_error"), Message: "image generation failed"},
		ExtraFields: schemas.BifrostErrorExtraFields{RawResponse: map[string]any{
			"error": map[string]any{
				"message":           "Your request was rejected by the safety system.",
				"safety_violations": []any{"sexual"},
			},
			"echoed_prompt": "this unrelated field must not be inspected",
		}},
	}})
	if recognition.Category != schemas.FailureCategoryContentPolicy {
		t.Fatalf("recognition = %#v, want content policy", recognition)
	}
	if recognition.PatternID == "" || strings.Contains(recognition.PatternID, "Your request") {
		t.Fatalf("recognition must expose only a stable pattern id: %#v", recognition)
	}
}

func TestSupplementCanMatchBoundedRawErrorMessage(t *testing.T) {
	provider := schemas.ModelProvider("custom-provider")
	req := &schemas.BifrostRequest{
		RequestType:            schemas.ImageGenerationRequest,
		ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{Provider: provider, Model: "image-model"},
	}
	err := &schemas.BifrostError{
		StatusCode: Ptr(http.StatusBadRequest),
		Error:      &schemas.ErrorField{Message: "image generation failed"},
		ExtraFields: schemas.BifrostErrorExtraFields{RawResponse: map[string]any{
			"error": map[string]any{"message": "vendor moderation signature 731"},
		}},
	}
	rule, failure := firstMatchingErrorFallbackRule(req, err, []schemas.ErrorFallbackRule{{
		Scenario: schemas.FailureCategoryContentPolicy,
		Supplement: &schemas.ErrorFallbackSupplement{
			Providers:          []schemas.ModelProvider{provider},
			MessageContainsAny: []string{"moderation signature 731"},
		},
		Fallbacks: []schemas.Fallback{{Provider: schemas.XAI, Model: "grok-image"}},
	}})
	if rule == nil || failure.RuleMatch.MatchedBy != FailureMatchedBySupplement {
		t.Fatalf("rule=%v failure=%#v, want raw-message supplement match", rule, failure)
	}
}
