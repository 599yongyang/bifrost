package tracing

import (
	"context"
	"encoding/base64"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
)

type testRealtimeObservabilityPlugin struct {
	injected        chan *schemas.Trace
	injectedPayload chan string
}

func TestSetObservabilityPluginsSkipsTypedNil(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()
	var plugin *testRealtimeObservabilityPlugin
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{plugin})
	slots := tracer.obsPlugins.Load()
	if slots == nil || len(*slots) != 0 {
		t.Fatalf("observability slots = %v, want empty", slots)
	}
}

type rejectMediaCapturePlugin struct {
	testRealtimeObservabilityPlugin
}

type flippingMediaCapturePlugin struct {
	testRealtimeObservabilityPlugin
	calls atomic.Int64
}

func (p *flippingMediaCapturePlugin) BeginTraceMediaCapture(string, *schemas.BifrostRequest) schemas.TraceMediaCaptureDecision {
	return schemas.TraceMediaCaptureDecision{Capture: p.calls.Add(1) == 1, PolicySnapshot: "first-policy"}
}

type panickingMediaCapturePlugin struct {
	testRealtimeObservabilityPlugin
}

type imageOnlyRejectMediaCapturePlugin struct {
	testRealtimeObservabilityPlugin
	calls atomic.Int64
}

func (p *imageOnlyRejectMediaCapturePlugin) BeginTraceMediaCapture(_ string, request *schemas.BifrostRequest) schemas.TraceMediaCaptureDecision {
	p.calls.Add(1)
	return schemas.TraceMediaCaptureDecision{Capture: request == nil || !isImageTracingRequestType(string(request.RequestType))}
}

func (*panickingMediaCapturePlugin) BeginTraceMediaCapture(string, *schemas.BifrostRequest) schemas.TraceMediaCaptureDecision {
	panic("broken dynamic policy")
}

func (*rejectMediaCapturePlugin) BeginTraceMediaCapture(string, *schemas.BifrostRequest) schemas.TraceMediaCaptureDecision {
	return schemas.TraceMediaCaptureDecision{Capture: false}
}

func (p *testRealtimeObservabilityPlugin) GetName() string { return "test-observability" }
func (p *testRealtimeObservabilityPlugin) Cleanup() error  { return nil }
func (p *testRealtimeObservabilityPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *testRealtimeObservabilityPlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (p *testRealtimeObservabilityPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}
func (p *testRealtimeObservabilityPlugin) Inject(_ context.Context, trace *schemas.Trace) error {
	if p.injectedPayload != nil {
		serialized, err := sonic.MarshalString(trace)
		if err != nil {
			return err
		}
		p.injectedPayload <- serialized
		return nil
	}
	if trace == nil {
		p.injected <- nil
		return nil
	}
	// SnapshotForExport, not `*trace`: Trace carries a sync.Mutex, so a struct
	// copy duplicates the lock (go vet: "assignment copies lock value") and
	// still shares the Spans slice and attribute maps with the original. The
	// helper takes the lock and deep-copies spans and maps, which is what a
	// snapshot handed across a channel actually needs.
	p.injected <- trace.SnapshotForExport()
	return nil
}

func TestTracer_CompleteAndFlushTraceInjectsObservabilityPlugins(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	plugin := &testRealtimeObservabilityPlugin{
		injected: make(chan *schemas.Trace, 1),
	}

	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{plugin})
	tracer.CompleteAndFlushTrace(traceID)

	select {
	case trace := <-plugin.injected:
		if trace == nil || trace.TraceID != traceID {
			t.Fatalf("injected trace = %+v, want trace %q", trace, traceID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for observability inject")
	}

	if got := tracer.store.GetTrace(traceID); got != nil {
		t.Fatalf("trace %q was not released after flush", traceID)
	}
}

func TestPopulateLLMResponseAttributesIncludesErrorFallbackDecision(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	ctx.SetValue(schemas.BifrostContextKeyTraceID, traceID)
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackRuleName, "content-policy")
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackCategory, string(schemas.FailureCategoryContentPolicy))
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackMatchSource, "classifier.azure.message")
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackMatchDetail, "rejected_by_safety_system")
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackMatchedBy, "provider_pack")
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackPack, "azure_content_policy")
	ctx.SetValue(schemas.BifrostContextKeyErrorFallbackPatternID, "azure_safety_system")
	_, handle := tracer.StartSpan(ctx, "fallback", schemas.SpanKindFallback)
	tracer.PopulateLLMResponseAttributes(ctx, handle, &schemas.BifrostResponse{}, nil)

	internalHandle, ok := handle.(*spanHandle)
	if !ok || internalHandle == nil {
		t.Fatal("expected concrete span handle")
	}
	span := store.GetTrace(traceID).GetSpan(internalHandle.spanID)
	if got := span.Attributes[schemas.AttrBifrostErrorFallbackRule]; got != "content-policy" {
		t.Fatalf("error fallback rule attribute = %v, want content-policy", got)
	}
	if got := span.Attributes[schemas.AttrBifrostErrorFallbackCategory]; got != string(schemas.FailureCategoryContentPolicy) {
		t.Fatalf("error fallback category attribute = %v, want %q", got, schemas.FailureCategoryContentPolicy)
	}
	if got := span.Attributes[schemas.AttrBifrostErrorFallbackMatchSource]; got != "classifier.azure.message" {
		t.Fatalf("error fallback match source attribute = %v, want classifier.azure.message", got)
	}
	if got := span.Attributes[schemas.AttrBifrostErrorFallbackMatchDetail]; got != "rejected_by_safety_system" {
		t.Fatalf("error fallback match detail attribute = %v, want rejected_by_safety_system", got)
	}
	if got := span.Attributes[schemas.AttrBifrostErrorFallbackMatchedBy]; got != "provider_pack" {
		t.Fatalf("error fallback matched-by attribute = %v, want provider_pack", got)
	}
	if got := span.Attributes[schemas.AttrBifrostErrorFallbackPack]; got != "azure_content_policy" {
		t.Fatalf("error fallback pack attribute = %v, want azure_content_policy", got)
	}
	if got := span.Attributes[schemas.AttrBifrostErrorFallbackPatternID]; got != "azure_safety_system" {
		t.Fatalf("error fallback pattern attribute = %v, want azure_safety_system", got)
	}
}

