package bifrost

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

type safePhraseMatcher struct {
	id      string
	phrases []string
}

type errorFallbackMatch struct {
	MatchedBy string
	Pack      string
	PatternID string
	Source    string // internal detector path; normalized before it leaves this module
	Detail    string // internal safe pattern identifier
}

const (
	FailureMatchedByResponseSignal = "response_signal"
	FailureMatchedByStructured     = "structured"
	FailureMatchedByProviderPack   = "provider_pack"
	FailureMatchedByMessagePack    = "message_pack"
	FailureMatchedBySupplement     = "supplement"
	FailureMatchedByLegacyWhen     = "legacy_when"
)

// FailureSignal is the provider-neutral input to failure recognition. It keeps
// recognition independent from routing-rule matching and can also represent a
// provider response that encoded a failure without returning an HTTP error.
type FailureSignal struct {
	Provider     schemas.ModelProvider
	BaseProvider schemas.ModelProvider
	RequestType  schemas.RequestType
	Error        *schemas.BifrostError
	Response     *schemas.BifrostResponse
}

// FailureRecognition explains the normalized failure without exposing raw
// provider messages. Pack and PatternID are stable identifiers suitable for
// logs, traces, and support diagnostics.
type FailureRecognition struct {
	Category  schemas.FailureCategory
	MatchedBy string
	Pack      string
	PatternID string
}

func successfulContentPolicyError(req *schemas.BifrostRequest, response *schemas.BifrostResponse) *schemas.BifrostError {
	if req == nil || response == nil || len(req.GetErrorFallbacks()) == 0 || RecognizeFailure(FailureSignal{Response: response}).Category != schemas.FailureCategoryContentPolicy {
		return nil
	}

	allowFallbacks := true
	errorType := "content_filter"
	statusCode := http.StatusBadRequest
	err := &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     &statusCode,
		Type:           &errorType,
		AllowFallbacks: &allowFallbacks,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Code:    &errorType,
			Message: "upstream provider blocked the response due to content policy",
		},
	}
	populateErrorFallbackExtraFields(err, req)
	if rule, _ := firstMatchingErrorFallbackRule(req, err, req.GetErrorFallbacks()); rule == nil {
		return nil
	}
	return err
}

func responseIsEmptyContentPolicyBlock(response *schemas.BifrostResponse) bool {
	if response.ChatResponse != nil && choicesAreEmptyContentPolicyBlocks(response.ChatResponse.Choices) {
		return true
	}
	if response.TextCompletionResponse != nil && choicesAreEmptyContentPolicyBlocks(response.TextCompletionResponse.Choices) {
		return true
	}
	if responses := response.ResponsesResponse; responses != nil && responses.IncompleteDetails != nil &&
		strings.EqualFold(strings.TrimSpace(responses.IncompleteDetails.Reason), schemas.ResponsesResponseIncompleteReasonContentFilter) &&
		len(responses.Output) == 0 {
		return true
	}
	if images := response.ImageGenerationResponse; images != nil && len(images.Data) == 0 && images.ImageGenerationResponseParameters != nil {
		for _, finishReason := range images.FinishReasons {
			if finishReason != nil && isContentPolicyFinishReason(*finishReason) {
				return true
			}
		}
	}
	return false
}

func isContentPolicyFinishReason(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "content_filter", "content_filtered", "contentfilter", "contentfiltered", "safety", "image_safety", "unsafe_content", "prohibited_content", "image_prohibited_content", "guardrail_intervened":
		return true
	default:
		return false
	}
}

func choicesAreEmptyContentPolicyBlocks(choices []schemas.BifrostResponseChoice) bool {
	if len(choices) == 0 {
		return false
	}
	for _, choice := range choices {
		if choice.FinishReason == nil || !strings.EqualFold(strings.TrimSpace(*choice.FinishReason), "content_filter") {
			return false
		}
		if choice.TextCompletionResponseChoice != nil && choice.Text != nil && strings.TrimSpace(*choice.Text) != "" {
			return false
		}
		if message := choice.ChatNonStreamResponseChoice; message != nil && chatChoiceHasUsableOutput(message.Message) {
			return false
		}
	}
	return true
}

