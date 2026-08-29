package bifrost

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const (
	failureMatchedByResponseSignal = "response_signal"
	failureMatchedByStructured     = "structured"
	failureMatchedByProviderPack   = "provider_pack"
	failureMatchedByMessagePack    = "message_pack"
	failureMatchedBySupplement     = "supplement"
	failureMatchedByLegacyWhen     = "legacy_when"
)

type safePhraseMatcher struct {
	id      string
	phrases []string
}

type errorFallbackMatch struct {
	matchedBy string
	pack      string
	patternID string
}

type classifiedFailure struct {
	category     schemas.FailureCategory
	statusCode   *int
	errorTypes   []string
	errorCodes   []string
	message      string
	provider     schemas.ModelProvider
	baseProvider schemas.ModelProvider
	requestType  schemas.RequestType
	match        errorFallbackMatch
}

// FailureSignal is the provider-neutral input to failure recognition. A response
// may carry a successful HTTP status while still reporting a content-policy block.
type FailureSignal struct {
	Provider     schemas.ModelProvider
	BaseProvider schemas.ModelProvider
	RequestType  schemas.RequestType
	Error        *schemas.BifrostError
	Response     *schemas.BifrostResponse
}

// FailureRecognition exposes only stable classifier identifiers; raw provider
// messages never enter routing logs or trace attributes.
type FailureRecognition struct {
	Category  schemas.FailureCategory
	MatchedBy string
	Pack      string
	PatternID string
}

func RecognizeFailure(signal FailureSignal) FailureRecognition {
	if responseIsEmptyContentPolicyBlock(signal.Response) {
		pattern := "empty_content_filter"
		if signal.Response != nil && signal.Response.ImageGenerationResponse != nil {
			pattern = "image_finish_reason_content_filter"
		}
		return FailureRecognition{Category: schemas.FailureCategoryContentPolicy, MatchedBy: failureMatchedByResponseSignal, Pack: "content_policy_response", PatternID: pattern}
	}
	if signal.Error == nil {
		return FailureRecognition{Category: schemas.FailureCategoryUnknown}
	}
	failure := classifiedFailureFromSignal(signal)
	category, match := recognizeClassifiedFailure(signal.Error, failure)
	return FailureRecognition{Category: category, MatchedBy: match.matchedBy, Pack: match.pack, PatternID: match.patternID}
}

func classifiedFailureFromSignal(signal FailureSignal) classifiedFailure {
	failure := classifiedFailure{
		category:     schemas.FailureCategoryUnknown,
		provider:     signal.Provider,
		baseProvider: signal.BaseProvider,
		requestType:  signal.RequestType,
	}
	if signal.Error == nil {
		return failure
	}
	err := signal.Error
	failure.statusCode = err.StatusCode
	failure.message = strings.ToLower(strings.TrimSpace(err.GetErrorString()))
	failure.errorTypes = normalizedErrorValues(err.Type, errorFieldType(err))
	failure.errorCodes = normalizedErrorValues(errorFieldCode(err))
	rawSignals := schemas.MergeFailureRecognitionSignals(err.ExtraFields.FailureSignals, schemas.ExtractFailureRecognitionSignals(err.ExtraFields.RawResponse))
	failure.errorTypes = appendUniqueStrings(failure.errorTypes, normalizedStrings(rawSignals.ErrorTypes...)...)
	failure.errorCodes = appendUniqueStrings(failure.errorCodes, normalizedStrings(rawSignals.ErrorCodes...)...)
	if rawMessage := strings.Join(normalizedStrings(rawSignals.Messages...), "\n"); rawMessage != "" {
		failure.message = strings.TrimSpace(failure.message + "\n" + rawMessage)
	}
	if failure.provider == "" {
		failure.provider = err.ExtraFields.Provider
	}
	if failure.baseProvider == "" {
		failure.baseProvider = err.ExtraFields.BaseProvider
	}
	if failure.baseProvider == "" {
		failure.baseProvider = failure.provider
	}
	if failure.requestType == "" {
		failure.requestType = err.ExtraFields.RequestType
	}
	return failure
}

