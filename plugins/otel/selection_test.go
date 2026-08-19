package otel

import (
	"context"
	"fmt"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

func imageSelectionTrace(traceID string, latency time.Duration) *schemas.Trace {
	start := time.Now()
	span := &schemas.Span{
		SpanID: "1111111111111111", Name: "generate_content image-model", Kind: schemas.SpanKindLLMCall,
		StartTime: start, EndTime: start.Add(latency), Status: schemas.SpanStatusOk,
		Attributes: map[string]any{
			schemas.AttrLegacyRequestType:  string(schemas.ImageGenerationRequest),
			schemas.AttrBifrostImageInput:  `{"prompt":"test","size":"1024x1024","n":1}`,
			schemas.AttrBifrostImageOutput: `{"images":[{"url":"https://images.example.test/result.png"}],"image_count":1}`,
		},
	}
	return &schemas.Trace{TraceID: traceID, InternalID: traceID, RootSpan: span, Spans: []*schemas.Span{span}, StartTime: start, EndTime: start.Add(latency)}
}

func TestSelectionUsesFinalImageSpanInMixedTrace(t *testing.T) {
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{ID: "drop-images", ExportRate: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	trace := imageSelectionTrace("mixed", time.Second)
	trace.Spans = append(trace.Spans, &schemas.Span{Kind: schemas.SpanKindLLMCall, Attributes: map[string]any{
		schemas.AttrLegacyRequestType: string(schemas.ChatCompletionRequest),
	}})
	if selector.shouldExport(trace) {
		t.Fatal("mixed trace bypassed image selection because a non-image span was last")
	}
}

func TestSelectionNormalizesStreamRequestFamilies(t *testing.T) {
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{
		{ID: "keep-generation", RequestTypes: []schemas.RequestType{schemas.ImageGenerationRequest}, ExportRate: 1},
		{ID: "drop", ExportRate: 0},
	}})
	if err != nil {
		t.Fatal(err)
	}
	trace := imageSelectionTrace("stream", time.Second)
	trace.Spans[0].Attributes[schemas.AttrLegacyRequestType] = string(schemas.ImageGenerationStreamRequest)
	if !selector.shouldExport(trace) {
		t.Fatal("image_generation rule did not match image_generation_stream")
	}
}

func TestSelectionMatchesErrorAndFallbackClasses(t *testing.T) {
	requireTrue := true
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{
		{ID: "fallback-errors", Priority: 10, RequireError: &requireTrue, RequireFallback: &requireTrue, ExportRate: 1},
		{ID: "drop", ExportRate: 0},
	}})
	if err != nil {
		t.Fatal(err)
	}
	trace := imageSelectionTrace("fallback-error", time.Second)
	trace.Spans[0].Status = schemas.SpanStatusError
	trace.Spans[0].Attributes[schemas.AttrBifrostFallbackIndex] = 1
	if !selector.shouldExport(trace) {
		t.Fatal("error+fallback classification rule did not match")
	}
	trace.Spans[0].Status = schemas.SpanStatusOk
	if selector.shouldExport(trace) {
		t.Fatal("error-required rule matched a successful request")
	}
}

func TestSelectiveExportRejectsIncompleteRecordOptOut(t *testing.T) {
	value := false
	if _, err := newTraceSelector(&SelectiveExportConfig{
		Enabled: true, RequireCompleteRecord: &value, Rules: []SelectionRule{{ID: "all", ExportRate: 1}},
	}); err == nil {
		t.Fatal("newTraceSelector accepted require_complete_record=false")
	}
}

func TestSelectionUsesPerRequestInternalID(t *testing.T) {
	traceA := imageSelectionTrace("shared-w3c-trace", time.Second)
	traceB := imageSelectionTrace("shared-w3c-trace", time.Second)
	traceA.InternalID = "request-a"
	traceB.InternalID = "request-b"
	foundDifferent := false
	for i := 1; i < 100; i++ {
		rate := float64(i) / 100
		if stableSelection(traceA.InternalID, "sample", rate) != stableSelection(traceB.InternalID, "sample", rate) {
			foundDifferent = true
			break
		}
	}
	if !foundDifferent {
		t.Fatal("distinct requests sharing W3C trace ID always received the same sample decision")
	}
}

func TestBeginTraceMediaCaptureSkipsWhenNoTargetCanUploadMedia(t *testing.T) {
	plugin := &OtelPlugin{targets: []*otelTarget{{disableContentLogging: true}, {client: &countingTestOtelClient{}}}}
	decision := plugin.BeginTraceMediaCapture("request", &schemas.BifrostRequest{RequestType: schemas.ImageEditRequest})
	if decision.Capture {
		t.Fatal("media capture enabled although every target discards content or lacks a media uploader")
	}
	if _, ok := decision.PolicySnapshot.(*selectorSnapshot); !ok {
		t.Fatalf("disabled policy snapshot = %T, want explicit sentinel", decision.PolicySnapshot)
	}
}

