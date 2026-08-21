package otel

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// SelectiveExportConfig controls record-level export to every configured target.
// When disabled, behaviour is fully backward compatible and every trace is exported.
type SelectiveExportConfig struct {
	Enabled               bool            `json:"enabled"`
	DryRun                bool            `json:"dry_run,omitempty"`
	RequireCompleteRecord *bool           `json:"require_complete_record,omitempty"`
	CandidateRate         *float64        `json:"candidate_rate,omitempty"`
	MaxExportsPerMinute   int             `json:"max_exports_per_minute,omitempty"`
	Rules                 []SelectionRule `json:"rules,omitempty"`
}

// SelectionRule is an ordered, first-match rule. A matched record is exported in
// full when its stable trace/rule hash falls within ExportRate; otherwise the
// complete record is dropped from Langfuse/OTLP.
type SelectionRule struct {
	ID                  string                `json:"id"`
	Priority            int                   `json:"priority"`
	RequestTypes        []schemas.RequestType `json:"request_types,omitempty"`
	MinLatencyMS        *int64                `json:"min_latency_ms,omitempty"`
	MaxLatencyMS        *int64                `json:"max_latency_ms,omitempty"`
	RequireError        *bool                 `json:"require_error,omitempty"`
	RequireFallback     *bool                 `json:"require_fallback,omitempty"`
	RequireRetry        *bool                 `json:"require_retry,omitempty"`
	ErrorCategories     []string              `json:"error_categories,omitempty"`
	Providers           []string              `json:"providers,omitempty"`
	Models              []string              `json:"models,omitempty"`
	RoutingRules        []string              `json:"routing_rules,omitempty"`
	MinCost             *float64              `json:"min_cost,omitempty"`
	MinTechnicalQuality *float64              `json:"min_technical_quality,omitempty"`
	ExportRate          float64               `json:"export_rate"`
	MaxPerMinute        int                   `json:"max_per_minute,omitempty"`
}

const (
	errorCategoryTimeout    = "timeout"
	errorCategoryConnection = "connection"
	errorCategoryClient     = "client_error"
	errorCategoryServer     = "server_error"
	errorCategoryOther      = "other"
)

var supportedErrorCategories = []string{
	errorCategoryTimeout,
	errorCategoryConnection,
	errorCategoryClient,
	errorCategoryServer,
	errorCategoryOther,
}

type traceSelector struct {
	dryRun              bool
	candidateRate       float64
	maxExportsPerMinute int
	rules               []SelectionRule
}

type selectorSnapshot struct{ selector *traceSelector }

type selectionQuotaLedger struct {
	mu         sync.Mutex
	window     int64
	total      int
	ruleCounts map[string]int
}

var processSelectionQuota = selectionQuotaLedger{ruleCounts: make(map[string]int)}