func classifyBifrostFailure(err *schemas.BifrostError, provider schemas.ModelProvider, requestType schemas.RequestType) classifiedFailure {
	failure := classifiedFailureFromSignal(FailureSignal{Provider: provider, RequestType: requestType, Error: err})
	failure.category, failure.match = recognizeClassifiedFailure(err, failure)
	return failure
}

func recognizeClassifiedFailure(err *schemas.BifrostError, failure classifiedFailure) (schemas.FailureCategory, errorFallbackMatch) {
	if containsString(failure.errorTypes, "invalid_image_response") || containsString(failure.errorCodes, "invalid_image_response") {
		return schemas.FailureCategoryProviderUnavailable, structuredMatch("image_response", "invalid_image_response")
	}
	if match := detectContentPolicySignal(failure); match.matchedBy != "" {
		return schemas.FailureCategoryContentPolicy, match
	}
	if match := detectTimeoutSignal(failure); match.matchedBy != "" {
		return schemas.FailureCategoryTimeout, match
	}
	if match := detectNetworkSignal(err, failure); match.matchedBy != "" {
		return schemas.FailureCategoryNetwork, match
	}
	if match := detectAuthenticationSignal(failure); match.matchedBy != "" {
		return schemas.FailureCategoryAuthentication, match
	}
	if match := detectBillingSignal(failure); match.matchedBy != "" {
		return schemas.FailureCategoryBilling, match
	}
	if match := detectPermissionSignal(failure); match.matchedBy != "" {
		return schemas.FailureCategoryPermission, match
	}
	if match := detectRateLimitSignal(failure); match.matchedBy != "" {
		return schemas.FailureCategoryRateLimit, match
	}
	if match := detectUnsupportedOperationSignal(failure); match.matchedBy != "" {
		return schemas.FailureCategoryUnsupportedOperation, match
	}
	if failure.statusCode != nil {
		switch status := *failure.statusCode; {
		case status == http.StatusBadGateway || status == http.StatusServiceUnavailable:
			return schemas.FailureCategoryProviderUnavailable, structuredMatch("http_status", "status_"+strconv.Itoa(status))
		case status >= 500:
			return schemas.FailureCategoryInternal, structuredMatch("http_status", "status_5xx")
		case status == 400 || status == 404 || status == 405 || status == 409 || status == 410 || status == 422:
			return schemas.FailureCategoryInvalidRequest, structuredMatch("http_status", "status_"+strconv.Itoa(status))
		}
	}
	return schemas.FailureCategoryUnknown, errorFallbackMatch{}
}

func firstMatchingErrorFallbackRule(req *schemas.BifrostRequest, err *schemas.BifrostError, rules []schemas.ErrorFallbackRule) (*schemas.ErrorFallbackRule, classifiedFailure) {
	// Error fallbacks are failure-only policy. In particular, a successful
	// request must not be treated as the synthetic "unknown" failure category.
	if err == nil {
		return nil, classifiedFailure{}
	}
	populateErrorFallbackExtraFields(err, req)
	provider, _, _ := req.GetRequestFields()
	failure := classifyBifrostFailure(err, provider, req.RequestType)
	for i := range rules {
		match, ok := matchErrorFallbackRule(failure, rules[i])
		if ok {
			failure.match = match
			return &rules[i], failure
		}
	}
	return nil, failure
}

func matchErrorFallbackRule(failure classifiedFailure, rule schemas.ErrorFallbackRule) (errorFallbackMatch, bool) {
	if usesScenarioMatcher(rule) && conditionHasMatchers(rule.When) {
		return errorFallbackMatch{}, false
	}
	if usesScenarioMatcher(rule) {
		if rule.Scenario != "" && failure.category == rule.Scenario {
			return failure.match, true
		}
		return matchSupplement(failure, rule.Supplement)
	}
	return matchLegacyCondition(failure, rule.When)
}

