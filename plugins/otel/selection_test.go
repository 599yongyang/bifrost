package otel

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type selectionCountingClient struct{ calls atomic.Int32 }

func (c *selectionCountingClient) Emit(context.Context, []*ResourceSpan) error {
	c.calls.Add(1)
	return nil
}
func (*selectionCountingClient) Close() error { return nil }

func selectionTrace(id string, requestType schemas.RequestType, latency time.Duration) *schemas.Trace {
	start := time.Now()
	span := &schemas.Span{
		SpanID: "1111111111111111", Name: "generate_content model", Kind: schemas.SpanKindLLMCall,
		StartTime: start, EndTime: start.Add(latency), Status: schemas.SpanStatusOk,
		Attributes: map[string]any{
			schemas.AttrLegacyRequestType:      string(requestType),
			schemas.AttrBifrostProviderName:    "openai",
			schemas.AttrResponseModel:          "gpt-4o",
			schemas.AttrBifrostRoutingRuleName: "premium",
			schemas.AttrUsageCost:              0.25,
		},
	}
	return &schemas.Trace{TraceID: id, InternalID: id, RequestID: id, RootSpan: span, Spans: []*schemas.Span{span}, StartTime: start, EndTime: start.Add(latency)}
}

func TestSelectionMatchesV2CanonicalDimensions(t *testing.T) {
	required := true
	minCost := 0.2
	minLatency := int64(100)
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{
		ID: "rich", Priority: 10, RequestTypes: []schemas.RequestType{schemas.ChatCompletionRequest},
		MinLatencyMS: &minLatency, RequireFallback: &required, RequireRetry: &required,
		Providers: []string{"openai"}, Models: []string{"gpt-4o"}, RoutingRules: []string{"premium"}, MinCost: &minCost, ExportRate: 1,
	}}})
	require.NoError(t, err)
	trace := selectionTrace("rich", schemas.ChatCompletionRequest, time.Second)
	trace.Spans[0].Attributes[schemas.AttrBifrostFallbackIndex] = 1
	trace.Spans[0].Attributes[schemas.AttrBifrostRetries] = 2

	decision := selector.decide(trace)
	assert.True(t, decision.selected)
	assert.Equal(t, "rich", decision.ruleID)

	trace.Spans[0].Attributes[schemas.AttrUsageCost] = 0.1
	assert.False(t, selector.decide(trace).selected)
}

func TestSelectionUsesLatestAttemptSpan(t *testing.T) {
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{
		{ID: "anthropic-final", Providers: []string{"anthropic"}, ExportRate: 1},
		{ID: "drop", ExportRate: 0},
	}})
	require.NoError(t, err)
	trace := selectionTrace("fallback", schemas.ChatCompletionRequest, time.Second)
	latest := &schemas.Span{SpanID: "2222222222222222", Kind: schemas.SpanKindRetry, StartTime: trace.StartTime.Add(time.Second), EndTime: trace.EndTime.Add(time.Second), Attributes: map[string]any{
		schemas.AttrLegacyRequestType: string(schemas.ChatCompletionRequest), schemas.AttrBifrostProviderName: "anthropic", schemas.AttrRequestModel: "claude",
	}}
	trace.Spans = append(trace.Spans, latest)
	decision := selector.decide(trace)
	assert.True(t, decision.selected)
	assert.Equal(t, "anthropic-final", decision.ruleID)
}

func TestSelectionClassifiesErrors(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]any
		want  string
	}{
		{name: "timeout", attrs: map[string]any{schemas.AttrErrorTypeSpec: "request_timeout"}, want: errorCategoryTimeout},
		{name: "canonical 504 timeout", attrs: map[string]any{schemas.AttrErrorTypeSpec: "request_timed_out", schemas.AttrHTTPResponseStatusCode: 504}, want: errorCategoryTimeout},
		{name: "disconnect", attrs: map[string]any{schemas.AttrErrorTypeSpec: "connection_reset"}, want: errorCategoryConnection},
		{name: "connection before generic 503", attrs: map[string]any{schemas.AttrErrorTypeSpec: "upstream_connection_error", schemas.AttrHTTPResponseStatusCode: 503}, want: errorCategoryConnection},
		{name: "client", attrs: map[string]any{schemas.AttrHTTPResponseStatusCode: 429}, want: errorCategoryClient},
		{name: "server", attrs: map[string]any{schemas.AttrHTTPResponseStatusCode: 503}, want: errorCategoryServer},
		{name: "other", attrs: map[string]any{schemas.AttrErrorTypeSpec: "provider_error"}, want: errorCategoryOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := selectionTrace(tt.name, schemas.ImageGenerationRequest, time.Second)
			trace.Spans[0].Status = schemas.SpanStatusError
			for key, value := range tt.attrs {
				trace.Spans[0].Attributes[key] = value
			}
			assert.Equal(t, tt.want, selectionFactsFromTrace(trace).errorCategory)
		})
	}
}