func chatChoiceHasUsableOutput(message *schemas.ChatMessage) bool {
	if message == nil {
		return false
	}
	if content := message.Content; content != nil {
		if content.ContentStr != nil && strings.TrimSpace(*content.ContentStr) != "" {
			return true
		}
		if len(content.ContentBlocks) > 0 {
			return true
		}
	}
	assistant := message.ChatAssistantMessage
	return assistant != nil && (assistant.Refusal != nil || assistant.Reasoning != nil || len(assistant.ReasoningDetails) > 0 ||
		len(assistant.Annotations) > 0 || len(assistant.ToolCalls) > 0 || assistant.Audio != nil)
}

type classifiedFailure struct {
	Category schemas.FailureCategory

	StatusCode *int
	ErrorTypes []string
	ErrorCodes []string
	Message    string

	Provider     schemas.ModelProvider
	BaseProvider schemas.ModelProvider
	RequestType  schemas.RequestType

	CategorySource string
	CategoryDetail string
	MatchSource    string
	MatchDetail    string
	Recognition    FailureRecognition
	RuleMatch      errorFallbackMatch
}

func classifyBifrostFailure(err *schemas.BifrostError, provider schemas.ModelProvider, requestType schemas.RequestType) classifiedFailure {
	signal := FailureSignal{Provider: provider, RequestType: requestType, Error: err}
	failure := classifiedFailureFromSignal(signal)
	recognition := RecognizeFailure(signal)
	failure.Category = recognition.Category
	failure.Recognition = recognition
	failure.CategorySource = recognition.MatchedBy
	failure.CategoryDetail = recognition.PatternID
	return failure
}

// RecognizeFailure normalizes provider-specific and multilingual failure
// signals into one stable category plus a safe explanation.
func RecognizeFailure(signal FailureSignal) FailureRecognition {
	if signal.Response != nil && responseIsEmptyContentPolicyBlock(signal.Response) {
		patternID := "empty_content_filter"
		if signal.Response.ImageGenerationResponse != nil {
			patternID = "image_finish_reason_content_filter"
		}
		return FailureRecognition{
			Category:  schemas.FailureCategoryContentPolicy,
			MatchedBy: FailureMatchedByResponseSignal,
			Pack:      "content_policy_response",
			PatternID: patternID,
		}
	}
	if signal.Error == nil {
		return FailureRecognition{Category: schemas.FailureCategoryUnknown}
	}
	failure := classifiedFailureFromSignal(signal)
	return recognizeClassifiedFailure(signal.Error, failure)
}

func classifiedFailureFromSignal(signal FailureSignal) classifiedFailure {
	failure := classifiedFailure{Category: schemas.FailureCategoryUnknown, Provider: signal.Provider, BaseProvider: signal.BaseProvider, RequestType: signal.RequestType}
	if signal.Error == nil {
		return failure
	}
	failure.StatusCode = signal.Error.StatusCode
	failure.Message = strings.ToLower(strings.TrimSpace(signal.Error.GetErrorString()))
	failure.ErrorTypes = normalizedErrorValues(signal.Error.Type)
	failure.ErrorTypes = appendUniqueStrings(failure.ErrorTypes, normalizedErrorValues(errorFieldType(signal.Error))...)
	failure.ErrorCodes = normalizedErrorValues(errorFieldCode(signal.Error))
	rawSignals := mergeRawFailureSignals(signal.Error.ExtraFields.FailureSignals, extractRawFailureSignals(signal.Error.ExtraFields.RawResponse))
	failure.ErrorTypes = appendUniqueStrings(failure.ErrorTypes, normalizedStrings(rawSignals.ErrorTypes...)...)
	failure.ErrorCodes = appendUniqueStrings(failure.ErrorCodes, normalizedStrings(rawSignals.ErrorCodes...)...)
	if rawMessage := strings.Join(normalizedStrings(rawSignals.Messages...), "\n"); rawMessage != "" {
		failure.Message = strings.TrimSpace(failure.Message + "\n" + rawMessage)
	}
	failure.Provider = signal.Provider
	if failure.Provider == "" {
		failure.Provider = signal.Error.ExtraFields.Provider
	}
	if failure.BaseProvider == "" {
		failure.BaseProvider = signal.Error.ExtraFields.BaseProvider
	}
	if failure.BaseProvider == "" {
		failure.BaseProvider = failure.Provider
	}
	failure.RequestType = signal.RequestType
	if failure.RequestType == "" {
		failure.RequestType = signal.Error.ExtraFields.RequestType
	}
	return failure
}