func usesScenarioMatcher(rule schemas.ErrorFallbackRule) bool {
	return strings.TrimSpace(string(rule.Scenario)) != "" || rule.Supplement != nil
}

func matchLegacyCondition(failure classifiedFailure, condition schemas.ErrorFallbackCondition) (errorFallbackMatch, bool) {
	if !conditionHasMatchers(condition) ||
		(len(condition.Categories) > 0 && !containsFailureCategory(condition.Categories, failure.category)) ||
		(len(condition.StatusCodes) > 0 && (failure.statusCode == nil || !containsInt(condition.StatusCodes, *failure.statusCode))) ||
		(len(condition.ErrorTypes) > 0 && !containsAnyNormalized(condition.ErrorTypes, failure.errorTypes)) ||
		(len(condition.ErrorCodes) > 0 && !containsAnyNormalized(condition.ErrorCodes, failure.errorCodes)) {
		return errorFallbackMatch{}, false
	}
	if len(condition.MessageContains) > 0 {
		if _, ok := firstMatchingSubstring(failure.message, condition.MessageContains); !ok {
			return errorFallbackMatch{}, false
		}
	}
	pattern := "combined_conditions"
	switch {
	case len(condition.Categories) > 0:
		pattern = "categories"
	case len(condition.ErrorCodes) > 0:
		pattern = "error_codes"
	case len(condition.ErrorTypes) > 0:
		pattern = "error_types"
	case len(condition.StatusCodes) > 0:
		pattern = "status_codes"
	case len(condition.MessageContains) > 0:
		pattern = "message_contains"
	}
	return errorFallbackMatch{matchedBy: failureMatchedByLegacyWhen, pack: "configured", patternID: pattern}, true
}

func matchSupplement(failure classifiedFailure, supplement *schemas.ErrorFallbackSupplement) (errorFallbackMatch, bool) {
	if supplement == nil || !supplementHasMatchers(*supplement) ||
		(len(supplement.Providers) > 0 && !containsProvider(supplement.Providers, failure.provider)) {
		return errorFallbackMatch{}, false
	}
	if hit, ok := firstNormalizedHit(supplement.ErrorCodes, failure.errorCodes); ok {
		return errorFallbackMatch{matchedBy: failureMatchedBySupplement, pack: "configured", patternID: "error_code:" + hit}, true
	}
	if hit, ok := firstNormalizedHit(supplement.ErrorTypes, failure.errorTypes); ok {
		return errorFallbackMatch{matchedBy: failureMatchedBySupplement, pack: "configured", patternID: "error_type:" + hit}, true
	}
	if failure.statusCode != nil && containsInt(supplement.StatusCodes, *failure.statusCode) {
		return errorFallbackMatch{matchedBy: failureMatchedBySupplement, pack: "configured", patternID: "status_code:" + strconv.Itoa(*failure.statusCode)}, true
	}
	if _, ok := firstMatchingSubstring(failure.message, supplement.MessageContainsAny); ok {
		return errorFallbackMatch{matchedBy: failureMatchedBySupplement, pack: "configured", patternID: "message_contains_any"}, true
	}
	return errorFallbackMatch{}, false
}

func (bifrost *Bifrost) resolveFallbackChain(req *schemas.BifrostRequest, primaryErr *schemas.BifrostError) ([]schemas.Fallback, []schemas.Fallback, *schemas.ErrorFallbackRule, classifiedFailure) {
	provider, model, ordinary := req.GetRequestFields()
	attempted := attemptedFallbackTargets(provider, model)
	ordinary = sanitizeFallbackChain(ordinary, attempted)
	rule, failure := firstMatchingErrorFallbackRule(req, primaryErr, req.GetErrorFallbacks())
	if rule != nil {
		return sanitizeFallbackChain(rule.Fallbacks, attempted), ordinary, rule, failure
	}
	return ordinary, ordinary, nil, failure
}