func newTraceSelector(config *SelectiveExportConfig) (*traceSelector, error) {
	if config == nil || !config.Enabled {
		return nil, nil
	}
	if len(config.Rules) == 0 {
		return nil, fmt.Errorf("selective_export requires at least one rule when enabled")
	}
	if len(config.Rules) > 32 {
		return nil, fmt.Errorf("selective_export supports at most 32 rules")
	}
	rules := slices.Clone(config.Rules)
	seen := make(map[string]struct{}, len(rules))
	for i := range rules {
		rules[i].ID = strings.TrimSpace(rules[i].ID)
		if rules[i].ID == "" {
			return nil, fmt.Errorf("selective_export rule %d requires an id", i)
		}
		if _, exists := seen[rules[i].ID]; exists {
			return nil, fmt.Errorf("selective_export rule id %q is duplicated", rules[i].ID)
		}
		seen[rules[i].ID] = struct{}{}
		if rules[i].Priority < -1000 || rules[i].Priority > 1000 {
			return nil, fmt.Errorf("selective_export rule %q priority must be between -1000 and 1000", rules[i].ID)
		}
		for _, requestType := range rules[i].RequestTypes {
			if !isSelectiveImageRequest(requestType) {
				return nil, fmt.Errorf("selective_export rule %q has unsupported request type %q", rules[i].ID, requestType)
			}
		}
		for _, category := range rules[i].ErrorCategories {
			if !slices.Contains(supportedErrorCategories, category) {
				return nil, fmt.Errorf("selective_export rule %q has unsupported error category %q", rules[i].ID, category)
			}
		}
		if len(rules[i].ErrorCategories) > 0 && rules[i].RequireError != nil && !*rules[i].RequireError {
			return nil, fmt.Errorf("selective_export rule %q cannot combine error_categories with require_error=false", rules[i].ID)
		}
		for _, dimension := range []struct {
			name   string
			values []string
		}{
			{name: "providers", values: rules[i].Providers},
			{name: "models", values: rules[i].Models},
			{name: "routing_rules", values: rules[i].RoutingRules},
		} {
			for _, value := range dimension.values {
				if strings.TrimSpace(value) == "" {
					return nil, fmt.Errorf("selective_export rule %q %s cannot contain an empty value", rules[i].ID, dimension.name)
				}
			}
		}
		if rules[i].ExportRate < 0 || rules[i].ExportRate > 1 {
			return nil, fmt.Errorf("selective_export rule %q export_rate must be between 0 and 1", rules[i].ID)
		}
		if rules[i].MinLatencyMS != nil && *rules[i].MinLatencyMS < 0 || rules[i].MaxLatencyMS != nil && *rules[i].MaxLatencyMS < 0 {
			return nil, fmt.Errorf("selective_export rule %q latency bounds must be non-negative", rules[i].ID)
		}
		if rules[i].MinLatencyMS != nil && rules[i].MaxLatencyMS != nil && *rules[i].MinLatencyMS > *rules[i].MaxLatencyMS {
			return nil, fmt.Errorf("selective_export rule %q min_latency_ms exceeds max_latency_ms", rules[i].ID)
		}
		if rules[i].MinTechnicalQuality != nil && (*rules[i].MinTechnicalQuality < 0 || *rules[i].MinTechnicalQuality > 1) {
			return nil, fmt.Errorf("selective_export rule %q min_technical_quality must be between 0 and 1", rules[i].ID)
		}
		if rules[i].MinCost != nil && *rules[i].MinCost < 0 {
			return nil, fmt.Errorf("selective_export rule %q min_cost must be non-negative", rules[i].ID)
		}
		if rules[i].MaxPerMinute < 0 {
			return nil, fmt.Errorf("selective_export rule %q max_per_minute must be non-negative", rules[i].ID)
		}
	}
	if config.MaxExportsPerMinute < 0 {
		return nil, fmt.Errorf("selective_export max_exports_per_minute must be non-negative")
	}
	candidateRate := 1.0
	if config.CandidateRate != nil {
		candidateRate = *config.CandidateRate
	}
	if candidateRate < 0 || candidateRate > 1 {
		return nil, fmt.Errorf("selective_export candidate_rate must be between 0 and 1")
	}
	if config.RequireCompleteRecord != nil && !*config.RequireCompleteRecord {
		return nil, fmt.Errorf("selective_export require_complete_record cannot be false; selected image records are atomic")
	}
	slices.SortStableFunc(rules, func(a, b SelectionRule) int { return b.Priority - a.Priority })
	return &traceSelector{
		dryRun: config.DryRun, candidateRate: candidateRate, maxExportsPerMinute: config.MaxExportsPerMinute,
		rules: rules,
	}, nil
}

func traceMediaSummariesComplete(trace *schemas.Trace) bool {
	if trace == nil {
		return false
	}
	span := finalImageSpan(trace)
	if span == nil {
		return false
	}
	input, _ := span.Attributes[schemas.AttrBifrostImageInput].(string)
	if input == "" {
		return false
	}
	output, _ := span.Attributes[schemas.AttrBifrostImageOutput].(string)
	if span.Status != schemas.SpanStatusError && (output == "" || strings.Contains(output, `"image_count":0`)) {
		return false
	}
	for _, key := range []string{schemas.AttrBifrostImageInput, schemas.AttrBifrostImageOutput} {
		raw, _ := span.Attributes[key].(string)
		if raw == "" {
			continue
		}
		for _, incomplete := range []string{"metadata_only", "too_large", "trace_limit", "attachment_limit", "trace_byte_limit", "global_byte_limit", "decode_saturated", "unsupported_mime", "invalid_base64", "invalid_url"} {
			if strings.Contains(raw, `"capture_status":"`+incomplete+`"`) {
				return false
			}
		}
	}
	return true
}

func selectedTraceMediaAttachments(trace *schemas.Trace) []schemas.TraceMedia {
	span := finalImageSpan(trace)
	if span == nil {
		return nil
	}
	referenced := make(map[string]struct{})
	for _, key := range []string{schemas.AttrBifrostImageInput, schemas.AttrBifrostImageOutput} {
		raw, _ := span.Attributes[key].(string)
		for _, attachment := range trace.MediaAttachments() {
			if strings.Contains(raw, "bifrost-media://"+attachment.ID) {
				referenced[attachment.ID] = struct{}{}
			}
		}
	}
	attachments := make([]schemas.TraceMedia, 0, len(referenced))
	for _, attachment := range trace.MediaAttachments() {
		if _, ok := referenced[attachment.ID]; ok {
			attachments = append(attachments, attachment)
		}
	}
	return attachments
}