type rawFailureSignals = schemas.FailureRecognitionSignals

func extractRawFailureSignals(raw any) rawFailureSignals {
	return schemas.ExtractFailureRecognitionSignals(raw)
}

func mergeRawFailureSignals(groups ...rawFailureSignals) rawFailureSignals {
	return schemas.MergeFailureRecognitionSignals(groups...)
}

func captureFailureRecognitionSignals(err *schemas.BifrostError) {
	if err == nil {
		return
	}
	err.ExtraFields.FailureSignals = mergeRawFailureSignals(err.ExtraFields.FailureSignals, extractRawFailureSignals(err.ExtraFields.RawResponse))
}

func normalizedStrings(values ...string) []string {
	var normalized []string
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			normalized = appendUniqueStrings(normalized, value)
		}
	}
	return normalized
}

func recognizeClassifiedFailure(err *schemas.BifrostError, failure classifiedFailure) FailureRecognition {
	var category schemas.FailureCategory
	var match errorFallbackMatch
	switch {
	case matchDetected(detectContentPolicySignal(failure), &match):
		category = schemas.FailureCategoryContentPolicy
	case matchDetected(detectTimeoutSignal(failure), &match):
		category = schemas.FailureCategoryTimeout
	case matchDetected(detectNetworkSignal(err, failure), &match):
		category = schemas.FailureCategoryNetwork
	case matchDetected(detectAuthenticationSignal(failure), &match):
		category = schemas.FailureCategoryAuthentication
	case matchDetected(detectBillingSignal(failure), &match):
		category = schemas.FailureCategoryBilling
	case matchDetected(detectPermissionSignal(failure), &match):
		category = schemas.FailureCategoryPermission
	case matchDetected(detectRateLimitSignal(failure), &match):
		category = schemas.FailureCategoryRateLimit
	case matchDetected(detectUnsupportedOperationSignal(failure), &match):
		category = schemas.FailureCategoryUnsupportedOperation
	case failure.StatusCode != nil && (*failure.StatusCode == 502 || *failure.StatusCode == 503):
		category = schemas.FailureCategoryProviderUnavailable
		match = structuredMatch("http_status", "status_"+strconv.Itoa(*failure.StatusCode))
	case failure.StatusCode != nil && *failure.StatusCode >= 500:
		category = schemas.FailureCategoryInternal
		match = structuredMatch("http_status", "status_5xx")
	case failure.StatusCode != nil && (*failure.StatusCode == 400 || *failure.StatusCode == 404 || *failure.StatusCode == 405 || *failure.StatusCode == 409 || *failure.StatusCode == 410 || *failure.StatusCode == 422):
		category = schemas.FailureCategoryInvalidRequest
		match = structuredMatch("http_status", "status_"+strconv.Itoa(*failure.StatusCode))
	default:
		category = schemas.FailureCategoryUnknown
	}
	return FailureRecognition{Category: category, MatchedBy: match.MatchedBy, Pack: match.Pack, PatternID: match.PatternID}
}

func matchDetected(candidate errorFallbackMatch, target *errorFallbackMatch) bool {
	candidate = normalizeDetectorMatch(candidate)
	if candidate.MatchedBy == "" {
		return false
	}
	*target = candidate
	return true
}

func structuredMatch(pack, patternID string) errorFallbackMatch {
	return errorFallbackMatch{MatchedBy: FailureMatchedByStructured, Pack: pack, PatternID: patternID}
}

func normalizeDetectorMatch(match errorFallbackMatch) errorFallbackMatch {
	if match.MatchedBy != "" || match.Source == "" {
		return match
	}
	match.PatternID = match.Detail
	switch {
	case strings.HasPrefix(match.Source, "classifier.azure."):
		match.MatchedBy, match.Pack = FailureMatchedByProviderPack, "azure_content_policy"
	case strings.HasPrefix(match.Source, "classifier.openai_image."):
		match.MatchedBy, match.Pack = FailureMatchedByProviderPack, "openai_image_content_policy"
	case strings.HasPrefix(match.Source, "classifier.gemini."):
		match.MatchedBy, match.Pack = FailureMatchedByProviderPack, "gemini_safety"
	case strings.HasPrefix(match.Source, "classifier.bedrock."):
		match.MatchedBy, match.Pack = FailureMatchedByProviderPack, "bedrock_guardrail"
	case match.Source == "classifier.global.message" || match.Source == "classifier.message":
		match.MatchedBy, match.Pack = FailureMatchedByMessagePack, "global_multilingual"
	case match.Source == "classifier.global.error_code" || match.Source == "classifier.global.error_type":
		match.MatchedBy, match.Pack = FailureMatchedByStructured, "global_content_policy"
	default:
		match.MatchedBy, match.Pack = FailureMatchedByStructured, "generic_failure"
	}
	return match
}