func attemptedFallbackTargets(provider schemas.ModelProvider, model string) map[string]struct{} {
	attempted := make(map[string]struct{})
	if key := fallbackTargetKey(provider, model); key != "" {
		attempted[key] = struct{}{}
	}
	return attempted
}

func markFallbackTargetAttempted(attempted map[string]struct{}, fallback schemas.Fallback) {
	if key := fallbackTargetKey(fallback.Provider, fallback.Model); key != "" {
		attempted[key] = struct{}{}
	}
}

func sanitizeFallbackChain(fallbacks []schemas.Fallback, excluded map[string]struct{}) []schemas.Fallback {
	seen := make(map[string]struct{}, len(fallbacks))
	filtered := make([]schemas.Fallback, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		key := fallbackTargetKey(fallback.Provider, fallback.Model)
		if key == "" {
			continue
		}
		if _, skip := excluded[key]; skip {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, fallback)
	}
	return filtered
}

func fallbackTargetKey(provider schemas.ModelProvider, model string) string {
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return ""
	}
	return string(provider) + "\x00" + model
}

func errorFallbackRuleLabel(rule *schemas.ErrorFallbackRule) string {
	if rule == nil {
		return ""
	}
	if name := strings.TrimSpace(rule.Name); name != "" {
		return name
	}
	return "unnamed"
}

func recordErrorFallbackDecision(ctx *schemas.BifrostContext, rule *schemas.ErrorFallbackRule, failure classifiedFailure) {
	if ctx == nil {
		return
	}
	clearErrorFallbackDecision(ctx)
	if rule == nil {
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackRuleName, errorFallbackRuleLabel(rule))
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackCategory, string(failure.category))
	if failure.match.matchedBy != "" {
		ctx.SetValue(schemas.BifrostContextKeyErrorFallbackMatchedBy, failure.match.matchedBy)
		ctx.SetValue(schemas.BifrostContextKeyErrorFallbackMatchSource, failure.match.matchedBy)
	}
	if failure.match.pack != "" {
		ctx.SetValue(schemas.BifrostContextKeyErrorFallbackPack, failure.match.pack)
	}
	if failure.match.patternID != "" {
		ctx.SetValue(schemas.BifrostContextKeyErrorFallbackPatternID, failure.match.patternID)
		ctx.SetValue(schemas.BifrostContextKeyErrorFallbackMatchDetail, failure.match.patternID)
	}
}

func clearErrorFallbackDecision(ctx *schemas.BifrostContext) {
	if ctx == nil {
		return
	}
	ctx.ClearValue(schemas.BifrostContextKeyErrorFallbackRuleName)
	ctx.ClearValue(schemas.BifrostContextKeyErrorFallbackCategory)
	ctx.ClearValue(schemas.BifrostContextKeyErrorFallbackMatchSource)
	ctx.ClearValue(schemas.BifrostContextKeyErrorFallbackMatchDetail)
	ctx.ClearValue(schemas.BifrostContextKeyErrorFallbackMatchedBy)
	ctx.ClearValue(schemas.BifrostContextKeyErrorFallbackPack)
	ctx.ClearValue(schemas.BifrostContextKeyErrorFallbackPatternID)
}