func traceMediaCaptureFailureReasons(trace *schemas.Trace) []string {
	span := finalImageSpan(trace)
	if span == nil {
		return nil
	}
	known := []string{"metadata_only", "too_large", "attachment_limit", "trace_byte_limit", "global_byte_limit", "decode_saturated", "unsupported_mime", "invalid_base64", "invalid_url"}
	seen := make(map[string]struct{})
	for _, key := range []string{schemas.AttrBifrostImageInput, schemas.AttrBifrostImageOutput} {
		raw, _ := span.Attributes[key].(string)
		for _, reason := range known {
			if strings.Contains(raw, `"capture_status":"`+reason+`"`) {
				seen[reason] = struct{}{}
			}
		}
	}
	reasons := make([]string, 0, len(seen))
	for _, reason := range known {
		if _, ok := seen[reason]; ok {
			reasons = append(reasons, reason)
		}
	}
	return reasons
}

func (s *traceSelector) shouldExport(trace *schemas.Trace) bool {
	return s.decide(trace).selected
}

type selectionDecision struct {
	selected         bool
	ruleID           string
	reason           string
	technicalQuality float64
}

func (s *traceSelector) decide(trace *schemas.Trace) selectionDecision {
	if s == nil {
		return selectionDecision{selected: true, reason: "selection_disabled"}
	}
	facts := selectionFactsFromTrace(trace)
	if !isSelectiveImageRequest(facts.requestType) {
		return selectionDecision{selected: true, reason: "non_image"}
	}
	if eligible, exists := trace.GetAttribute(schemas.TraceAttrMediaCaptureEligible); exists {
		if allowed, ok := eligible.(bool); ok && !allowed {
			return selectionDecision{reason: "not_candidate", technicalQuality: facts.technicalQuality}
		}
	}
	for i := range s.rules {
		rule := &s.rules[i]
		if rule.matches(facts) {
			selectionID := trace.InternalID
			if selectionID == "" {
				selectionID = trace.RequestID
			}
			if selectionID == "" {
				selectionID = trace.TraceID
			}
			selected := stableSelection(selectionID, rule.ID, rule.ExportRate)
			reason := "sampled_out"
			if selected && !s.dryRun {
				selected = s.takeQuota(rule)
				if !selected {
					reason = "quota"
				}
			}
			if selected {
				reason = "selected"
			}
			return selectionDecision{selected: selected, ruleID: rule.ID, reason: reason, technicalQuality: facts.technicalQuality}
		}
	}
	return selectionDecision{reason: "no_matching_rule", technicalQuality: facts.technicalQuality}
}

func (s *traceSelector) shouldCaptureCandidate(traceID string, request *schemas.BifrostRequest) bool {
	if s == nil || s.dryRun || request == nil || !isSelectiveImageRequest(request.RequestType) {
		return true
	}
	return stableSelection(traceID, "media-candidate", s.candidateRate)
}

func isSelectiveImageRequest(requestType schemas.RequestType) bool {
	switch requestType {
	case schemas.ImageGenerationRequest, schemas.ImageGenerationStreamRequest,
		schemas.ImageEditRequest, schemas.ImageEditStreamRequest, schemas.ImageVariationRequest:
		return true
	default:
		return false
	}
}

func (s *traceSelector) takeQuota(rule *SelectionRule) bool {
	minute := time.Now().Unix() / 60
	processSelectionQuota.mu.Lock()
	defer processSelectionQuota.mu.Unlock()
	if processSelectionQuota.window != minute {
		processSelectionQuota.window = minute
		processSelectionQuota.total = 0
		clear(processSelectionQuota.ruleCounts)
	}
	if s.maxExportsPerMinute > 0 && processSelectionQuota.total >= s.maxExportsPerMinute {
		return false
	}
	if rule.MaxPerMinute > 0 && processSelectionQuota.ruleCounts[rule.ID] >= rule.MaxPerMinute {
		return false
	}
	if s.maxExportsPerMinute > 0 {
		processSelectionQuota.total++
	}
	if rule.MaxPerMinute > 0 {
		processSelectionQuota.ruleCounts[rule.ID]++
	}
	return true
}