func TestTracer_CompleteAndFlushTraceRedactsContentBeforeInject(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	plugin := &testRealtimeObservabilityPlugin{
		injectedPayload: make(chan string, 1),
	}
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{plugin})

	// Store replacements before output attributes are populated. This mirrors
	// streaming, where the final accumulated output lands near trace completion.
	tracer.SetTraceRedactionReplacements(traceID, schemas.RedactionPhaseInput, map[string]string{
		"alex@example.com": "[EMAIL-INPUT]",
	})
	tracer.SetTraceRedactionReplacements(traceID, schemas.RedactionPhaseOutput, map[string]string{
		"alex@example.com": "[EMAIL-OUTPUT]",
	})

	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	rootCtx, rootHandle := tracer.StartSpan(ctx, "http-request", schemas.SpanKindHTTPRequest)
	tracer.SetAttribute(rootHandle, schemas.AttrInputMessages, `{"content":"email alex@example.com"}`)
	_, childHandle := tracer.StartSpan(rootCtx, "llm-call", schemas.SpanKindLLMCall)
	tracer.SetAttribute(childHandle, schemas.AttrOutputMessages, `{"content":"reply alex@example.com"}`)
	tracer.SetAttribute(childHandle, schemas.AttrRequestModel, "gpt-4o-mini")

	tracer.CompleteAndFlushTrace(traceID)

	select {
	case payload := <-plugin.injectedPayload:
		if strings.Contains(payload, "alex@example.com") {
			t.Fatalf("injected trace leaked raw content: %s", payload)
		}
		if !strings.Contains(payload, "[EMAIL-INPUT]") {
			t.Fatalf("injected trace missing input redacted placeholder: %s", payload)
		}
		if !strings.Contains(payload, "[EMAIL-OUTPUT]") {
			t.Fatalf("injected trace missing output redacted placeholder: %s", payload)
		}
		if !strings.Contains(payload, "gpt-4o-mini") {
			t.Fatalf("injected trace should retain non-content attributes: %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for observability inject")
	}
}

func TestTracer_ImageEditUsesMediaSidecar(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	_, handle := tracer.StartSpan(ctx, "generate_content gpt-image-2", schemas.SpanKindLLMCall)
	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	mask := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3, 4, 'I', 'D', 'A', 'T'}
	tracer.PopulateLLMRequestAttributes(handle, &schemas.BifrostRequest{
		RequestType: schemas.ImageEditRequest,
		ImageEditRequest: &schemas.BifrostImageEditRequest{
			Provider: schemas.OpenAI,
			Model:    "gpt-image-2",
			Input: &schemas.ImageEditInput{
				Prompt: "replace the sky",
				Images: []schemas.ImageInput{{Image: image}},
			},
			Params: &schemas.ImageEditParameters{Mask: mask, Size: ptr("1024x1024"), Quality: ptr("high"), N: ptr(1)},
		},
	})

	trace := tracer.EndTrace(traceID)
	if trace == nil {
		t.Fatal("trace was not completed")
	}
	defer tracer.ReleaseTrace(trace)
	mediaAttachments := trace.MediaAttachments()
	if len(mediaAttachments) != 2 {
		t.Fatalf("trace media count = %d, want image and mask", len(mediaAttachments))
	}
	for _, media := range mediaAttachments {
		if media.MIMEType != "image/png" || media.Bytes != 16 || len(media.SHA256) != 64 {
			t.Fatalf("media summary = %+v, want png/16 bytes/sha256", media)
		}
		if len(media.Data) != 16 {
			t.Fatalf("sidecar data bytes = %d, want 16", len(media.Data))
		}
	}
	span := trace.GetSpan(handle.(*spanHandle).spanID)
	raw, _ := span.Attributes[schemas.AttrBifrostImageInput].(string)
	if raw == "" || strings.Contains(raw, "iVBOR") || strings.Contains(raw, "89504e47") {
		t.Fatalf("span input must contain references, not image bytes: %q", raw)
	}
	var input map[string]any
	if err := schemas.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("image input is not JSON: %v", err)
	}
	images, _ := input["images"].([]any)
	if len(images) != 1 || input["mask"] == nil {
		t.Fatalf("image edit input = %#v, want image and mask summaries", input)
	}
}

