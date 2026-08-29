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

// SelectiveExportConfig controls trace-level export to every configured target.
// Selection happens once before fan-out, so a record is either sent to all profiles
// or dropped from all profiles. Metrics remain unaffected.
type SelectiveExportConfig struct {
	Enabled               bool            `json:"enabled"`
	DryRun                bool            `json:"dry_run,omitempty"`
	RequireCompleteRecord *bool           `json:"require_complete_record,omitempty"`
	CandidateRate         *float64        `json:"candidate_rate,omitempty"`
	MaxExportsPerMinute   int             `json:"max_exports_per_minute,omitempty"`
	Rules                 []SelectionRule `json:"rules,omitempty"`
}

// SelectionRule is an ordered first-match rule. Values inside a dimension are ORed;
// populated dimensions are ANDed. ExportRate uses a stable request/rule hash.
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

	attrSelectionRule           = "bifrost.selection.rule"
	attrSelectionReason         = "bifrost.selection.reason"
	attrSelectionDryRunSelected = "bifrost.selection.dry_run_selected"
)

var supportedErrorCategories = []string{
	errorCategoryTimeout, errorCategoryConnection, errorCategoryClient,
	errorCategoryServer, errorCategoryOther,
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

	rules := slices.Clone(config.Rules)
	seen := make(map[string]struct{}, len(rules))
	for i := range rules {
		rule := &rules[i]
		rule.ID = strings.TrimSpace(rule.ID)
		if rule.ID == "" {
			return nil, fmt.Errorf("selective_export rule %d requires an id", i)
		}
		if _, exists := seen[rule.ID]; exists {
			return nil, fmt.Errorf("selective_export rule id %q is duplicated", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if rule.Priority < -1000 || rule.Priority > 1000 {
			return nil, fmt.Errorf("selective_export rule %q priority must be between -1000 and 1000", rule.ID)
		}
		for _, requestType := range rule.RequestTypes {
			if strings.TrimSpace(string(requestType)) == "" {
				return nil, fmt.Errorf("selective_export rule %q request_types cannot contain an empty value", rule.ID)
			}
		}
		for _, category := range rule.ErrorCategories {
			if !slices.Contains(supportedErrorCategories, category) {
				return nil, fmt.Errorf("selective_export rule %q has unsupported error category %q", rule.ID, category)
			}
		}
		if len(rule.ErrorCategories) > 0 && rule.RequireError != nil && !*rule.RequireError {
			return nil, fmt.Errorf("selective_export rule %q cannot combine error_categories with require_error=false", rule.ID)
		}
		for _, dimension := range [][]string{rule.Providers, rule.Models, rule.RoutingRules} {
			for _, value := range dimension {
				if strings.TrimSpace(value) == "" {
					return nil, fmt.Errorf("selective_export rule %q dimensions cannot contain an empty value", rule.ID)
				}
			}
		}
		if rule.ExportRate < 0 || rule.ExportRate > 1 {
			return nil, fmt.Errorf("selective_export rule %q export_rate must be between 0 and 1", rule.ID)
		}
		if (rule.MinLatencyMS != nil && *rule.MinLatencyMS < 0) || (rule.MaxLatencyMS != nil && *rule.MaxLatencyMS < 0) {
			return nil, fmt.Errorf("selective_export rule %q latency bounds must be non-negative", rule.ID)
		}
		if rule.MinLatencyMS != nil && rule.MaxLatencyMS != nil && *rule.MinLatencyMS > *rule.MaxLatencyMS {
			return nil, fmt.Errorf("selective_export rule %q min_latency_ms exceeds max_latency_ms", rule.ID)
		}
		if rule.MinCost != nil && *rule.MinCost < 0 {
			return nil, fmt.Errorf("selective_export rule %q min_cost must be non-negative", rule.ID)
		}
		if rule.MinTechnicalQuality != nil && (*rule.MinTechnicalQuality < 0 || *rule.MinTechnicalQuality > 1) {
			return nil, fmt.Errorf("selective_export rule %q min_technical_quality must be between 0 and 1", rule.ID)
		}
		if rule.MaxPerMinute < 0 {
			return nil, fmt.Errorf("selective_export rule %q max_per_minute must be non-negative", rule.ID)
		}
	}
	slices.SortStableFunc(rules, func(a, b SelectionRule) int { return b.Priority - a.Priority })
	return &traceSelector{dryRun: config.DryRun, candidateRate: candidateRate, maxExportsPerMinute: config.MaxExportsPerMinute, rules: rules}, nil
}

type selectionDecision struct {
	selected bool
	ruleID   string
	reason   string
}

func (s *traceSelector) decide(trace *schemas.Trace) selectionDecision {
	if s == nil {
		return selectionDecision{selected: true, reason: "selection_disabled"}
	}
	facts := selectionFactsFromTrace(trace)
	if eligible, exists := trace.GetAttribute(schemas.TraceAttrMediaCaptureEligible); exists {
		if allowed, ok := eligible.(bool); ok && !allowed && isImageRequestType(facts.requestType) {
			return selectionDecision{reason: "not_candidate"}
		}
	}
	for i := range s.rules {
		rule := &s.rules[i]
		if !rule.matches(facts) {
			continue
		}
		selectionID := firstNonEmpty(trace.InternalID, trace.RequestID, trace.TraceID)
		selected := stableSelection(selectionID, rule.ID, rule.ExportRate)
		reason := "sampled_out"
		if selected && isImageRequestType(facts.requestType) && !traceMediaSummariesComplete(trace) {
			selected, reason = false, "incomplete_media"
			return selectionDecision{selected: selected, ruleID: rule.ID, reason: reason}
		}
		if selected && !s.dryRun && !s.takeQuota(rule) {
			selected, reason = false, "quota"
		} else if selected {
			reason = "selected"
		}
		return selectionDecision{selected: selected, ruleID: rule.ID, reason: reason}
	}
	return selectionDecision{reason: "no_matching_rule"}
}

func isImageRequestType(requestType schemas.RequestType) bool {
	switch requestType {
	case schemas.ImageGenerationRequest, schemas.ImageGenerationStreamRequest,
		schemas.ImageEditRequest, schemas.ImageEditStreamRequest, schemas.ImageVariationRequest:
		return true
	default:
		return false
	}
}

func traceMediaSummariesComplete(trace *schemas.Trace) bool {
	span := finalSelectionSpan(trace)
	if span == nil {
		return false
	}
	input := getStringAttr(span.Attributes, schemas.AttrBifrostImageInput)
	if input == "" {
		return false
	}
	output := getStringAttr(span.Attributes, schemas.AttrBifrostImageOutput)
	if span.Status != schemas.SpanStatusError && (output == "" || strings.Contains(output, `"image_count":0`)) {
		return false
	}
	for _, raw := range []string{input, output} {
		for _, incomplete := range []string{"metadata_only", "too_large", "attachment_limit", "trace_byte_limit", "global_byte_limit", "decode_saturated", "unsupported_mime", "invalid_base64", "invalid_url"} {
			if strings.Contains(raw, `"capture_status":"`+incomplete+`"`) {
				return false
			}
		}
	}
	attachments := trace.MediaAttachments()
	for _, raw := range []string{input, output} {
		for _, marker := range localMediaReferencePattern.FindAllString(raw, -1) {
			found := false
			for _, media := range attachments {
				if marker == "bifrost-media://"+media.ID {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
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
	span := finalSelectionSpan(trace)
	if span == nil {
		return facts
	}
	facts.requestType = schemas.RequestType(getStringAttr(span.Attributes, schemas.AttrLegacyRequestType))
	if facts.latencyMS == 0 && !span.StartTime.IsZero() && !span.EndTime.IsZero() {
		facts.latencyMS = span.EndTime.Sub(span.StartTime).Milliseconds()
	}
	facts.hasError = span.Status == schemas.SpanStatusError
	facts.isFallback = getIntAttr(span.Attributes, schemas.AttrBifrostFallbackIndex) > 0
	facts.hasRetry = getIntAttr(span.Attributes, schemas.AttrBifrostRetries) > 0
	facts.errorCategory = selectionErrorCategory(span)
	facts.provider = firstNonEmpty(getStringAttr(span.Attributes, schemas.AttrBifrostProviderName), getStringAttr(span.Attributes, schemas.AttrProviderName))
	facts.model = firstNonEmpty(getStringAttr(span.Attributes, schemas.AttrResponseModel), getStringAttr(span.Attributes, schemas.AttrRequestModel))
	facts.routingRuleID = getStringAttr(span.Attributes, schemas.AttrBifrostRoutingRuleID)
	facts.routingRuleName = getStringAttr(span.Attributes, schemas.AttrBifrostRoutingRuleName)
	facts.cost = getFloat64Attr(span.Attributes, schemas.AttrUsageCost)
	facts.technicalQuality = technicalQualityScore(span)
	return facts
}

func finalSelectionSpan(trace *schemas.Trace) *schemas.Span {
	if trace == nil {
		return nil
	}
	var final *schemas.Span
	for _, span := range trace.Spans {
		if span == nil || (span.Kind != schemas.SpanKindLLMCall && span.Kind != schemas.SpanKindRetry) {
			continue
		}
		if final == nil || span.EndTime.After(final.EndTime) {
			final = span
		}
	}
	return final
}

func (r *SelectionRule) matches(facts selectionFacts) bool {
	if len(r.RequestTypes) > 0 && !containsRequestType(r.RequestTypes, facts.requestType) {
		return false
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
	return r.MinTechnicalQuality == nil || facts.technicalQuality >= *r.MinTechnicalQuality
}

func (s *traceSelector) shouldCaptureCandidate(traceID string, request *schemas.BifrostRequest) bool {
	if s == nil || s.dryRun || request == nil || !isImageRequestType(request.RequestType) {
		return true
	}
	return stableSelection(traceID, "media-candidate", s.candidateRate)
}

func technicalQualityScore(span *schemas.Span) float64 {
	if span == nil || span.Status == schemas.SpanStatusError {
		return 0
	}
	score := 0.4
	inputN := 1
	if raw := getStringAttr(span.Attributes, schemas.AttrBifrostImageInput); raw != "" {
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
	rawOutput := getStringAttr(span.Attributes, schemas.AttrBifrostImageOutput)
	if rawOutput == "" || schemas.Unmarshal([]byte(rawOutput), &output) != nil || len(output.Images) == 0 {
		return score
	}
	score += 0.1
	if output.ImageCount == inputN || len(output.Images) == inputN {
		score += 0.1
	}
	allUsable, revisedPrompt := true, false
	for _, image := range output.Images {
		status, _ := image["capture_status"].(string)
		urlValue, _ := image["url"].(string)
		mediaRef, _ := image["media_ref"].(string)
		if urlValue == "" && mediaRef == "" && status != "captured" {
			allUsable = false
		}
		if value, _ := image["revised_prompt"].(string); value != "" {
			revisedPrompt = true
		}
	}
	if allUsable {
		score += 0.3
	}
	if revisedPrompt {
		score += 0.05
	}
	if getIntAttr(span.Attributes, schemas.AttrBifrostFallbackIndex) == 0 {
		score += 0.05
	}
	return score
}

func containsRequestType(values []schemas.RequestType, target schemas.RequestType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
	errorType := strings.ToLower(getStringAttr(span.Attributes, schemas.AttrErrorTypeSpec))
	if strings.Contains(errorType, "timeout") || strings.Contains(errorType, "timed_out") || strings.Contains(errorType, "deadline") {
		return errorCategoryTimeout
	}
	if strings.Contains(errorType, "connection") || strings.Contains(errorType, "disconnect") || strings.Contains(errorType, "eof") {
		return errorCategoryConnection
	}
	status := getIntAttr(span.Attributes, schemas.AttrHTTPResponseStatusCode)
	if status >= 400 && status < 500 {
		return errorCategoryClient
	}
	if status >= 500 {
		return errorCategoryServer
	}
	return errorCategoryOther
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

func annotateSelectionDecision(trace *schemas.Trace, decision selectionDecision, dryRun bool) {
	span := finalSelectionSpan(trace)
	if span == nil {
		return
	}
	span.SetAttribute(attrSelectionRule, decision.ruleID)
	span.SetAttribute(attrSelectionReason, decision.reason)
	if dryRun {
		span.SetAttribute(attrSelectionDryRunSelected, decision.selected)
	}
}