func setErrorFallbackSpanAttributes(tracer schemas.Tracer, handle schemas.SpanHandle, ctx *schemas.BifrostContext) {
	if tracer == nil || ctx == nil {
		return
	}
	attributes := [...]struct {
		contextKey schemas.BifrostContextKey
		traceKey   string
	}{
		{schemas.BifrostContextKeyErrorFallbackRuleName, schemas.AttrBifrostErrorFallbackRule},
		{schemas.BifrostContextKeyErrorFallbackCategory, schemas.AttrBifrostErrorFallbackCategory},
		{schemas.BifrostContextKeyErrorFallbackMatchSource, schemas.AttrBifrostErrorFallbackMatchSource},
		{schemas.BifrostContextKeyErrorFallbackMatchDetail, schemas.AttrBifrostErrorFallbackMatchDetail},
		{schemas.BifrostContextKeyErrorFallbackMatchedBy, schemas.AttrBifrostErrorFallbackMatchedBy},
		{schemas.BifrostContextKeyErrorFallbackPack, schemas.AttrBifrostErrorFallbackPack},
		{schemas.BifrostContextKeyErrorFallbackPatternID, schemas.AttrBifrostErrorFallbackPatternID},
	}
	for _, attribute := range attributes {
		if value, ok := ctx.Value(attribute.contextKey).(string); ok && value != "" {
			tracer.SetAttribute(handle, attribute.traceKey, value)
		}
	}
}

func successfulContentPolicyError(req *schemas.BifrostRequest, response *schemas.BifrostResponse) *schemas.BifrostError {
	if req == nil || response == nil || len(req.GetErrorFallbacks()) == 0 || RecognizeFailure(FailureSignal{Response: response}).Category != schemas.FailureCategoryContentPolicy {
		return nil
	}
	allowFallbacks, statusCode, errorType := true, http.StatusBadRequest, "content_filter"
	err := &schemas.BifrostError{StatusCode: &statusCode, Type: &errorType, AllowFallbacks: &allowFallbacks, Error: &schemas.ErrorField{Type: &errorType, Code: &errorType, Message: "upstream provider blocked the response due to content policy"}}
	populateErrorFallbackExtraFields(err, req)
	if rule, _ := firstMatchingErrorFallbackRule(req, err, req.GetErrorFallbacks()); rule == nil {
		return nil
	}
	return err
}