func TestTracer_ImageMediaCaptureDegradesForUnsupportedAndOversizedFiles(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	_, handle := tracer.StartSpan(ctx, "image_variation dall-e-2", schemas.SpanKindLLMCall)
	oversized := make([]byte, maxTraceMediaBytes+1)
	copy(oversized, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	tracer.PopulateLLMRequestAttributes(handle, &schemas.BifrostRequest{
		RequestType: schemas.ImageVariationRequest,
		ImageVariationRequest: &schemas.BifrostImageVariationRequest{
			Input: &schemas.ImageVariationInput{Image: schemas.ImageInput{Image: oversized}},
		},
	})

	trace := tracer.EndTrace(traceID)
	if trace == nil {
		t.Fatal("trace was not completed")
	}
	defer tracer.ReleaseTrace(trace)
	if mediaCount := len(trace.MediaAttachments()); mediaCount != 0 {
		t.Fatalf("oversized media payload was retained: %d attachments", mediaCount)
	}
	span := trace.GetSpan(handle.(*spanHandle).spanID)
	raw, _ := span.Attributes[schemas.AttrBifrostImageInput].(string)
	if !strings.Contains(raw, `"capture_status":"too_large"`) {
		t.Fatalf("oversized capture summary = %q, want too_large", raw)
	}
}

func TestTracer_ImageVariationCapturesInputMedia(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	_, handle := tracer.StartSpan(ctx, "image_variation dall-e-2", schemas.SpanKindLLMCall)
	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	tracer.PopulateLLMRequestAttributes(handle, &schemas.BifrostRequest{
		RequestType: schemas.ImageVariationRequest,
		ImageVariationRequest: &schemas.BifrostImageVariationRequest{
			Provider: schemas.OpenAI,
			Model:    "dall-e-2",
			Input:    &schemas.ImageVariationInput{Image: schemas.ImageInput{Image: image}},
			Params:   &schemas.ImageVariationParameters{Size: ptr("1024x1024"), N: ptr(1)},
		},
	})

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	attachments := trace.MediaAttachments()
	if len(attachments) != 1 || attachments[0].Field != "input" || attachments[0].MIMEType != "image/png" {
		t.Fatalf("variation media = %+v, want one PNG input", attachments)
	}
	span := trace.GetSpan(handle.(*spanHandle).spanID)
	input, _ := span.Attributes[schemas.AttrBifrostImageInput].(string)
	if !strings.Contains(input, "bifrost-media://") || !strings.Contains(input, `"size":"1024x1024"`) {
		t.Fatalf("variation input summary = %q", input)
	}
}

func TestTracer_ImageBase64OutputUsesMediaSidecar(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	_, handle := tracer.StartSpan(ctx, "generate_content gpt-image-2", schemas.SpanKindLLMCall)
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	tracer.PopulateLLMResponseAttributes(ctx, handle, &schemas.BifrostResponse{
		ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{
			Model: "gpt-image-2-2026-08-01",
			Data: []schemas.ImageData{{
				B64JSON:       base64.StdEncoding.EncodeToString(image),
				RevisedPrompt: "A refined prompt",
			}},
			Usage: &schemas.ImageUsage{InputTokens: 11, OutputTokens: 22, TotalTokens: 33},
		},
	}, nil)

	trace := tracer.EndTrace(traceID)
	if trace == nil {
		t.Fatal("trace was not completed")
	}
	defer tracer.ReleaseTrace(trace)
	mediaAttachments := trace.MediaAttachments()
	if len(mediaAttachments) != 1 || string(mediaAttachments[0].Data) != string(image) || mediaAttachments[0].Field != "output" {
		t.Fatalf("output media sidecar = %+v, want decoded output image", mediaAttachments)
	}
	span := trace.GetSpan(handle.(*spanHandle).spanID)
	raw, _ := span.Attributes[schemas.AttrBifrostImageOutput].(string)
	if !strings.Contains(raw, "bifrost-media://") || strings.Contains(raw, base64.StdEncoding.EncodeToString(image)) {
		t.Fatalf("output summary must reference sidecar without base64: %q", raw)
	}
	if span.Attributes[schemas.AttrInputTokens] != 11 || span.Attributes[schemas.AttrOutputTokens] != 22 || span.Attributes[schemas.AttrTotalTokens] != 33 {
		t.Fatalf("image usage attributes = %#v, want 11/22/33", span.Attributes)
	}
}

func TestTracer_MediaCapturePolicySkipsInputCopyAndBase64Decode(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{&rejectMediaCapturePlugin{}})

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	_, handle := tracer.StartSpan(ctx, "image_edit image-model", schemas.SpanKindLLMCall)
	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	tracer.PopulateLLMRequestAttributes(handle, &schemas.BifrostRequest{
		RequestType: schemas.ImageEditRequest,
		ImageEditRequest: &schemas.BifrostImageEditRequest{
			Input: &schemas.ImageEditInput{Prompt: "edit", Images: []schemas.ImageInput{{Image: image}}},
		},
	})
	tracer.PopulateLLMResponseAttributes(ctx, handle, &schemas.BifrostResponse{
		ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{Data: []schemas.ImageData{{B64JSON: base64.StdEncoding.EncodeToString(image)}}},
	}, nil)

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	if attachments := trace.MediaAttachments(); len(attachments) != 0 {
		t.Fatalf("capture-rejected trace retained %d media attachments", len(attachments))
	}
	span := trace.GetSpan(handle.(*spanHandle).spanID)
	input, _ := span.Attributes[schemas.AttrBifrostImageInput].(string)
	output, _ := span.Attributes[schemas.AttrBifrostImageOutput].(string)
	if !strings.Contains(input, `"capture_status":"metadata_only"`) || !strings.Contains(output, `"capture_status":"metadata_only"`) {
		t.Fatalf("capture-rejected summaries input=%s output=%s", input, output)
	}
}