func firstMatchingErrorFallbackRule(req *schemas.BifrostRequest, err *schemas.BifrostError, rules []schemas.ErrorFallbackRule) (*schemas.ErrorFallbackRule, classifiedFailure) {
	populateErrorFallbackExtraFields(err, req)
	provider, _, _ := req.GetRequestFields()
	failure := classifyBifrostFailure(err, provider, req.RequestType)
	for i := range rules {
		match, ok := matchErrorFallbackRule(failure, rules[i])
		if !ok {
			continue
		}
		match = normalizeRuleMatch(match)
		failure.RuleMatch = match
		failure.MatchSource = match.MatchedBy
		failure.MatchDetail = match.PatternID
		return &rules[i], failure
	}
	return nil, failure
}

func normalizeRuleMatch(match errorFallbackMatch) errorFallbackMatch {
	if match.MatchedBy != "" {
		return match
	}
	match.PatternID = strings.TrimPrefix(match.Source, "legacy_when.")
	if match.PatternID == "legacy_when" || match.PatternID == "" {
		match.PatternID = "combined_conditions"
	}
	if strings.HasPrefix(match.Source, "supplement.") {
		match.MatchedBy = FailureMatchedBySupplement
		match.PatternID = strings.TrimPrefix(match.Source, "supplement.")
		return match
	}
	if strings.HasPrefix(match.Source, "legacy_when") {
		match.MatchedBy = FailureMatchedByLegacyWhen
		return match
	}
	return normalizeDetectorMatch(match)
}

func populateErrorFallbackExtraFields(err *schemas.BifrostError, req *schemas.BifrostRequest) {
	if err == nil || req == nil {
		return
	}
	provider, model, _ := req.GetRequestFields()
	if provider == "" && err.ExtraFields.Provider == "" {
		return
	}
	if err.ExtraFields.Provider == "" || err.ExtraFields.RequestType == "" || err.ExtraFields.OriginalModelRequested == "" {
		err.PopulateExtraFields(req.RequestType, provider, model, model)
	}
}

func matchErrorFallbackRule(failure classifiedFailure, rule schemas.ErrorFallbackRule) (errorFallbackMatch, bool) {
	if ruleHasMixedMatcherShapes(rule) {
		return errorFallbackMatch{}, false
	}

	if usesScenarioMatcher(rule) {
		if rule.Scenario != "" && failure.Category == rule.Scenario {
			return errorFallbackMatch{
				MatchedBy: failure.Recognition.MatchedBy,
				Pack:      failure.Recognition.Pack,
				PatternID: failure.Recognition.PatternID,
			}, true
		}
		if match, ok := matchSupplement(failure, rule.Supplement); ok {
			return match, true
		}
		return errorFallbackMatch{}, false
	}

	return matchLegacyCondition(failure, rule.When)
}

func usesScenarioMatcher(rule schemas.ErrorFallbackRule) bool {
	return strings.TrimSpace(string(rule.Scenario)) != "" || rule.Supplement != nil
}

func ruleHasMixedMatcherShapes(rule schemas.ErrorFallbackRule) bool {
	return usesScenarioMatcher(rule) && conditionHasMatchers(rule.When)
}