func responseIsEmptyContentPolicyBlock(response *schemas.BifrostResponse) bool {
	if response == nil {
		return false
	}
	if response.ChatResponse != nil && choicesAreEmptyContentPolicyBlocks(response.ChatResponse.Choices) {
		return true
	}
	if response.TextCompletionResponse != nil && choicesAreEmptyContentPolicyBlocks(response.TextCompletionResponse.Choices) {
		return true
	}
	if responses := response.ResponsesResponse; responses != nil && responses.IncompleteDetails != nil && strings.EqualFold(strings.TrimSpace(responses.IncompleteDetails.Reason), schemas.ResponsesResponseIncompleteReasonContentFilter) && len(responses.Output) == 0 {
		return true
	}
	if images := response.ImageGenerationResponse; images != nil && len(images.Data) == 0 && images.ImageGenerationResponseParameters != nil {
		for _, reason := range images.FinishReasons {
			if reason != nil && isContentPolicyFinishReason(*reason) {
				return true
			}
		}
	}
	return false
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
		if chat := choice.ChatNonStreamResponseChoice; chat != nil && chatChoiceHasUsableOutput(chat.Message) {
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

func isContentPolicyFinishReason(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "content_filter", "content_filtered", "contentfilter", "contentfiltered", "safety", "image_safety", "unsafe_content", "prohibited_content", "image_prohibited_content", "guardrail_intervened":
		return true
	default:
		return false
	}
}

func populateErrorFallbackExtraFields(err *schemas.BifrostError, req *schemas.BifrostRequest) {
	if err == nil || req == nil {
		return
	}
	provider, model, _ := req.GetRequestFields()
	if err.ExtraFields.Provider == "" || err.ExtraFields.RequestType == "" || err.ExtraFields.OriginalModelRequested == "" {
		err.PopulateExtraFields(req.RequestType, provider, model, model)
	}
}

func captureFailureRecognitionSignals(err *schemas.BifrostError) {
	if err == nil {
		return
	}
	err.ExtraFields.FailureSignals = schemas.MergeFailureRecognitionSignals(
		err.ExtraFields.FailureSignals,
		schemas.ExtractFailureRecognitionSignals(err.ExtraFields.RawResponse),
	)
}

func detectContentPolicySignal(failure classifiedFailure) errorFallbackMatch {
	if hit, ok := firstNormalizedHit([]string{"content_filter", "content_filtered", "content_policy", "content_policy_error", "content_policy_violation", "safety_violation", "safety_violations", "responsible_ai_policy_violation", "unsafe_content", "prohibited_content", "image_prohibited_content", "guardrail_intervened"}, append(append([]string(nil), failure.errorCodes...), failure.errorTypes...)); ok {
		return errorFallbackMatch{matchedBy: failureMatchedByStructured, pack: "content_policy", patternID: hit}
	}
	if id, ok := firstMessagePhrase(failure.message, []safePhraseMatcher{
		{id: "safety_system", phrases: []string{"rejected by the safety system", "被安全系统拒绝", "安全系统拒绝"}},
		{id: "unsafe_image", phrases: []string{"generated images appear to be unsafe", "appear to be unsafe", "图片可能不安全"}},
		{id: "content_policy", phrases: []string{"content policy", "responsible ai policy", "内容安全", "安全策略", "guardrail intervened"}},
	}); ok {
		pack := "global_multilingual"
		matchedBy := failureMatchedByMessagePack
		if failure.baseProvider == schemas.Azure || failure.baseProvider == schemas.OpenAI || failure.baseProvider == schemas.Gemini || failure.baseProvider == schemas.Bedrock {
			pack = string(failure.baseProvider) + "_content_policy"
			matchedBy = failureMatchedByProviderPack
		}
		return errorFallbackMatch{matchedBy: matchedBy, pack: pack, patternID: id}
	}
	return errorFallbackMatch{}
}

func detectTimeoutSignal(failure classifiedFailure) errorFallbackMatch {
	if failure.statusCode != nil && (*failure.statusCode == 408 || *failure.statusCode == 504) {
		return structuredMatch("http_status", "status_"+strconv.Itoa(*failure.statusCode))
	}
	if id, ok := firstMessagePhrase(failure.message, []safePhraseMatcher{{id: "timeout", phrases: []string{"timeout", "timed out", "deadline exceeded"}}}); ok {
		return errorFallbackMatch{matchedBy: failureMatchedByMessagePack, pack: "network", patternID: id}
	}
	return errorFallbackMatch{}
}

func detectNetworkSignal(err *schemas.BifrostError, failure classifiedFailure) errorFallbackMatch {
	if err != nil && err.Error != nil && (err.Error.Message == schemas.ErrProviderDoRequest || err.Error.Message == schemas.ErrProviderNetworkError) {
		return structuredMatch("network", "provider_network_error")
	}
	if id, ok := firstMessagePhrase(failure.message, []safePhraseMatcher{{id: "network", phrases: []string{"connection reset", "connection refused", "temporary failure in name resolution", "no such host"}}}); ok {
		return errorFallbackMatch{matchedBy: failureMatchedByMessagePack, pack: "network", patternID: id}
	}
	return errorFallbackMatch{}
}

func detectAuthenticationSignal(failure classifiedFailure) errorFallbackMatch {
	return detectStatusOrNormalized(failure, 401, []string{"authentication_error", "auth_error", "invalid_api_key", "unauthorized"}, schemas.FailureCategoryAuthentication)
}

func detectBillingSignal(failure classifiedFailure) errorFallbackMatch {
	return detectStatusOrNormalized(failure, 402, []string{"billing_error", "insufficient_quota", "quota_exceeded"}, schemas.FailureCategoryBilling)
}

func detectPermissionSignal(failure classifiedFailure) errorFallbackMatch {
	return detectStatusOrNormalized(failure, 403, []string{"permission_error", "forbidden"}, schemas.FailureCategoryPermission)
}

func detectRateLimitSignal(failure classifiedFailure) errorFallbackMatch {
	if failure.statusCode != nil && *failure.statusCode == 429 {
		return structuredMatch("http_status", "status_429")
	}
	for _, value := range append(append([]string(nil), failure.errorTypes...), failure.errorCodes...) {
		if IsRateLimitErrorMessage(value) {
			return structuredMatch("rate_limit", "structured_signal")
		}
	}
	if IsRateLimitErrorMessage(failure.message) {
		return errorFallbackMatch{matchedBy: failureMatchedByMessagePack, pack: "rate_limit", patternID: "message"}
	}
	return errorFallbackMatch{}
}

func detectUnsupportedOperationSignal(failure classifiedFailure) errorFallbackMatch {
	if id, ok := firstMessagePhrase(failure.message, []safePhraseMatcher{{id: "unsupported_operation", phrases: []string{"not supported", "unsupported", "does not support", "only supports", "is not available for this model"}}}); ok {
		return errorFallbackMatch{matchedBy: failureMatchedByMessagePack, pack: "unsupported_operation", patternID: id}
	}
	return errorFallbackMatch{}
}

func detectStatusOrNormalized(failure classifiedFailure, status int, values []string, category schemas.FailureCategory) errorFallbackMatch {
	if failure.statusCode != nil && *failure.statusCode == status {
		return structuredMatch("http_status", "status_"+strconv.Itoa(status))
	}
	if hit, ok := firstNormalizedHit(values, append(append([]string(nil), failure.errorTypes...), failure.errorCodes...)); ok {
		return structuredMatch(string(category), hit)
	}
	return errorFallbackMatch{}
}

func structuredMatch(pack, pattern string) errorFallbackMatch {
	return errorFallbackMatch{matchedBy: failureMatchedByStructured, pack: pack, patternID: pattern}
}

func conditionHasMatchers(c schemas.ErrorFallbackCondition) bool {
	return len(c.Categories) > 0 || len(c.ErrorCodes) > 0 || len(c.ErrorTypes) > 0 || len(c.StatusCodes) > 0 || len(c.MessageContains) > 0
}

func supplementHasMatchers(s schemas.ErrorFallbackSupplement) bool {
	return len(s.ErrorCodes) > 0 || len(s.ErrorTypes) > 0 || len(s.StatusCodes) > 0 || len(s.MessageContainsAny) > 0
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
	var result []string
	for _, value := range values {
		if value != nil {
			result = appendUniqueStrings(result, normalizedStrings(*value)...)
		}
	}
	return result
}

func normalizedStrings(values ...string) []string {
	var result []string
	for _, value := range values {
		if normalized := strings.ToLower(strings.TrimSpace(value)); normalized != "" {
			result = appendUniqueStrings(result, normalized)
		}
	}
	return result
}

func appendUniqueStrings(dst []string, values ...string) []string {
	for _, value := range values {
		if value != "" && !containsString(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}

func containsAnyNormalized(needles, haystack []string) bool {
	_, ok := firstNormalizedHit(needles, haystack)
	return ok
}

func firstNormalizedHit(needles, haystack []string) (string, bool) {
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && containsString(haystack, needle) {
			return needle, true
		}
	}
	return "", false
}

func firstMatchingSubstring(message string, fragments []string) (string, bool) {
	for _, fragment := range fragments {
		if fragment = strings.ToLower(strings.TrimSpace(fragment)); fragment != "" && strings.Contains(message, fragment) {
			return fragment, true
		}
	}
	return "", false
}

func firstMessagePhrase(message string, matchers []safePhraseMatcher) (string, bool) {
	for _, matcher := range matchers {
		for _, phrase := range matcher.phrases {
			if normalized := strings.ToLower(strings.TrimSpace(phrase)); normalized != "" && strings.Contains(message, normalized) {
				return matcher.id, true
			}
		}
	}
	return "", false
}

func containsProvider(values []schemas.ModelProvider, target schemas.ModelProvider) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsFailureCategory(values []schemas.FailureCategory, target schemas.FailureCategory) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