func TestTracer_MediaCapturePolicyIsPinnedOnFirstAttempt(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()
	policy := &flippingMediaCapturePlugin{}
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{policy})

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	_, first := tracer.StartSpan(ctx, "image_edit primary", schemas.SpanKindLLMCall)
	_, fallback := tracer.StartSpan(ctx, "image_edit fallback", schemas.SpanKindLLMCall)
	request := &schemas.BifrostRequest{RequestType: schemas.ImageEditRequest, ImageEditRequest: &schemas.BifrostImageEditRequest{
		Input: &schemas.ImageEditInput{Prompt: "edit", Images: []schemas.ImageInput{{Image: []byte("not-an-image")}}},
	}}
	tracer.PopulateLLMRequestAttributes(first, request)
	tracer.PopulateLLMRequestAttributes(fallback, request)

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	if calls := policy.calls.Load(); calls != 1 {
		t.Fatalf("media policy calls = %d, want one pinned decision across fallback attempts", calls)
	}
	if eligible, ok := trace.GetAttribute(schemas.TraceAttrMediaCaptureEligible); !ok || eligible != true {
		t.Fatalf("media capture eligibility = %#v, %v; want pinned true", eligible, ok)
	}
}

func TestTracer_NonImageSpanDoesNotPinImageMediaCapturePolicy(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()
	policy := &imageOnlyRejectMediaCapturePlugin{}
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{policy})

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	_, internal := tracer.StartSpan(ctx, "internal embedding", schemas.SpanKindLLMCall)
	tracer.PopulateLLMRequestAttributes(internal, &schemas.BifrostRequest{RequestType: schemas.EmbeddingRequest})
	_, image := tracer.StartSpan(ctx, "image_edit model", schemas.SpanKindLLMCall)
	tracer.PopulateLLMRequestAttributes(image, &schemas.BifrostRequest{RequestType: schemas.ImageEditRequest, ImageEditRequest: &schemas.BifrostImageEditRequest{
		Input: &schemas.ImageEditInput{Prompt: "edit", Images: []schemas.ImageInput{{Image: []byte("image")}}},
	}})

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	if calls := policy.calls.Load(); calls != 1 {
		t.Fatalf("media policy calls = %d, want only the image request to initialize it", calls)
	}
	if eligible, ok := trace.GetAttribute(schemas.TraceAttrMediaCaptureEligible); !ok || eligible != false {
		t.Fatalf("image media eligibility = %#v, %v; want false", eligible, ok)
	}
}

func TestTracer_MediaCapturePolicyPanicIsIsolatedAndFailsClosed(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{&panickingMediaCapturePlugin{}})

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	_, handle := tracer.StartSpan(ctx, "image_generation model", schemas.SpanKindLLMCall)
	tracer.PopulateLLMRequestAttributes(handle, &schemas.BifrostRequest{
		RequestType:            schemas.ImageGenerationRequest,
		ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{Input: &schemas.ImageGenerationInput{Prompt: "safe"}},
	})

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	if eligible, ok := trace.GetAttribute(schemas.TraceAttrMediaCaptureEligible); !ok || eligible != false {
		t.Fatalf("media capture eligibility = %#v, %v; want fail-closed false", eligible, ok)
	}
}

func TestTracer_FallbackAttemptsReuseIdenticalInputMedia(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	request := &schemas.BifrostRequest{RequestType: schemas.ImageEditRequest, ImageEditRequest: &schemas.BifrostImageEditRequest{
		Input: &schemas.ImageEditInput{Prompt: "edit", Images: []schemas.ImageInput{{Image: image}}},
	}}
	for _, name := range []string{"image_edit primary", "image_edit fallback"} {
		_, handle := tracer.StartSpan(ctx, name, schemas.SpanKindLLMCall)
		tracer.PopulateLLMRequestAttributes(handle, request)
	}

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	if attachments := trace.MediaAttachments(); len(attachments) != 1 {
		t.Fatalf("fallback attempts retained %d copies of identical input, want 1", len(attachments))
	}
}