func TestInjectSelectiveExportKeepsSlowRecordsAndDropsFastRecords(t *testing.T) {
	client := &countingTestOtelClient{}
	selector, err := newTraceSelector(&SelectiveExportConfig{
		Enabled: true,
		Rules: []SelectionRule{
			{ID: "slow", Priority: 100, MinLatencyMS: int64Ptr(1000), ExportRate: 1},
			{ID: "default", Priority: 0, ExportRate: 0},
		},
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	plugin := &OtelPlugin{
		pluginSpanFilter: &PluginSpanFilter{},
		selector:         selector,
		targets: []*otelTarget{{
			serviceName: "test", client: client, exportTimeout: time.Second,
		}},
	}

	if err := plugin.Inject(context.Background(), imageSelectionTrace("00000000000000000000000000000001", 2*time.Second)); err != nil {
		t.Fatalf("slow Inject() error = %v", err)
	}
	if err := plugin.Inject(context.Background(), imageSelectionTrace("00000000000000000000000000000002", 100*time.Millisecond)); err != nil {
		t.Fatalf("fast Inject() error = %v", err)
	}
	if calls := client.calls.Load(); calls != 1 {
		t.Fatalf("OTLP calls = %d, want only the complete slow record exported", calls)
	}
}

func TestInjectSelectiveExportKeepsTechnicallyGoodCompleteRecords(t *testing.T) {
	client := &countingTestOtelClient{}
	selector, err := newTraceSelector(&SelectiveExportConfig{
		Enabled: true,
		Rules: []SelectionRule{
			{ID: "technically-good", Priority: 100, MinTechnicalQuality: float64Ptr(0.8), ExportRate: 1},
			{ID: "default", Priority: 0, ExportRate: 0},
		},
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	plugin := &OtelPlugin{
		pluginSpanFilter: &PluginSpanFilter{}, selector: selector,
		targets: []*otelTarget{{serviceName: "test", client: client, exportTimeout: time.Second}},
	}

	good := imageSelectionTrace("00000000000000000000000000000011", time.Second)
	bad := imageSelectionTrace("00000000000000000000000000000012", time.Second)
	bad.Spans[0].Attributes[schemas.AttrBifrostImageOutput] = `{"images":[{"capture_status":"invalid_base64"}],"image_count":1}`
	for _, trace := range []*schemas.Trace{good, bad} {
		if err := plugin.Inject(context.Background(), trace); err != nil {
			t.Fatalf("Inject() error = %v", err)
		}
	}
	if calls := client.calls.Load(); calls != 1 {
		t.Fatalf("OTLP calls = %d, want only the technically good complete record", calls)
	}
}

func TestInjectSelectiveExportDropsSelectedRecordWhenMediaUploadFails(t *testing.T) {
	logger = bifrost.NewDefaultLogger(schemas.LogLevelError)
	client := &countingTestOtelClient{}
	selector, err := newTraceSelector(&SelectiveExportConfig{
		Enabled: true,
		Rules:   []SelectionRule{{ID: "all", Priority: 0, ExportRate: 1}},
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	plugin := &OtelPlugin{
		pluginSpanFilter: &PluginSpanFilter{}, selector: selector,
		targets: []*otelTarget{{
			serviceName: "test", client: client, mediaUploader: &failingTestMediaUploader{},
			exportTimeout: time.Second, mediaSem: make(chan struct{}, 1),
		}},
	}
	trace := imageSelectionTrace("00000000000000000000000000000021", time.Second)
	trace.Spans[0].Attributes[schemas.AttrBifrostImageInput] = `{"images":[{"media_ref":"bifrost-media://image","capture_status":"captured"}]}`
	attachTestMedia(trace, schemas.TraceMedia{ID: "image", SpanID: trace.Spans[0].SpanID, Field: "input", MIMEType: "image/png", Data: []byte("image")})
	if err := plugin.Inject(context.Background(), trace); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if calls := client.calls.Load(); calls != 0 {
		t.Fatalf("OTLP calls = %d, want selected incomplete record dropped atomically", calls)
	}
}

func TestInjectSelectiveExportEnforcesPerRuleQuota(t *testing.T) {
	client := &countingTestOtelClient{}
	selector, err := newTraceSelector(&SelectiveExportConfig{
		Enabled: true,
		Rules:   []SelectionRule{{ID: "all", Priority: 0, ExportRate: 1, MaxPerMinute: 2}},
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	plugin := &OtelPlugin{pluginSpanFilter: &PluginSpanFilter{}, selector: selector, targets: []*otelTarget{{serviceName: "test", client: client, exportTimeout: time.Second}}}
	for i := 0; i < 5; i++ {
		if err := plugin.Inject(context.Background(), imageSelectionTrace(fmt.Sprintf("%032x", i+31), time.Second)); err != nil {
			t.Fatalf("Inject() error = %v", err)
		}
	}
	if calls := client.calls.Load(); calls != 2 {
		t.Fatalf("OTLP calls = %d, want per-rule quota 2", calls)
	}
}

func TestInjectSelectiveExportDryRunAndNonImagePreserveExistingExports(t *testing.T) {
	client := &countingTestOtelClient{}
	selector, err := newTraceSelector(&SelectiveExportConfig{
		Enabled: true, DryRun: true,
		Rules: []SelectionRule{{ID: "drop", Priority: 0, ExportRate: 0}},
	})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	plugin := &OtelPlugin{pluginSpanFilter: &PluginSpanFilter{}, selector: selector, targets: []*otelTarget{{serviceName: "test", client: client, exportTimeout: time.Second}}}
	if err := plugin.Inject(context.Background(), imageSelectionTrace("00000000000000000000000000000041", time.Second)); err != nil {
		t.Fatalf("dry-run Inject() error = %v", err)
	}

	now := time.Now()
	chatSpan := &schemas.Span{SpanID: "2222222222222222", Kind: schemas.SpanKindLLMCall, StartTime: now, EndTime: now.Add(time.Second), Attributes: map[string]any{schemas.AttrLegacyRequestType: string(schemas.ChatCompletionRequest)}}
	chatTrace := &schemas.Trace{TraceID: "00000000000000000000000000000042", RootSpan: chatSpan, Spans: []*schemas.Span{chatSpan}}
	selector.dryRun = false
	if err := plugin.Inject(context.Background(), chatTrace); err != nil {
		t.Fatalf("chat Inject() error = %v", err)
	}
	if calls := client.calls.Load(); calls != 2 {
		t.Fatalf("OTLP calls = %d, want dry-run image and unaffected chat exported", calls)
	}
}

func TestInjectPinsSelectivePolicySnapshotAcrossHotReload(t *testing.T) {
	oldSelector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{ID: "old-keep", Priority: 0, ExportRate: 1}}})
	if err != nil {
		t.Fatalf("old selector: %v", err)
	}
	newSelector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{ID: "new-drop", Priority: 0, ExportRate: 0}}})
	if err != nil {
		t.Fatalf("new selector: %v", err)
	}
	client := &countingTestOtelClient{}
	plugin := &OtelPlugin{pluginSpanFilter: &PluginSpanFilter{}, selector: newSelector, targets: []*otelTarget{{serviceName: "test", client: client, exportTimeout: time.Second}}}
	trace := imageSelectionTrace("00000000000000000000000000000051", time.Second)
	trace.SetAttribute("bifrost.internal.media_capture_policy_snapshots", map[string]any{PluginName: oldSelector})
	if err := plugin.Inject(context.Background(), trace); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if calls := client.calls.Load(); calls != 1 {
		t.Fatalf("OTLP calls = %d, want in-flight request pinned to old keep policy", calls)
	}
}

func TestInjectPinsDisabledPolicyAcrossHotReload(t *testing.T) {
	newSelector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{ID: "new-drop-disabled-old", ExportRate: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	client := &countingTestOtelClient{}
	plugin := &OtelPlugin{pluginSpanFilter: &PluginSpanFilter{}, selector: newSelector, targets: []*otelTarget{{serviceName: "test", client: client, exportTimeout: time.Second}}}
	trace := imageSelectionTrace("disabled-to-enabled", time.Second)
	trace.SetAttribute(schemas.TraceAttrMediaPolicySnapshots, map[string]any{PluginName: &selectorSnapshot{selector: nil}})
	if err := plugin.Inject(context.Background(), trace); err != nil {
		t.Fatal(err)
	}
	if calls := client.calls.Load(); calls != 1 {
		t.Fatalf("OTLP calls = %d, want request started while selection was disabled exported", calls)
	}
}

func TestSelectionQuotaSurvivesPolicyReload(t *testing.T) {
	resetSelectionQuotaLedgerForTest()
	first, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, MaxExportsPerMinute: 1, Rules: []SelectionRule{{ID: "reload-global", ExportRate: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, MaxExportsPerMinute: 1, Rules: []SelectionRule{{ID: "reload-global", ExportRate: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if !first.shouldExport(imageSelectionTrace("quota-before-reload", time.Second)) {
		t.Fatal("first selector unexpectedly rejected quota")
	}
	if second.shouldExport(imageSelectionTrace("quota-after-reload", time.Second)) {
		t.Fatal("hot reload reset the process-wide quota")
	}
}

func int64Ptr(value int64) *int64       { return &value }
func float64Ptr(value float64) *float64 { return &value }