func matchLegacyCondition(failure classifiedFailure, condition schemas.ErrorFallbackCondition) (errorFallbackMatch, bool) {
	if !conditionHasMatchers(condition) {
		return errorFallbackMatch{}, false
	}
	if len(condition.Categories) > 0 && !containsFailureCategory(condition.Categories, failure.Category) {
		return errorFallbackMatch{}, false
	}
	if len(condition.StatusCodes) > 0 {
		if failure.StatusCode == nil || !containsInt(condition.StatusCodes, *failure.StatusCode) {
			return errorFallbackMatch{}, false
		}
	}
	if len(condition.ErrorTypes) > 0 && !containsAnyNormalized(condition.ErrorTypes, failure.ErrorTypes) {
		return errorFallbackMatch{}, false
	}
	if len(condition.ErrorCodes) > 0 && !containsAnyNormalized(condition.ErrorCodes, failure.ErrorCodes) {
		return errorFallbackMatch{}, false
	}
	if len(condition.MessageContains) > 0 {
		if _, ok := firstMatchingSubstring(failure.Message, condition.MessageContains); !ok {
			return errorFallbackMatch{}, false
		}
	}

	switch {
	case len(condition.Categories) > 0:
		return errorFallbackMatch{Source: "legacy_when.categories", Detail: string(failure.Category)}, true
	case len(condition.ErrorCodes) > 0:
		if hit, ok := firstNormalizedHit(condition.ErrorCodes, failure.ErrorCodes); ok {
			return errorFallbackMatch{Source: "legacy_when.error_codes", Detail: hit}, true
		}
	case len(condition.ErrorTypes) > 0:
		if hit, ok := firstNormalizedHit(condition.ErrorTypes, failure.ErrorTypes); ok {
			return errorFallbackMatch{Source: "legacy_when.error_types", Detail: hit}, true
		}
	case len(condition.StatusCodes) > 0 && failure.StatusCode != nil:
		return errorFallbackMatch{Source: "legacy_when.status_codes", Detail: strconv.Itoa(*failure.StatusCode)}, true
	case len(condition.MessageContains) > 0:
		if _, ok := firstMatchingSubstring(failure.Message, condition.MessageContains); ok {
			return errorFallbackMatch{Source: "legacy_when.message_contains", Detail: "configured_phrase"}, true
		}
	}

	return errorFallbackMatch{Source: "legacy_when", Detail: "matched"}, true
}

func matchSupplement(failure classifiedFailure, supplement *schemas.ErrorFallbackSupplement) (errorFallbackMatch, bool) {
	if supplement == nil || !supplementHasMatchers(*supplement) {
		return errorFallbackMatch{}, false
	}
	if len(supplement.Providers) > 0 && !containsProvider(supplement.Providers, failure.Provider) {
		return errorFallbackMatch{}, false
	}
	if hit, ok := firstNormalizedHit(supplement.ErrorCodes, failure.ErrorCodes); ok {
		return errorFallbackMatch{Source: "supplement.error_codes", Detail: hit}, true
	}
	if hit, ok := firstNormalizedHit(supplement.ErrorTypes, failure.ErrorTypes); ok {
		return errorFallbackMatch{Source: "supplement.error_types", Detail: hit}, true
	}
	if failure.StatusCode != nil && containsInt(supplement.StatusCodes, *failure.StatusCode) {
		return errorFallbackMatch{Source: "supplement.status_codes", Detail: strconv.Itoa(*failure.StatusCode)}, true
	}
	if _, ok := firstMatchingSubstring(failure.Message, supplement.MessageContainsAny); ok {
		return errorFallbackMatch{Source: "supplement.message_contains_any", Detail: "configured_phrase"}, true
	}
	return errorFallbackMatch{}, false
}

func conditionHasMatchers(condition schemas.ErrorFallbackCondition) bool {
	return len(condition.Categories) > 0 ||
		len(condition.ErrorCodes) > 0 ||
		len(condition.ErrorTypes) > 0 ||
		len(condition.StatusCodes) > 0 ||
		len(condition.MessageContains) > 0
}

func supplementHasMatchers(supplement schemas.ErrorFallbackSupplement) bool {
	return len(supplement.ErrorCodes) > 0 ||
		len(supplement.ErrorTypes) > 0 ||
		len(supplement.StatusCodes) > 0 ||
		len(supplement.MessageContainsAny) > 0
}

func sanitizeMatchedErrorFallbackChain(primaryProvider schemas.ModelProvider, primaryModel string, fallbacks []schemas.Fallback) []schemas.Fallback {
	excluded := map[string]struct{}{}
	if primaryKey := fallbackTargetKey(primaryProvider, primaryModel); primaryKey != "" {
		excluded[primaryKey] = struct{}{}
	}
	return sanitizeFallbackChain(fallbacks, excluded)
}