func TestSelectionValidationStableSamplingAndQuota(t *testing.T) {
	negative := -0.1
	falseValue := false
	for _, config := range []*SelectiveExportConfig{
		{Enabled: true},
		{Enabled: true, Rules: []SelectionRule{{ID: "duplicate", ExportRate: 1}, {ID: "duplicate", ExportRate: 1}}},
		{Enabled: true, Rules: []SelectionRule{{ID: "bad-rate", ExportRate: 2}}},
		{Enabled: true, Rules: []SelectionRule{{ID: "bad-cost", MinCost: &negative, ExportRate: 1}}},
		{Enabled: true, Rules: []SelectionRule{{ID: "bad-error", RequireError: &falseValue, ErrorCategories: []string{errorCategoryTimeout}, ExportRate: 1}}},
	} {
		_, err := newTraceSelector(config)
		require.Error(t, err)
	}

	assert.Equal(t, stableSelection("trace", "rule", 0.5), stableSelection("trace", "rule", 0.5))
	resetSelectionQuotaLedgerForTest()
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, MaxExportsPerMinute: 2, Rules: []SelectionRule{{ID: "all", ExportRate: 1}}})
	require.NoError(t, err)
	for i, want := range []bool{true, true, false} {
		assert.Equal(t, want, selector.decide(selectionTrace(fmt.Sprintf("quota-%d", i), schemas.ResponsesRequest, time.Second)).selected)
	}
}

func TestInjectSelectiveExportAndDryRun(t *testing.T) {
	client := &selectionCountingClient{}
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{
		{ID: "slow", Priority: 10, MinLatencyMS: int64Ptr(500), ExportRate: 1},
		{ID: "drop", ExportRate: 0},
	}})
	require.NoError(t, err)
	plugin := &OtelPlugin{pluginSpanFilter: &PluginSpanFilter{}, selector: selector, targets: []*otelTarget{{serviceName: "test", client: client, exportTimeout: time.Second}}}
	require.NoError(t, plugin.Inject(context.Background(), selectionTrace("slow", schemas.ChatCompletionRequest, time.Second)))
	require.NoError(t, plugin.Inject(context.Background(), selectionTrace("fast", schemas.ChatCompletionRequest, time.Millisecond)))
	assert.Equal(t, int32(1), client.calls.Load())

	selector.dryRun = true
	require.NoError(t, plugin.Inject(context.Background(), selectionTrace("dry", schemas.ChatCompletionRequest, time.Millisecond)))
	assert.Equal(t, int32(2), client.calls.Load())
}

func TestSelectionAnnotationUsesBifrostNamespace(t *testing.T) {
	trace := selectionTrace("annotation", schemas.EmbeddingRequest, time.Second)
	annotateSelectionDecision(trace, selectionDecision{selected: false, ruleID: "sample", reason: "sampled_out"}, true)
	attrs := trace.Spans[0].Attributes
	assert.Equal(t, "sample", attrs[attrSelectionRule])
	assert.Equal(t, "sampled_out", attrs[attrSelectionReason])
	assert.Equal(t, false, attrs[attrSelectionDryRunSelected])
	for key := range attrs {
		assert.NotContains(t, key, "gen_ai.bifrost")
	}
}

func TestSelectiveExportConfigStorageAndRedactionRoundTrip(t *testing.T) {
	required := true
	candidateRate := 0.25
	quality := 0.85
	config := &Config{
		Profiles: []*Profile{{Enabled: true, TracesEnabled: true, ServiceName: "test", CollectorURL: schemas.NewSecretVar("http://collector.test"), Headers: map[string]string{"Authorization": "secret"}}},
		SelectiveExport: &SelectiveExportConfig{Enabled: true, DryRun: true, RequireCompleteRecord: &required, CandidateRate: &candidateRate, MaxExportsPerMinute: 10, Rules: []SelectionRule{{
			ID: "fallbacks", RequestTypes: []schemas.RequestType{schemas.ResponsesRequest}, RequireFallback: &required, MinTechnicalQuality: &quality, ExportRate: 0.5,
		}}},
	}
	stored, err := config.MarshalForStorage()
	require.NoError(t, err)
	var decoded Config
	require.NoError(t, sonic.Unmarshal(stored, &decoded))
	require.NotNil(t, decoded.SelectiveExport)
	require.Len(t, decoded.SelectiveExport.Rules, 1)
	assert.Equal(t, schemas.ResponsesRequest, decoded.SelectiveExport.Rules[0].RequestTypes[0])
	assert.Equal(t, 0.5, decoded.SelectiveExport.Rules[0].ExportRate)
	assert.Equal(t, 0.25, *decoded.SelectiveExport.CandidateRate)
	assert.Equal(t, 0.85, *decoded.SelectiveExport.Rules[0].MinTechnicalQuality)

	redacted := config.Redacted()
	require.NotNil(t, redacted.SelectiveExport)
	assert.Equal(t, "fallbacks", redacted.SelectiveExport.Rules[0].ID)
	assert.NotEqual(t, "secret", redacted.Profiles[0].Headers["Authorization"])
}

func TestSelectiveExportCandidateRateControlsHeadMediaCapture(t *testing.T) {
	zero := 0.0
	selector, err := newTraceSelector(&SelectiveExportConfig{
		Enabled: true, CandidateRate: &zero, Rules: []SelectionRule{{ID: "all", ExportRate: 1}},
	})
	require.NoError(t, err)
	request := &schemas.BifrostRequest{RequestType: schemas.ImageGenerationRequest}
	assert.False(t, selector.shouldCaptureCandidate("trace", request))
	assert.True(t, selector.shouldCaptureCandidate("trace", &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest}))
}

func int64Ptr(value int64) *int64 { return &value }
