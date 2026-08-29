package schemas

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testErrorFallbackRules() []ErrorFallbackRule {
	return []ErrorFallbackRule{{
		Name:     "policy",
		Scenario: FailureCategoryContentPolicy,
		Supplement: &ErrorFallbackSupplement{
			Providers:          []ModelProvider{OpenAI},
			ErrorCodes:         []string{"safety_violations"},
			ErrorTypes:         []string{"policy_error"},
			StatusCodes:        []int{400},
			MessageContainsAny: []string{"blocked"},
		},
		When: ErrorFallbackCondition{
			Categories:      []FailureCategory{FailureCategoryContentPolicy},
			ErrorCodes:      []string{"legacy_code"},
			ErrorTypes:      []string{"legacy_type"},
			StatusCodes:     []int{422},
			MessageContains: []string{"legacy message"},
		},
		Fallbacks: []Fallback{{Provider: Anthropic, Model: "claude-fallback"}},
	}}
}

func TestErrorFallbackRuleJSONRoundTrip(t *testing.T) {
	original := BifrostChatRequest{
		Provider:       OpenAI,
		Model:          "gpt-primary",
		ErrorFallbacks: testErrorFallbackRules(),
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"error_fallbacks"`) || !strings.Contains(string(encoded), `"message_contains_any"`) {
		t.Fatalf("missing error fallback fields: %s", encoded)
	}
	var decoded BifrostChatRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.ErrorFallbacks, original.ErrorFallbacks) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", decoded.ErrorFallbacks, original.ErrorFallbacks)
	}
}

func TestBifrostRequestErrorFallbackAccessors(t *testing.T) {
	rules := testErrorFallbackRules()
	requests := map[string]*BifrostRequest{
		"text":             {TextCompletionRequest: &BifrostTextCompletionRequest{}},
		"chat":             {ChatRequest: &BifrostChatRequest{}},
		"responses":        {ResponsesRequest: &BifrostResponsesRequest{}},
		"count_tokens":     {CountTokensRequest: &BifrostResponsesRequest{}},
		"compaction":       {CompactionRequest: &BifrostCompactionRequest{}},
		"embedding":        {EmbeddingRequest: &BifrostEmbeddingRequest{}},
		"rerank":           {RerankRequest: &BifrostRerankRequest{}},
		"ocr":              {OCRRequest: &BifrostOCRRequest{}},
		"speech":           {SpeechRequest: &BifrostSpeechRequest{}},
		"transcription":    {TranscriptionRequest: &BifrostTranscriptionRequest{}},
		"image_generation": {ImageGenerationRequest: &BifrostImageGenerationRequest{}},
		"image_edit":       {ImageEditRequest: &BifrostImageEditRequest{}},
		"image_variation":  {ImageVariationRequest: &BifrostImageVariationRequest{}},
		"video_generation": {VideoGenerationRequest: &BifrostVideoGenerationRequest{}},
		"video_edit":       {VideoEditRequest: &BifrostVideoEditRequest{}},
	}
	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			if got := request.GetErrorFallbacks(); got != nil {
				t.Fatalf("zero-value policy must remain nil, got %#v", got)
			}
			request.SetErrorFallbacks(rules)
			if !reflect.DeepEqual(request.GetErrorFallbacks(), rules) {
				t.Fatalf("policy was not stored: %#v", request.GetErrorFallbacks())
			}
			request.SetErrorFallbacks(nil)
			if got := request.GetErrorFallbacks(); got != nil {
				t.Fatalf("nil reset must remain nil, got %#v", got)
			}
		})
	}

	var nilRequest *BifrostRequest
	nilRequest.SetErrorFallbacks(rules)
	if nilRequest.GetErrorFallbacks() != nil {
		t.Fatal("nil request must return nil")
	}
	unsupported := &BifrostRequest{ListModelsRequest: &BifrostListModelsRequest{}}
	unsupported.SetErrorFallbacks(rules)
	if unsupported.GetErrorFallbacks() != nil {
		t.Fatal("unsupported operation must ignore error fallbacks")
	}
}

func TestErrorFallbacksSurviveRequestConversions(t *testing.T) {
	rules := testErrorFallbackRules()
	chat := &BifrostChatRequest{Provider: OpenAI, Model: "gpt", ErrorFallbacks: rules}
	responses := chat.ToResponsesRequest()
	if !reflect.DeepEqual(responses.ErrorFallbacks, rules) {
		t.Fatalf("chat-to-responses dropped rules: %#v", responses.ErrorFallbacks)
	}
	chatAgain := responses.ToChatRequest()
	if !reflect.DeepEqual(chatAgain.ErrorFallbacks, rules) {
		t.Fatalf("responses-to-chat dropped rules: %#v", chatAgain.ErrorFallbacks)
	}

	prompt := "hello"
	text := &BifrostTextCompletionRequest{
		Provider:       OpenAI,
		Model:          "gpt",
		Input:          &TextCompletionInput{PromptStr: &prompt},
		ErrorFallbacks: rules,
	}
	converted := text.ToBifrostChatRequest()
	if converted == nil || !reflect.DeepEqual(converted.ErrorFallbacks, rules) {
		t.Fatalf("text-to-chat dropped rules: %#v", converted)
	}
}

func TestFailureRecognitionSignalsAreBoundedAndInternal(t *testing.T) {
	signals := ExtractFailureRecognitionSignals([]byte(`{"error":{"code":"safety_violations","type":"policy_error","message":"blocked"},"ignored":"secret"}`))
	if !reflect.DeepEqual(signals.ErrorCodes, []string{"safety_violations"}) ||
		!reflect.DeepEqual(signals.ErrorTypes, []string{"policy_error"}) ||
		!reflect.DeepEqual(signals.Messages, []string{"blocked"}) {
		t.Fatalf("unexpected signals: %#v", signals)
	}

	merged := MergeFailureRecognitionSignals(signals, FailureRecognitionSignals{
		ErrorCodes: []string{"safety_violations", "second"},
		Messages:   []string{"blocked", "other"},
	})
	if !reflect.DeepEqual(merged.ErrorCodes, []string{"safety_violations", "second"}) ||
		!reflect.DeepEqual(merged.Messages, []string{"blocked", "other"}) {
		t.Fatalf("merge did not preserve order/deduplicate: %#v", merged)
	}

	long := strings.Repeat("x", maxFailureRecognitionLength+100)
	bounded := ExtractFailureRecognitionSignals(long)
	if len(bounded.Messages) != 1 || len(bounded.Messages[0]) != maxFailureRecognitionLength {
		t.Fatalf("plain-text signal was not bounded: %d", len(bounded.Messages[0]))
	}

	extra := BifrostErrorExtraFields{
		BaseProvider:   OpenAI,
		FailureSignals: signals,
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "safety_violations") || strings.Contains(string(encoded), string(OpenAI)) {
		t.Fatalf("internal recognition fields leaked into JSON: %s", encoded)
	}
}