func sanitizeFallbackChain(fallbacks []schemas.Fallback, excluded map[string]struct{}) []schemas.Fallback {
	if len(fallbacks) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fallbacks))
	filtered := make([]schemas.Fallback, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		targetKey := fallbackTargetKey(fallback.Provider, fallback.Model)
		if targetKey == "" {
			continue
		}
		if _, skip := excluded[targetKey]; skip {
			continue
		}
		if _, exists := seen[targetKey]; exists {
			continue
		}
		seen[targetKey] = struct{}{}
		filtered = append(filtered, fallback)
	}
	return filtered
}

func fallbackTargetKey(provider schemas.ModelProvider, model string) string {
	if provider == "" {
		return ""
	}
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		return ""
	}
	return string(provider) + "\x00" + trimmedModel
}

func detectContentPolicySignal(failure classifiedFailure) errorFallbackMatch {
	packProvider := failure.BaseProvider
	if packProvider == "" {
		packProvider = failure.Provider
	}
	if packProvider == schemas.Azure || packProvider == schemas.OpenAI {
		if hit, ok := firstMessagePhrase(failure.Message, []safePhraseMatcher{
			{id: "azure_safety_system", phrases: []string{"rejected by the safety system", "safety_violations=", "azure support ticket", "被安全系统拒绝", "安全系统拒绝"}},
		}); ok {
			return errorFallbackMatch{Source: "classifier.azure.message", Detail: hit}
		}
	}
	if packProvider == schemas.OpenAI {
		if isImageRequestType(failure.RequestType) {
			if hit, ok := firstNormalizedHit([]string{"content_filtered", "unsafe_content", "moderation_blocked"}, failure.ErrorCodes); ok {
				return errorFallbackMatch{Source: "classifier.openai_image.error_code", Detail: hit}
			}
			if hit, ok := firstNormalizedHit([]string{"content_filter", "unsafe_content"}, failure.ErrorTypes); ok {
				return errorFallbackMatch{Source: "classifier.openai_image.error_type", Detail: hit}
			}
			if hit, ok := firstMessagePhrase(failure.Message, []safePhraseMatcher{
				{id: "unsafe_image", phrases: []string{"generated images appear to be unsafe", "appear to be unsafe", "生成的图片可能不安全", "图片可能不安全"}},
			}); ok {
				return errorFallbackMatch{Source: "classifier.openai_image.message", Detail: hit}
			}
		}
	}

	if packProvider == schemas.Gemini {
		if hit, ok := firstNormalizedHit([]string{
			"safety",
			"image_safety",
			"prohibited_content",
			"image_prohibited_content",
			"spii",
			"blocklist",
			"recitation",
		}, failure.ErrorCodes); ok {
			return errorFallbackMatch{Source: "classifier.gemini.error_code", Detail: hit}
		}
		if hit, ok := firstNormalizedHit([]string{
			"safety",
			"image_safety",
			"prohibited_content",
			"image_prohibited_content",
			"spii",
			"blocklist",
			"recitation",
		}, failure.ErrorTypes); ok {
			return errorFallbackMatch{Source: "classifier.gemini.error_type", Detail: hit}
		}
	}

	if packProvider == schemas.Bedrock {
		if hit, ok := firstNormalizedHit([]string{"content_filtered", "guardrail_intervened"}, failure.ErrorCodes); ok {
			return errorFallbackMatch{Source: "classifier.bedrock.error_code", Detail: hit}
		}
		if hit, ok := firstNormalizedHit([]string{"content_filtered", "guardrail_intervened"}, failure.ErrorTypes); ok {
			return errorFallbackMatch{Source: "classifier.bedrock.error_type", Detail: hit}
		}
		if hit, ok := firstMessagePhrase(failure.Message, []safePhraseMatcher{
			{id: "guardrail_intervened", phrases: []string{"guardrail intervened", "guardrail blocked", "内容护栏", "护栏拦截"}},
		}); ok {
			return errorFallbackMatch{Source: "classifier.bedrock.message", Detail: hit}
		}
	}

	if hit, ok := firstNormalizedHit([]string{
		"content_filter",
		"content_filtered",
		"content_policy",
		"content_policy_error",
		"content_policy_violation",
		"safety_violation",
		"safety_violations",
		"responsible_ai_policy_violation",
		"unsafe_content",
		"moderation_blocked",
	}, failure.ErrorCodes); ok {
		return errorFallbackMatch{Source: "classifier.global.error_code", Detail: hit}
	}
	if hit, ok := firstNormalizedHit([]string{
		"content_filter",
		"content_filtered",
		"content_policy",
		"content_policy_error",
		"content_policy_violation",
		"safety_violation",
		"safety_violations",
		"responsible_ai_policy_violation",
		"unsafe_content",
	}, failure.ErrorTypes); ok {
		return errorFallbackMatch{Source: "classifier.global.error_type", Detail: hit}
	}
	if hit, ok := firstMessagePhrase(failure.Message, []safePhraseMatcher{
		{id: "rejected_by_safety_system", phrases: []string{"rejected by the safety system", "被安全系统拒绝", "安全系统拒绝"}},
		{id: "safety_violations", phrases: []string{"safety_violations="}},
		{id: "unsafe_image", phrases: []string{"generated images appear to be unsafe", "appear to be unsafe", "生成的图片可能不安全", "图片可能不安全"}},
		{id: "content_filtered", phrases: []string{"content was filtered", "content filtered", "内容已被过滤", "内容被过滤"}},
		{id: "content_policy", phrases: []string{"content policy", "responsible ai policy", "内容安全", "内容政策", "安全策略"}},
		{id: "prohibited_content", phrases: []string{"prohibited content", "违规内容", "禁止内容"}},
		{id: "sexual_content_guardrail", phrases: []string{"裸露、色情或情色内容", "色情或情色内容的防护限制", "nudity, sexual, or erotic content", "sexual or erotic content safeguards"}},
	}); ok {
		return errorFallbackMatch{Source: "classifier.global.message", Detail: hit}
	}
	return errorFallbackMatch{}
}