func TestTracer_ImageFallbackRecordsPublicAndProviderModels(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	primaryProvider := schemas.OpenAI
	primaryModel := "gpt-image-public"
	ctx.SetValue(schemas.BifrostContextKeyRoutingInfo, schemas.RoutingInfo{
		Provider: schemas.Azure, Model: "fallback-deployment", IsFallback: true,
		PrimaryProvider: &primaryProvider, PrimaryModel: &primaryModel,
		ResolvedKeyAlias: &schemas.ResolvedKeyAlias{ModelID: "azure-image-wire-model"},
	})
	_, handle := tracer.StartSpan(ctx, "image_generation fallback-deployment", schemas.SpanKindLLMCall)
	tracer.SetAttribute(handle, schemas.AttrLegacyRequestType, string(schemas.ImageGenerationRequest))
	tracer.PopulateLLMRequestAttributes(handle, &schemas.BifrostRequest{
		RequestType: schemas.ImageGenerationRequest,
		ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{
			Provider: schemas.Azure, Model: "fallback-deployment", Input: &schemas.ImageGenerationInput{Prompt: "a lighthouse"},
		},
	})
	tracer.PopulateLLMResponseAttributes(ctx, handle, &schemas.BifrostResponse{
		ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{Model: "azure-image-wire-model"},
	}, nil)

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	span := trace.GetSpan(handle.(*spanHandle).spanID)
	if got := span.Attributes[schemas.AttrBifrostPublicModel]; got != primaryModel {
		t.Fatalf("public model = %v, want %q", got, primaryModel)
	}
	if got := span.Attributes[schemas.AttrBifrostProviderModel]; got != "azure-image-wire-model" {
		t.Fatalf("provider model = %v, want azure wire model", got)
	}
}

func TestTracer_ImageFallbackPrefersResponseRoutingInfo(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := schemas.NewBifrostContext(context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID), time.Time{})
	_, handle := tracer.StartSpan(ctx, "image_generation fallback-deployment", schemas.SpanKindLLMCall)
	tracer.SetAttribute(handle, schemas.AttrLegacyRequestType, string(schemas.ImageGenerationRequest))
	primaryProvider := schemas.OpenAI
	primaryModel := "public-image-model"
	response := &schemas.BifrostResponse{ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{Model: "wire-image-model"}}
	response.ImageGenerationResponse.ExtraFields.RoutingInfo = schemas.RoutingInfo{
		Provider: schemas.Azure, Model: "fallback-deployment", IsFallback: true,
		PrimaryProvider: &primaryProvider, PrimaryModel: &primaryModel,
		ResolvedKeyAlias: &schemas.ResolvedKeyAlias{ModelID: "wire-image-model"},
	}
	tracer.PopulateLLMResponseAttributes(ctx, handle, response, nil)

	trace := tracer.EndTrace(traceID)
	defer tracer.ReleaseTrace(trace)
	span := trace.GetSpan(handle.(*spanHandle).spanID)
	if span.Attributes[schemas.AttrBifrostPublicModel] != primaryModel || span.Attributes[schemas.AttrBifrostProviderModel] != "wire-image-model" {
		t.Fatalf("image fallback models = public:%v provider:%v", span.Attributes[schemas.AttrBifrostPublicModel], span.Attributes[schemas.AttrBifrostProviderModel])
	}
}

func TestTracer_SetTraceRedactionReplacementsSurvivesLaterObservabilityPlugins(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	tracer.SetTraceRedactionReplacements(traceID, schemas.RedactionPhaseInput, map[string]string{
		"alex@example.com": "[EMAIL-1]",
	})

	plugin := &testRealtimeObservabilityPlugin{
		injectedPayload: make(chan string, 1),
	}
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{plugin})

	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	_, rootHandle := tracer.StartSpan(ctx, "http-request", schemas.SpanKindHTTPRequest)
	tracer.SetAttribute(rootHandle, schemas.AttrInputMessages, `{"content":"email alex@example.com"}`)
	tracer.CompleteAndFlushTrace(traceID)

	select {
	case payload := <-plugin.injectedPayload:
		if strings.Contains(payload, "alex@example.com") {
			t.Fatalf("trace redaction replacements should survive later observability plugin registration: %s", payload)
		}
		if !strings.Contains(payload, "[EMAIL-1]") {
			t.Fatalf("injected trace missing redacted placeholder: %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for observability inject")
	}
}

func TestTracer_StartSpan_RootSpanWithW3CParent(t *testing.T) {
	// This is the key test: verifies that when an incoming request has a W3C traceparent header,
	// the root span in Bifrost correctly links to the upstream service's span.
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	// Simulate incoming W3C traceparent: 00-{traceID}-{parentSpanID}-01
	inheritedTraceID := "69538b980000000079943934f90c1d40"
	externalParentSpanID := "aad09d1659b4c7e3"

	// Create trace with inherited trace ID. The returned value is a unique
	// per-request store key, not the inherited ID (issue #5256) — the W3C ID
	// lives on trace.TraceID for export.
	traceID := tracer.CreateTrace(inheritedTraceID)
	if traceID == inheritedTraceID {
		t.Errorf("CreateTrace() = %q, want a unique store key distinct from the inherited trace ID", traceID)
	}

	// Set up context with trace ID and parent span ID (as middleware would do)
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	ctx = context.WithValue(ctx, schemas.BifrostContextKeyParentSpanID, externalParentSpanID)

	// Create root span - this should link to the external parent
	newCtx, handle := tracer.StartSpan(ctx, "bifrost-http-request", schemas.SpanKindHTTPRequest)
	if handle == nil {
		t.Fatal("StartSpan() returned nil handle")
	}

	// Verify the span was created with correct parent
	trace := store.GetTrace(traceID)
	if trace == nil {
		t.Fatal("Trace not found in store")
	}

	if trace.RootSpan == nil {
		t.Fatal("Root span not set on trace")
	}

	// THE CRITICAL CHECK: Root span should have the external parent span ID
	if trace.RootSpan.ParentID != externalParentSpanID {
		t.Errorf("Root span ParentID = %q, want external parent span ID %q", trace.RootSpan.ParentID, externalParentSpanID)
	}

	// The exported W3C trace ID is preserved on both the trace and its spans;
	// only the store key returned by CreateTrace is the per-request handle.
	if trace.TraceID != inheritedTraceID {
		t.Errorf("trace.TraceID = %q, want inherited %q", trace.TraceID, inheritedTraceID)
	}
	if trace.RootSpan.TraceID != inheritedTraceID {
		t.Errorf("Root span TraceID = %q, want inherited %q", trace.RootSpan.TraceID, inheritedTraceID)
	}

	// Verify context has span ID for child span creation
	spanID, ok := newCtx.Value(schemas.BifrostContextKeySpanID).(string)
	if !ok || spanID == "" {
		t.Error("Context should have span ID after StartSpan()")
	}

	if spanID != trace.RootSpan.SpanID {
		t.Errorf("Context span ID = %q, want %q", spanID, trace.RootSpan.SpanID)
	}
}