func resetSelectionQuotaLedgerForTest() {
	processSelectionQuota.mu.Lock()
	processSelectionQuota.window = 0
	processSelectionQuota.total = 0
	clear(processSelectionQuota.ruleCounts)
	processSelectionQuota.mu.Unlock()
}

type selectionFacts struct {
	requestType      schemas.RequestType
	latencyMS        int64
	hasError         bool
	isFallback       bool
	hasRetry         bool
	errorCategory    string
	provider         string
	model            string
	routingRuleID    string
	routingRuleName  string
	cost             float64
	technicalQuality float64
}

func selectionFactsFromTrace(trace *schemas.Trace) selectionFacts {
	var facts selectionFacts
	if trace == nil {
		return facts
	}
	if !trace.StartTime.IsZero() && !trace.EndTime.IsZero() {
		facts.latencyMS = trace.EndTime.Sub(trace.StartTime).Milliseconds()
	}
	span := finalImageSpan(trace)
	if span != nil {
		requestType, _ := span.Attributes[schemas.AttrLegacyRequestType].(string)
		facts.requestType = schemas.RequestType(requestType)
		if facts.latencyMS == 0 && !span.StartTime.IsZero() && !span.EndTime.IsZero() {
			facts.latencyMS = span.EndTime.Sub(span.StartTime).Milliseconds()
		}
		facts.hasError = span.Status == schemas.SpanStatusError
		facts.isFallback = getIntAttr(span.Attributes, schemas.AttrBifrostFallbackIndex) > 0
		facts.hasRetry = getIntAttr(span.Attributes, schemas.AttrBifrostRetries) > 0
		facts.errorCategory = selectionErrorCategory(span)
		facts.provider = firstNonEmpty(
			getStringAttr(span.Attributes, schemas.AttrBifrostProviderName),
			getStringAttr(span.Attributes, schemas.AttrProviderName),
		)
		facts.model = firstNonEmpty(
			getStringAttr(span.Attributes, schemas.AttrBifrostProviderModel),
			getStringAttr(span.Attributes, schemas.AttrResponseModel),
			getStringAttr(span.Attributes, schemas.AttrRequestModel),
		)
		facts.routingRuleID = getStringAttr(span.Attributes, schemas.AttrBifrostRoutingRuleID)
		facts.routingRuleName = getStringAttr(span.Attributes, schemas.AttrBifrostRoutingRuleName)
		facts.cost = getFloat64Attr(span.Attributes, schemas.AttrUsageCost)
		facts.technicalQuality = technicalQualityScore(span)
	}
	return facts
}

func finalImageSpan(trace *schemas.Trace) *schemas.Span {
	if trace == nil {
		return nil
	}
	for i := len(trace.Spans) - 1; i >= 0; i-- {
		span := trace.Spans[i]
		if span == nil || span.Kind != schemas.SpanKindLLMCall {
			continue
		}
		requestType, _ := span.Attributes[schemas.AttrLegacyRequestType].(string)
		if isSelectiveImageRequest(schemas.RequestType(requestType)) {
			return span
		}
	}
	return nil
}