func detectTimeoutSignal(failure classifiedFailure) errorFallbackMatch {
	if failure.StatusCode != nil && (*failure.StatusCode == 408 || *failure.StatusCode == 504) {
		return errorFallbackMatch{Source: "classifier.status_code", Detail: strconv.Itoa(*failure.StatusCode)}
	}
	if hit, ok := firstMessagePhrase(failure.Message, []safePhraseMatcher{
		{id: "timeout", phrases: []string{"timeout", "timed out", "deadline exceeded"}},
	}); ok {
		return errorFallbackMatch{Source: "classifier.message", Detail: hit}
	}
	return errorFallbackMatch{}
}

func detectNetworkSignal(err *schemas.BifrostError, failure classifiedFailure) errorFallbackMatch {
	if err == nil || err.Error == nil {
		return errorFallbackMatch{}
	}
	if err.Error.Message == schemas.ErrProviderDoRequest || err.Error.Message == schemas.ErrProviderNetworkError {
		return errorFallbackMatch{Source: "classifier.network_error", Detail: "provider_network_error"}
	}
	if hit, ok := firstMessagePhrase(failure.Message, []safePhraseMatcher{
		{id: "network", phrases: []string{"connection reset", "connection refused", "temporary failure in name resolution"}},
	}); ok {
		return errorFallbackMatch{Source: "classifier.message", Detail: hit}
	}
	return errorFallbackMatch{}
}

func detectAuthenticationSignal(failure classifiedFailure) errorFallbackMatch {
	if failure.StatusCode != nil && *failure.StatusCode == 401 {
		return errorFallbackMatch{Source: "classifier.status_code", Detail: "401"}
	}
	if hit, ok := firstNormalizedHit([]string{"authentication_error", "auth_error", "invalid_api_key", "unauthorized"}, failure.ErrorTypes); ok {
		return errorFallbackMatch{Source: "classifier.error_type", Detail: hit}
	}
	if hit, ok := firstNormalizedHit([]string{"invalid_api_key", "unauthorized"}, failure.ErrorCodes); ok {
		return errorFallbackMatch{Source: "classifier.error_code", Detail: hit}
	}
	return errorFallbackMatch{}
}

func detectBillingSignal(failure classifiedFailure) errorFallbackMatch {
	if failure.StatusCode != nil && *failure.StatusCode == 402 {
		return errorFallbackMatch{Source: "classifier.status_code", Detail: "402"}
	}
	if hit, ok := firstNormalizedHit([]string{"billing_error", "insufficient_quota", "quota_exceeded"}, failure.ErrorTypes); ok {
		return errorFallbackMatch{Source: "classifier.error_type", Detail: hit}
	}
	if hit, ok := firstNormalizedHit([]string{"insufficient_quota", "quota_exceeded"}, failure.ErrorCodes); ok {
		return errorFallbackMatch{Source: "classifier.error_code", Detail: hit}
	}
	return errorFallbackMatch{}
}