func TestTracer_StartSpan_RootSpanWithoutW3CParent(t *testing.T) {
	// When there's no incoming W3C context, root span should have no parent
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	// Create new trace (no inherited trace ID)
	traceID := tracer.CreateTrace("")

	// Set up context with only trace ID (no parent span ID)
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)

	// Create root span
	_, handle := tracer.StartSpan(ctx, "local-request", schemas.SpanKindHTTPRequest)
	if handle == nil {
		t.Fatal("StartSpan() returned nil handle")
	}

	trace := store.GetTrace(traceID)
	if trace == nil {
		t.Fatal("Trace not found in store")
	}

	// Root span should have no parent
	if trace.RootSpan.ParentID != "" {
		t.Errorf("Root span ParentID = %q, want empty string (no W3C parent)", trace.RootSpan.ParentID)
	}
}

func TestTracer_StartSpan_ChildSpanLinking(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	inheritedTraceID := "69538b980000000079943934f90c1d40"
	externalParentSpanID := "aad09d1659b4c7e3"

	traceID := tracer.CreateTrace(inheritedTraceID)

	// Set up context with W3C parent span ID
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	ctx = context.WithValue(ctx, schemas.BifrostContextKeyParentSpanID, externalParentSpanID)

	// Create root span
	rootCtx, rootHandle := tracer.StartSpan(ctx, "http-request", schemas.SpanKindHTTPRequest)
	if rootHandle == nil {
		t.Fatal("StartSpan() returned nil handle for root span")
	}

	// Create child span using the context from root span
	childCtx, childHandle := tracer.StartSpan(rootCtx, "llm-call", schemas.SpanKindLLMCall)
	if childHandle == nil {
		t.Fatal("StartSpan() returned nil handle for child span")
	}

	trace := store.GetTrace(traceID)

	// Find the child span
	var childSpan *schemas.Span
	for _, span := range trace.Spans {
		if span.Name == "llm-call" {
			childSpan = span
			break
		}
	}

	if childSpan == nil {
		t.Fatal("Child span not found in trace")
	}

	// Child span should have root span as parent (not the external parent)
	if childSpan.ParentID != trace.RootSpan.SpanID {
		t.Errorf("Child span ParentID = %q, want root span ID %q", childSpan.ParentID, trace.RootSpan.SpanID)
	}

	// Create grandchild span
	_, grandchildHandle := tracer.StartSpan(childCtx, "plugin-call", schemas.SpanKindPlugin)
	if grandchildHandle == nil {
		t.Fatal("StartSpan() returned nil handle for grandchild span")
	}

	// Find the grandchild span
	var grandchildSpan *schemas.Span
	for _, span := range trace.Spans {
		if span.Name == "plugin-call" {
			grandchildSpan = span
			break
		}
	}

	if grandchildSpan == nil {
		t.Fatal("Grandchild span not found in trace")
	}

	// Grandchild should have child as parent
	if grandchildSpan.ParentID != childSpan.SpanID {
		t.Errorf("Grandchild span ParentID = %q, want child span ID %q", grandchildSpan.ParentID, childSpan.SpanID)
	}
}

func TestTracer_StartSpan_NoTraceID(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	// Context without trace ID
	ctx := context.Background()

	newCtx, handle := tracer.StartSpan(ctx, "operation", schemas.SpanKindHTTPRequest)
	if handle != nil {
		t.Error("StartSpan() should return nil handle when no trace ID in context")
	}

	// Context should be unchanged
	if newCtx != ctx {
		t.Error("Context should be unchanged when StartSpan() fails")
	}
}

func TestTracer_EndTrace_ReturnsTraceData(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	inheritedTraceID := "69538b980000000079943934f90c1d40"
	externalParentSpanID := "aad09d1659b4c7e3"

	traceID := tracer.CreateTrace(inheritedTraceID)

	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	ctx = context.WithValue(ctx, schemas.BifrostContextKeyParentSpanID, externalParentSpanID)

	_, rootHandle := tracer.StartSpan(ctx, "http-request", schemas.SpanKindHTTPRequest)
	tracer.EndSpan(rootHandle, schemas.SpanStatusOk, "")

	trace := tracer.EndTrace(traceID)
	if trace == nil {
		t.Fatal("EndTrace() returned nil")
	}

	if trace.TraceID != inheritedTraceID {
		t.Errorf("trace.TraceID = %q, want %q", trace.TraceID, inheritedTraceID)
	}

	if len(trace.Spans) != 1 {
		t.Errorf("len(trace.Spans) = %d, want 1", len(trace.Spans))
	}

	// Root span should still have external parent
	if trace.RootSpan.ParentID != externalParentSpanID {
		t.Errorf("Root span ParentID = %q, want %q", trace.RootSpan.ParentID, externalParentSpanID)
	}
}