func (r *SelectionRule) matches(facts selectionFacts) bool {
	if len(r.RequestTypes) > 0 {
		matched := false
		for _, requestType := range r.RequestTypes {
			if normalizeImageRequestType(requestType) == normalizeImageRequestType(facts.requestType) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if r.MinLatencyMS != nil && facts.latencyMS < *r.MinLatencyMS {
		return false
	}
	if r.MaxLatencyMS != nil && facts.latencyMS >= *r.MaxLatencyMS {
		return false
	}
	if r.RequireError != nil && facts.hasError != *r.RequireError {
		return false
	}
	if r.RequireFallback != nil && facts.isFallback != *r.RequireFallback {
		return false
	}
	if r.RequireRetry != nil && facts.hasRetry != *r.RequireRetry {
		return false
	}
	if len(r.ErrorCategories) > 0 && !containsFold(r.ErrorCategories, facts.errorCategory) {
		return false
	}
	if len(r.Providers) > 0 && !containsFold(r.Providers, facts.provider) {
		return false
	}
	if len(r.Models) > 0 && !containsFold(r.Models, facts.model) {
		return false
	}
	if len(r.RoutingRules) > 0 && !containsFold(r.RoutingRules, facts.routingRuleID) && !containsFold(r.RoutingRules, facts.routingRuleName) {
		return false
	}
	if r.MinCost != nil && facts.cost < *r.MinCost {
		return false
	}
	if r.MinTechnicalQuality != nil && facts.technicalQuality < *r.MinTechnicalQuality {
		return false
	}
	return true
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func selectionErrorCategory(span *schemas.Span) string {
	if span == nil || span.Status != schemas.SpanStatusError {
		return ""
	}
	timeoutSource := getStringAttr(span.Attributes, schemas.AttrBifrostTimeoutSource)
	if timeoutSource == string(schemas.TimeoutSourceUpstreamDisconnect) {
		return errorCategoryConnection
	}
	if timeoutSource != "" {
		return errorCategoryTimeout
	}
	status := getIntAttr(span.Attributes, schemas.AttrHTTPResponseStatusCode)
	if status >= 400 && status < 500 {
		return errorCategoryClient
	}
	if status >= 500 {
		return errorCategoryServer
	}
	errorType := strings.ToLower(firstNonEmpty(
		getStringAttr(span.Attributes, schemas.AttrErrorTypeSpec),
		getStringAttr(span.Attributes, schemas.AttrErrorType),
	))
	if strings.Contains(errorType, "timeout") || strings.Contains(errorType, "deadline") {
		return errorCategoryTimeout
	}
	if strings.Contains(errorType, "connection") || strings.Contains(errorType, "disconnect") || strings.Contains(errorType, "eof") {
		return errorCategoryConnection
	}
	return errorCategoryOther
}

func normalizeImageRequestType(requestType schemas.RequestType) schemas.RequestType {
	switch requestType {
	case schemas.ImageGenerationStreamRequest:
		return schemas.ImageGenerationRequest
	case schemas.ImageEditStreamRequest:
		return schemas.ImageEditRequest
	default:
		return requestType
	}
}

func technicalQualityScore(span *schemas.Span) float64 {
	if span == nil || span.Status == schemas.SpanStatusError {
		return 0
	}
	score := 0.4
	inputN := 1
	if raw, ok := span.Attributes[schemas.AttrBifrostImageInput].(string); ok && raw != "" {
		var input struct {
			N int `json:"n"`
		}
		if schemas.Unmarshal([]byte(raw), &input) == nil && input.N > 0 {
			inputN = input.N
		}
	}
	var output struct {
		ImageCount int              `json:"image_count"`
		Images     []map[string]any `json:"images"`
	}
	rawOutput, _ := span.Attributes[schemas.AttrBifrostImageOutput].(string)
	if rawOutput == "" || schemas.Unmarshal([]byte(rawOutput), &output) != nil || len(output.Images) == 0 {
		return score
	}
	score += 0.1
	if output.ImageCount == inputN || len(output.Images) == inputN {
		score += 0.1
	}
	allUsable := true
	hasRevisedPrompt := false
	for _, image := range output.Images {
		status, _ := image["capture_status"].(string)
		urlValue, _ := image["url"].(string)
		mediaRef, _ := image["media_ref"].(string)
		if urlValue == "" && mediaRef == "" && status != "captured" {
			allUsable = false
		}
		if revisedPrompt, _ := image["revised_prompt"].(string); revisedPrompt != "" {
			hasRevisedPrompt = true
		}
	}
	if allUsable {
		score += 0.3
	}
	if hasRevisedPrompt {
		score += 0.05
	}
	if getIntAttr(span.Attributes, schemas.AttrBifrostFallbackIndex) == 0 {
		score += 0.05
	}
	return score
}

func stableSelection(traceID, ruleID string, rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	digest := sha256.Sum256([]byte(traceID + "\x00" + ruleID))
	bucket := float64(binary.BigEndian.Uint64(digest[:8])) / float64(^uint64(0))
	return bucket < rate
}

const (
	attrSelectionRule             = "bifrost.selection.rule"
	attrSelectionTechnicalQuality = "bifrost.selection.technical_quality"
	attrSelectionDryRunSelected   = "bifrost.selection.dry_run_selected"
)

func annotateSelectionDecision(trace *schemas.Trace, decision selectionDecision, dryRun bool) {
	if trace == nil {
		return
	}
	for _, span := range trace.Spans {
		if span == nil || span.Kind != schemas.SpanKindLLMCall {
			continue
		}
		requestType, _ := span.Attributes[schemas.AttrLegacyRequestType].(string)
		switch schemas.RequestType(requestType) {
		case schemas.ImageGenerationRequest, schemas.ImageGenerationStreamRequest,
			schemas.ImageEditRequest, schemas.ImageEditStreamRequest, schemas.ImageVariationRequest:
			span.SetAttribute(attrSelectionRule, decision.ruleID)
			span.SetAttribute(attrSelectionTechnicalQuality, decision.technicalQuality)
			if dryRun {
				span.SetAttribute(attrSelectionDryRunSelected, decision.selected)
			}
		}
	}
}