func detectPermissionSignal(failure classifiedFailure) errorFallbackMatch {
	if failure.StatusCode != nil && *failure.StatusCode == 403 {
		return errorFallbackMatch{Source: "classifier.status_code", Detail: "403"}
	}
	if hit, ok := firstNormalizedHit([]string{"permission_error", "forbidden"}, failure.ErrorTypes); ok {
		return errorFallbackMatch{Source: "classifier.error_type", Detail: hit}
	}
	if hit, ok := firstNormalizedHit([]string{"forbidden"}, failure.ErrorCodes); ok {
		return errorFallbackMatch{Source: "classifier.error_code", Detail: hit}
	}
	return errorFallbackMatch{}
}

func detectRateLimitSignal(failure classifiedFailure) errorFallbackMatch {
	if failure.StatusCode != nil && *failure.StatusCode == 429 {
		return errorFallbackMatch{Source: "classifier.status_code", Detail: "429"}
	}
	for _, value := range append(append([]string(nil), failure.ErrorTypes...), failure.ErrorCodes...) {
		if IsRateLimitErrorMessage(value) {
			return errorFallbackMatch{Source: "classifier.rate_limit_signal", Detail: strings.ToLower(strings.TrimSpace(value))}
		}
	}
	if IsRateLimitErrorMessage(failure.Message) {
		return errorFallbackMatch{Source: "classifier.rate_limit_signal", Detail: "message"}
	}
	return errorFallbackMatch{}
}

func detectUnsupportedOperationSignal(failure classifiedFailure) errorFallbackMatch {
	if hit, ok := firstMessagePhrase(failure.Message, []safePhraseMatcher{
		{id: "unsupported_operation", phrases: []string{"not supported", "unsupported", "does not support", "only supports", "is not available for this model"}},
	}); ok {
		return errorFallbackMatch{Source: "classifier.message", Detail: hit}
	}
	return errorFallbackMatch{}
}

func isImageRequestType(requestType schemas.RequestType) bool {
	return requestType == schemas.ImageGenerationRequest ||
		requestType == schemas.ImageGenerationStreamRequest ||
		requestType == schemas.ImageEditRequest ||
		requestType == schemas.ImageEditStreamRequest ||
		requestType == schemas.ImageVariationRequest
}

func errorFieldType(err *schemas.BifrostError) *string {
	if err == nil || err.Error == nil {
		return nil
	}
	return err.Error.Type
}

func errorFieldCode(err *schemas.BifrostError) *string {
	if err == nil || err.Error == nil {
		return nil
	}
	return err.Error.Code
}

func normalizedErrorValues(values ...*string) []string {
	var out []string
	for _, value := range values {
		if value == nil {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(*value))
		if normalized == "" {
			continue
		}
		out = appendUniqueStrings(out, normalized)
	}
	return out
}

func appendUniqueStrings(dst []string, values ...string) []string {
	for _, value := range values {
		if value == "" || containsString(dst, value) {
			continue
		}
		dst = append(dst, value)
	}
	return dst
}

func containsAnyNormalized(needles []string, haystack []string) bool {
	_, ok := firstNormalizedHit(needles, haystack)
	return ok
}

func firstNormalizedHit(needles []string, haystack []string) (string, bool) {
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle == "" {
			continue
		}
		if containsString(haystack, needle) {
			return needle, true
		}
	}
	return "", false
}

func firstMatchingSubstring(message string, fragments []string) (string, bool) {
	for _, fragment := range fragments {
		normalized := strings.ToLower(strings.TrimSpace(fragment))
		if normalized != "" && strings.Contains(message, normalized) {
			return normalized, true
		}
	}
	return "", false
}

func firstMessagePhrase(message string, matchers []safePhraseMatcher) (string, bool) {
	for _, matcher := range matchers {
		for _, phrase := range matcher.phrases {
			normalized := strings.ToLower(strings.TrimSpace(phrase))
			if normalized != "" && strings.Contains(message, normalized) {
				return matcher.id, true
			}
		}
	}
	return "", false
}

func containsProvider(haystack []schemas.ModelProvider, needle schemas.ModelProvider) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

func containsFailureCategory(haystack []schemas.FailureCategory, needle schemas.FailureCategory) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

func containsInt(haystack []int, needle int) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}

func containsString(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