func TestTracer_SetAttribute(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)

	_, handle := tracer.StartSpan(ctx, "operation", schemas.SpanKindHTTPRequest)

	tracer.SetAttribute(handle, "http.method", "POST")
	tracer.SetAttribute(handle, "http.status_code", 200)

	trace := store.GetTrace(traceID)
	span := trace.RootSpan

	if span.Attributes["http.method"] != "POST" {
		t.Errorf("span attribute http.method = %v, want POST", span.Attributes["http.method"])
	}

	if span.Attributes["http.status_code"] != 200 {
		t.Errorf("span attribute http.status_code = %v, want 200", span.Attributes["http.status_code"])
	}
}

func TestTracer_GetSpanHandleByID_RootSpan(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)

	rootCtx, rootHandle := tracer.StartSpan(ctx, "http-request", schemas.SpanKindHTTPRequest)
	_, childHandle := tracer.StartSpan(rootCtx, "llm-call", schemas.SpanKindLLMCall)

	tracer.SetAttribute(childHandle, "child.attr", "child-value")

	handle := tracer.GetSpanHandleByID(traceID, nil)
	if handle == nil {
		t.Fatal("GetSpanHandleByID(traceID, nil) returned nil")
	}
	tracer.SetAttribute(handle, "custom.root_attr", "root-value")

	trace := store.GetTrace(traceID)
	if trace.RootSpan.Attributes["custom.root_attr"] != "root-value" {
		t.Fatalf("root span custom attr = %v, want root-value", trace.RootSpan.Attributes["custom.root_attr"])
	}
	if trace.RootSpan.Attributes["child.attr"] != nil {
		t.Fatalf("child attr leaked to root span: %v", trace.RootSpan.Attributes["child.attr"])
	}

	childSpanID := childHandle.(*spanHandle).spanID
	childSpan := trace.GetSpan(childSpanID)
	if childSpan.Attributes["custom.root_attr"] != nil {
		t.Fatalf("root attr leaked to child span: %v", childSpan.Attributes["custom.root_attr"])
	}

	if rootHandle.(*spanHandle).spanID != trace.RootSpan.SpanID {
		t.Fatalf("root handle span ID = %q, want %q", rootHandle.(*spanHandle).spanID, trace.RootSpan.SpanID)
	}
}

func TestTracer_GetSpanHandleByID_ExplicitSpan(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)

	rootCtx, _ := tracer.StartSpan(ctx, "http-request", schemas.SpanKindHTTPRequest)
	_, childHandle := tracer.StartSpan(rootCtx, "llm-call", schemas.SpanKindLLMCall)
	childSpanID := childHandle.(*spanHandle).spanID

	handle := tracer.GetSpanHandleByID(traceID, &childSpanID)
	if handle == nil {
		t.Fatal("GetSpanHandleByID(traceID, childSpanID) returned nil")
	}
	tracer.SetAttribute(handle, "custom.child_attr", "child-value")

	trace := store.GetTrace(traceID)
	childSpan := trace.GetSpan(childSpanID)
	if childSpan.Attributes["custom.child_attr"] != "child-value" {
		t.Fatalf("child span custom attr = %v, want child-value", childSpan.Attributes["custom.child_attr"])
	}
	if trace.RootSpan.Attributes["custom.child_attr"] != nil {
		t.Fatalf("child attr leaked to root span: %v", trace.RootSpan.Attributes["custom.child_attr"])
	}
}

func TestTracer_GetSpanHandleByID_MissingInputs(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	if handle := tracer.GetSpanHandleByID("", nil); handle != nil {
		t.Fatalf("empty trace ID handle = %v, want nil", handle)
	}
	if handle := tracer.GetSpanHandleByID("missing-trace", nil); handle != nil {
		t.Fatalf("missing trace handle = %v, want nil", handle)
	}

	traceID := tracer.CreateTrace("")
	if handle := tracer.GetSpanHandleByID(traceID, nil); handle != nil {
		t.Fatalf("trace with no root span handle = %v, want nil", handle)
	}

	missingSpanID := "missing-span"
	if handle := tracer.GetSpanHandleByID(traceID, &missingSpanID); handle != nil {
		t.Fatalf("missing span handle = %v, want nil", handle)
	}

	emptySpanID := ""
	if handle := tracer.GetSpanHandleByID(traceID, &emptySpanID); handle != nil {
		t.Fatalf("empty span ID handle = %v, want nil", handle)
	}
}

func TestTracer_AddEvent(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	traceID := tracer.CreateTrace("")
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)

	_, handle := tracer.StartSpan(ctx, "operation", schemas.SpanKindHTTPRequest)

	tracer.AddEvent(handle, "request.received", map[string]any{
		"size": 1024,
	})

	trace := store.GetTrace(traceID)
	span := trace.RootSpan

	if len(span.Events) != 1 {
		t.Fatalf("len(span.Events) = %d, want 1", len(span.Events))
	}

	if span.Events[0].Name != "request.received" {
		t.Errorf("event name = %q, want request.received", span.Events[0].Name)
	}

	if span.Events[0].Attributes["size"] != 1024 {
		t.Errorf("event attribute size = %v, want 1024", span.Events[0].Attributes["size"])
	}
}

// TestIntegration_FullDistributedTraceFlow tests the complete flow of receiving
// a distributed trace from an upstream service and properly linking spans.
func TestIntegration_FullDistributedTraceFlow(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()

	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	// Simulating headers from user's actual Datadog request:
	// traceparent: 00-69538b980000000079943934f90c1d40-aad09d1659b4c7e3-01
	inheritedTraceID := "69538b980000000079943934f90c1d40"
	externalParentSpanID := "aad09d1659b4c7e3"

	// Step 1: Middleware extracts trace context and creates trace
	traceID := tracer.CreateTrace(inheritedTraceID)

	// Step 2: Middleware sets up context (simulating what TracingMiddleware does)
	ctx := context.WithValue(context.Background(), schemas.BifrostContextKeyTraceID, traceID)
	ctx = context.WithValue(ctx, schemas.BifrostContextKeyParentSpanID, externalParentSpanID)

	// Step 3: Middleware creates root span
	httpCtx, httpHandle := tracer.StartSpan(ctx, "/v1/chat/completions", schemas.SpanKindHTTPRequest)
	tracer.SetAttribute(httpHandle, "http.method", "POST")

	// Step 4: Bifrost creates LLM call span
	llmCtx, llmHandle := tracer.StartSpan(httpCtx, "openai.chat.completions", schemas.SpanKindLLMCall)
	tracer.SetAttribute(llmHandle, "llm.model", "gpt-4")
	tracer.SetAttribute(llmHandle, "llm.provider", "openai")

	// Step 5: Plugin creates its own span
	_, pluginHandle := tracer.StartSpan(llmCtx, "governance-plugin", schemas.SpanKindPlugin)
	tracer.SetAttribute(pluginHandle, "plugin.name", "governance")

	// Step 6: Complete spans (in reverse order)
	tracer.EndSpan(pluginHandle, schemas.SpanStatusOk, "")
	tracer.EndSpan(llmHandle, schemas.SpanStatusOk, "")
	tracer.EndSpan(httpHandle, schemas.SpanStatusOk, "")

	// Step 7: Complete trace
	trace := tracer.EndTrace(traceID)

	// Verify the trace structure for Datadog
	if trace.TraceID != inheritedTraceID {
		t.Errorf("Trace ID should match inherited ID from Datadog: got %q, want %q", trace.TraceID, inheritedTraceID)
	}

	// Find spans by name
	var httpSpan, llmSpan, pluginSpan *schemas.Span
	for _, span := range trace.Spans {
		switch span.Name {
		case "/v1/chat/completions":
			httpSpan = span
		case "openai.chat.completions":
			llmSpan = span
		case "governance-plugin":
			pluginSpan = span
		}
	}

	if httpSpan == nil || llmSpan == nil || pluginSpan == nil {
		t.Fatal("Not all spans found in trace")
	}

	// Verify span hierarchy for Datadog linking:
	// External Parent (aad09d1659b4c7e3) -> HTTP Span -> LLM Span -> Plugin Span

	// HTTP span should link to Datadog's parent span
	if httpSpan.ParentID != externalParentSpanID {
		t.Errorf("HTTP span should link to Datadog parent: got ParentID %q, want %q",
			httpSpan.ParentID, externalParentSpanID)
	}

	// LLM span should be child of HTTP span
	if llmSpan.ParentID != httpSpan.SpanID {
		t.Errorf("LLM span should be child of HTTP span: got ParentID %q, want %q",
			llmSpan.ParentID, httpSpan.SpanID)
	}

	// Plugin span should be child of LLM span
	if pluginSpan.ParentID != llmSpan.SpanID {
		t.Errorf("Plugin span should be child of LLM span: got ParentID %q, want %q",
			pluginSpan.ParentID, llmSpan.SpanID)
	}

	// All spans carry the inherited W3C trace identity; the store key returned
	// by CreateTrace is a separate per-request handle.
	if httpSpan.TraceID != inheritedTraceID || llmSpan.TraceID != inheritedTraceID || pluginSpan.TraceID != inheritedTraceID {
		t.Error("All spans should have the inherited trace ID")
	}
	if trace.TraceID != inheritedTraceID {
		t.Errorf("trace.TraceID = %q, want inherited %q", trace.TraceID, inheritedTraceID)
	}

	t.Logf("Trace structure (for Datadog):")
	t.Logf("  Trace ID: %s", trace.TraceID)
	t.Logf("  External Parent Span: %s (from Datadog)", externalParentSpanID)
	t.Logf("    -> HTTP Span: %s (ParentID: %s)", httpSpan.SpanID, httpSpan.ParentID)
	t.Logf("      -> LLM Span: %s (ParentID: %s)", llmSpan.SpanID, llmSpan.ParentID)
	t.Logf("        -> Plugin Span: %s (ParentID: %s)", pluginSpan.SpanID, pluginSpan.ParentID)
}
