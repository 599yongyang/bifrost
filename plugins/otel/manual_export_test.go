package otel

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")

func TestNilOtelPluginManualExportSurfaceIsSafe(t *testing.T) {
	var plugin *OtelPlugin
	if ids := plugin.ObservationTargetIDs(); ids != nil {
		t.Fatalf("target IDs = %v, want nil", ids)
	}
	if plugin.ManualExportAvailable() {
		t.Fatal("nil plugin must not report manual export availability")
	}
	status, _, err := plugin.EnqueueManualExport(context.Background(), "log-1")
	if err == nil || status != logstore.ObservationExportStatusUnavailable {
		t.Fatalf("status=%q error=%v, want unavailable", status, err)
	}
}

func manualImageLog(requestType schemas.RequestType) *logstore.Log {
	latency := 1234.0
	return &logstore.Log{
		ID: "manual-log", Object: string(requestType), Provider: "openai", Model: "gpt-image-2",
		Status: "success", Timestamp: time.Now(), Latency: &latency,
		ImageGenerationOutputParsed: &schemas.BifrostImageGenerationResponse{Data: []schemas.ImageData{{URL: "https://images.example.test/output.png"}}},
	}
}

func TestManualTraceFromLogGenerationURL(t *testing.T) {
	entry := manualImageLog(schemas.ImageGenerationRequest)
	entry.ImageGenerationInputParsed = &schemas.ImageGenerationInput{Prompt: "a moonlit harbor"}
	trace, err := manualTraceFromLog(entry)
	if err != nil {
		t.Fatal(err)
	}
	span := finalSelectionSpan(trace)
	if span == nil || span.Attributes[schemas.AttrBifrostImageInput] == "" || span.Attributes[schemas.AttrBifrostImageOutput] == "" {
		t.Fatalf("manual trace missing image summaries: %+v", span)
	}
	if len(trace.MediaAttachments()) != 0 {
		t.Fatalf("URL-only generation stored %d media attachments", len(trace.MediaAttachments()))
	}
}

func TestManualTraceFromLogEditRestoresInputMaskAndBase64Output(t *testing.T) {
	entry := manualImageLog(schemas.ImageEditRequest)
	entry.ImageEditInputParsed = &schemas.ImageEditInput{Prompt: "edit", Images: []schemas.ImageInput{{Image: onePixelPNG}}}
	params, err := sonic.Marshal(&schemas.ImageEditParameters{Mask: onePixelPNG})
	if err != nil {
		t.Fatal(err)
	}
	entry.Params = string(params)
	entry.ImageGenerationOutputParsed.Data[0] = schemas.ImageData{B64JSON: base64.StdEncoding.EncodeToString(onePixelPNG)}
	trace, err := manualTraceFromLog(entry)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(trace.MediaAttachments()); got != 3 {
		t.Fatalf("media attachments = %d, want input + mask + output", got)
	}
}

func TestManualTraceFromLogRejectsUnavailableContent(t *testing.T) {
	entry := manualImageLog(schemas.ImageEditRequest)
	entry.ContentHidden = true
	if _, err := manualTraceFromLog(entry); manualFailureReason(err) != "content_hidden" {
		t.Fatalf("content-hidden reason = %q", manualFailureReason(err))
	}
	entry.ContentHidden = false
	if _, err := manualTraceFromLog(entry); manualFailureReason(err) != "missing_input_media" {
		t.Fatalf("missing-media reason = %q", manualFailureReason(err))
	}
}

type manualTestRepo struct {
	mu            sync.Mutex
	logs          map[string]*logstore.Log
	states        map[string]logstore.ObservationExport
	panicGet      bool
	batchFailures int
	batchCalls    int
}

func (r *manualTestRepo) GetLog(_ context.Context, id string) (*logstore.Log, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.panicGet {
		panic("repository secret")
	}
	entry := r.logs[id]
	if entry == nil {
		return nil, logstore.ErrNotFound
	}
	return entry, nil
}

func TestManualExportPanicTransitionsPendingStateToFailed(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport), panicGet: true}
	target := &otelTarget{id: "profile-0", client: &countingTestOtelClient{}, mediaUploader: &successfulManualUploader{}}
	p := &OtelPlugin{ctx: context.Background(), targets: []*otelTarget{target}}
	job := manualExportJob{logID: "panic-log", attempts: map[string]int{"profile-0": 1}, repo: repo}
	repo.states["panic-log\x00profile-0"] = logstore.ObservationExport{
		LogID: "panic-log", TargetID: "profile-0", Status: logstore.ObservationExportStatusPending,
	}

	p.runManualExportSafely(job)

	states, err := repo.GetObservationExports(context.Background(), []string{"panic-log"})
	if err != nil || len(states) != 1 {
		t.Fatalf("states=%v err=%v", states, err)
	}
	if states[0].Status != logstore.ObservationExportStatusFailed || states[0].Reason != "worker_panic" {
		t.Fatalf("panic state=%+v, want failed/worker_panic", states[0])
	}
}

func (r *manualTestRepo) UpsertObservationExport(_ context.Context, state *logstore.ObservationExport) error {
	r.mu.Lock()
	r.states[state.LogID+"\x00"+state.TargetID] = *state
	r.mu.Unlock()
	return nil
}

func (r *manualTestRepo) GetObservationExports(_ context.Context, ids []string) ([]logstore.ObservationExport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	states := make([]logstore.ObservationExport, 0)
	for _, state := range r.states {
		if _, ok := wanted[state.LogID]; ok {
			states = append(states, state)
		}
	}
	return states, nil
}

func (r *manualTestRepo) BatchUpsertObservationExports(ctx context.Context, states []logstore.ObservationExport) error {
	r.mu.Lock()
	r.batchCalls++
	if r.batchFailures > 0 {
		r.batchFailures--
		r.mu.Unlock()
		return fmt.Errorf("transient batch failure")
	}
	r.mu.Unlock()
	for i := range states {
		if err := r.UpsertObservationExport(ctx, &states[i]); err != nil {
			return err
		}
	}
	return nil
}

type successfulManualUploader struct{}

func (*successfulManualUploader) Upload(_ context.Context, _ string, media schemas.TraceMedia) (string, error) {
	return "@@@langfuseMedia:type=" + media.MIMEType + "|id=test@@@", nil
}
func (*successfulManualUploader) Close() {}

type sharedConcurrencyUploader struct {
	active atomic.Int32
	max    atomic.Int32
}

func (u *sharedConcurrencyUploader) Upload(ctx context.Context, _ string, media schemas.TraceMedia) (string, error) {
	active := u.active.Add(1)
	defer u.active.Add(-1)
	for {
		current := u.max.Load()
		if active <= current || u.max.CompareAndSwap(current, active) {
			break
		}
	}
	select {
	case <-time.After(30 * time.Millisecond):
		return "@@@langfuseMedia:type=" + media.MIMEType + "|id=shared@@@", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (*sharedConcurrencyUploader) Close() {}

type panickingManualClient struct{}

func (*panickingManualClient) Emit(context.Context, []*ResourceSpan) error { panic("client secret") }
func (*panickingManualClient) Close() error                                { return nil }

func TestManualExportPanicPreservesCompletedTargets(t *testing.T) {
	entry := manualImageLog(schemas.ImageGenerationRequest)
	entry.ImageGenerationInputParsed = &schemas.ImageGenerationInput{Prompt: "test"}
	repo := &manualTestRepo{logs: map[string]*logstore.Log{entry.ID: entry}, states: make(map[string]logstore.ObservationExport)}
	p := &OtelPlugin{ctx: context.Background(), targets: []*otelTarget{
		{id: "completed", client: &countingTestOtelClient{}, mediaUploader: &successfulManualUploader{}, exportTimeout: time.Second},
		{id: "panicked", client: &panickingManualClient{}, mediaUploader: &successfulManualUploader{}, exportTimeout: time.Second},
	}}
	job := manualExportJob{logID: entry.ID, attempts: map[string]int{"completed": 1, "panicked": 1}, repo: repo}
	p.runManualExportSafely(job)

	states, err := repo.GetObservationExports(context.Background(), []string{entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	byTarget := make(map[string]logstore.ObservationExport, len(states))
	for _, state := range states {
		byTarget[state.TargetID] = state
	}
	if got := byTarget["completed"].Status; got != logstore.ObservationExportStatusExported {
		t.Fatalf("completed target status = %q, want exported", got)
	}
	if got := byTarget["panicked"].Status; got != logstore.ObservationExportStatusFailed {
		t.Fatalf("panicked target status = %q, want failed", got)
	}
}

func TestAutomaticAndManualExportsShareLazyMediaConcurrencyLimit(t *testing.T) {
	uploader := &sharedConcurrencyUploader{}
	target := &otelTarget{id: "shared", serviceName: "test", client: &countingTestOtelClient{}, mediaUploader: uploader, exportTimeout: time.Second}
	plugin := &OtelPlugin{targets: []*otelTarget{target}, pluginSpanFilter: &PluginSpanFilter{}}

	makeTrace := func(id string) *schemas.Trace {
		trace := selectionTrace(id, schemas.ImageGenerationRequest, time.Second)
		trace.Spans[0].Attributes[schemas.AttrBifrostImageInput] = `{"prompt":"test"}`
		trace.Spans[0].Attributes[schemas.AttrBifrostImageOutput] = `{"images":[{"media_ref":"bifrost-media://` + id + `-media","capture_status":"captured"}],"image_count":1}`
		attachTestMedia(trace, schemas.TraceMedia{ID: id + "-media", SpanID: trace.Spans[0].SpanID, Field: "output", Role: "image", MIMEType: "image/png", Data: onePixelPNG})
		return trace
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		trace := makeTrace(fmt.Sprintf("shared-%d", i))
		wg.Add(1)
		go func(manual bool) {
			defer wg.Done()
			if manual {
				_, _, _ = plugin.emitManualTrace(context.Background(), target, trace)
				return
			}
			_ = plugin.Inject(context.Background(), trace)
		}(i%2 == 0)
	}
	wg.Wait()
	if target.mediaSem == nil {
		t.Fatal("lazy media runtime was not initialized")
	}
	if maximum := uploader.max.Load(); maximum > 4 {
		t.Fatalf("shared automatic/manual uploads reached concurrency %d, want <= 4", maximum)
	}
}

func TestEnqueueManualExportPersistsSuccess(t *testing.T) {
	entry := manualImageLog(schemas.ImageGenerationRequest)
	entry.ImageGenerationInputParsed = &schemas.ImageGenerationInput{Prompt: "test"}
	repo := &manualTestRepo{logs: map[string]*logstore.Log{entry.ID: entry}, states: make(map[string]logstore.ObservationExport)}
	ctx, cancel := context.WithCancel(context.Background())
	p := &OtelPlugin{
		ctx: ctx, cancel: cancel, manualRepo: repo, exportStore: repo,
		manualQueue: make(chan manualExportJob, 2),
		targets:     []*otelTarget{{id: "profile-0", serviceName: "test", client: &countingTestOtelClient{}, mediaUploader: &successfulManualUploader{}, exportTimeout: time.Second, mediaSem: make(chan struct{}, 1)}},
	}
	p.startManualExportWorkers(1)
	defer func() { cancel(); p.manualWG.Wait() }()
	status, _, err := p.EnqueueManualExport(context.Background(), entry.ID)
	if err != nil || status != logstore.ObservationExportStatusPending {
		t.Fatalf("enqueue = status %q err %v", status, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		states, _ := repo.GetObservationExports(context.Background(), []string{entry.ID})
		if len(states) == 1 && states[0].Status == logstore.ObservationExportStatusExported {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("manual export did not reach exported state")
}

func TestEnqueueManualExportQueueFullAndIdempotentStates(t *testing.T) {
	entry := manualImageLog(schemas.ImageGenerationRequest)
	entry.ImageGenerationInputParsed = &schemas.ImageGenerationInput{Prompt: "test"}
	target := &otelTarget{id: "profile-0", client: &countingTestOtelClient{}, mediaUploader: &successfulManualUploader{}, exportTimeout: time.Second}

	t.Run("queue full becomes terminal failed", func(t *testing.T) {
		repo := &manualTestRepo{logs: map[string]*logstore.Log{entry.ID: entry}, states: make(map[string]logstore.ObservationExport)}
		queue := make(chan manualExportJob, 1)
		queue <- manualExportJob{logID: "occupied"}
		plugin := &OtelPlugin{manualRepo: repo, exportStore: repo, manualQueue: queue, targets: []*otelTarget{target}}
		status, reason, err := plugin.EnqueueManualExport(context.Background(), entry.ID)
		if err != ErrManualExportQueueFull || status != logstore.ObservationExportStatusFailed || reason != "queue_full" {
			t.Fatalf("enqueue = %q/%q/%v", status, reason, err)
		}
		states, _ := repo.GetObservationExports(context.Background(), []string{entry.ID})
		if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusFailed || states[0].Reason != "queue_full" {
			t.Fatalf("queue-full state = %+v", states)
		}
	})

	t.Run("fresh pending is idempotent", func(t *testing.T) {
		repo := &manualTestRepo{logs: map[string]*logstore.Log{entry.ID: entry}, states: map[string]logstore.ObservationExport{
			entry.ID + "\x00profile-0": {LogID: entry.ID, TargetID: "profile-0", Status: logstore.ObservationExportStatusPending, Source: logstore.ObservationExportSourceManual, Attempts: 1, UpdatedAt: time.Now()},
		}}
		plugin := &OtelPlugin{manualRepo: repo, exportStore: repo, manualQueue: make(chan manualExportJob, 1), targets: []*otelTarget{target}}
		status, reason, err := plugin.EnqueueManualExport(context.Background(), entry.ID)
		if err != nil || status != logstore.ObservationExportStatusPending || reason != "already_pending" || len(plugin.manualQueue) != 0 {
			t.Fatalf("idempotent enqueue = %q/%q/%v queue=%d", status, reason, err, len(plugin.manualQueue))
		}
	})

	t.Run("stale pending retries", func(t *testing.T) {
		repo := &manualTestRepo{logs: map[string]*logstore.Log{entry.ID: entry}, states: map[string]logstore.ObservationExport{
			entry.ID + "\x00profile-0": {LogID: entry.ID, TargetID: "profile-0", Status: logstore.ObservationExportStatusPending, Source: logstore.ObservationExportSourceManual, Attempts: 2, UpdatedAt: time.Now().Add(-manualPendingStaleAfter - time.Second)},
		}}
		plugin := &OtelPlugin{manualRepo: repo, exportStore: repo, manualQueue: make(chan manualExportJob, 1), targets: []*otelTarget{target}}
		status, reason, err := plugin.EnqueueManualExport(context.Background(), entry.ID)
		if err != nil || status != logstore.ObservationExportStatusPending || reason != "queued" || len(plugin.manualQueue) != 1 {
			t.Fatalf("stale enqueue = %q/%q/%v queue=%d", status, reason, err, len(plugin.manualQueue))
		}
		job := <-plugin.manualQueue
		if job.attempts["profile-0"] != 3 {
			t.Fatalf("attempts = %d, want 3", job.attempts["profile-0"])
		}
	})

	t.Run("irrecoverable unavailable is stable", func(t *testing.T) {
		repo := &manualTestRepo{states: map[string]logstore.ObservationExport{
			entry.ID + "\x00profile-0": {LogID: entry.ID, TargetID: "profile-0", Status: logstore.ObservationExportStatusUnavailable, Source: logstore.ObservationExportSourceManual, Reason: "content_hidden", UpdatedAt: time.Now()},
		}}
		plugin := &OtelPlugin{manualRepo: repo, exportStore: repo, manualQueue: make(chan manualExportJob, 1), targets: []*otelTarget{target}}
		status, reason, err := plugin.EnqueueManualExport(context.Background(), entry.ID)
		if err != nil || status != logstore.ObservationExportStatusUnavailable || reason != "content_hidden" || len(plugin.manualQueue) != 0 {
			t.Fatalf("irrecoverable enqueue = %q/%q/%v", status, reason, err)
		}
	})
}

func TestManualTraceStripsSignedImageURLQuery(t *testing.T) {
	entry := manualImageLog(schemas.ImageGenerationRequest)
	entry.ImageGenerationInputParsed = &schemas.ImageGenerationInput{Prompt: "test"}
	entry.ImageGenerationOutputParsed.Data[0].URL = "https://images.example.test/output.png?X-Amz-Signature=secret#fragment"
	trace, err := manualTraceFromLog(entry)
	if err != nil {
		t.Fatal(err)
	}
	output := getStringAttr(finalSelectionSpan(trace).Attributes, schemas.AttrBifrostImageOutput)
	if strings.Contains(output, "secret") || strings.Contains(output, "X-Amz") || strings.Contains(output, "fragment") {
		t.Fatalf("manual output leaked signed URL: %s", output)
	}
}

func TestAutomaticInjectPersistsSelectionAndExportStatuses(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport)}
	target := &otelTarget{id: "profile-0", serviceName: "test", client: &countingTestOtelClient{}, mediaUploader: &successfulManualUploader{}, exportTimeout: time.Second, mediaSem: make(chan struct{}, 1)}
	dropSelector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{ID: "drop", ExportRate: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	p := &OtelPlugin{targets: []*otelTarget{target}, selector: dropSelector, exportStore: repo, pluginSpanFilter: &PluginSpanFilter{}}
	dropped := selectionTrace("automatic-drop", schemas.ImageGenerationRequest, time.Second)
	dropped.RequestID = "automatic-drop"
	if err := p.Inject(context.Background(), dropped); err != nil {
		t.Fatal(err)
	}
	states, _ := repo.GetObservationExports(context.Background(), []string{dropped.RequestID})
	if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusNotExported || states[0].Reason != "sampled_out" {
		t.Fatalf("dropped export state = %+v", states)
	}

	p.selector = nil
	exported := selectionTrace("automatic-export", schemas.ImageGenerationRequest, time.Second)
	exported.RequestID = "automatic-export"
	if err := p.Inject(context.Background(), exported); err != nil {
		t.Fatal(err)
	}
	states, _ = repo.GetObservationExports(context.Background(), []string{exported.RequestID})
	if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusExported {
		t.Fatalf("successful export state = %+v", states)
	}
}

func TestExportStatusWriterBatchesLatestState(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport)}
	ctx, cancel := context.WithCancel(context.Background())
	p := &OtelPlugin{ctx: ctx, cancel: cancel, exportStore: repo, statusQueue: make(chan logstore.ObservationExport, 8)}
	target := &otelTarget{id: "profile-0", mediaUploader: &successfulManualUploader{}}
	trace := selectionTrace("status-batch", schemas.ImageGenerationRequest, time.Second)
	trace.RequestID = "status-batch"
	p.startExportStatusWriter()
	p.persistExportState(context.Background(), trace, target, logstore.ObservationExportStatusNotExported, logstore.ObservationExportSourceAutomatic, "sampled_out", "rule")
	p.persistExportState(context.Background(), trace, target, logstore.ObservationExportStatusExported, logstore.ObservationExportSourceAutomatic, "selected", "rule")
	time.Sleep(600 * time.Millisecond)
	cancel()
	p.statusWG.Wait()
	states, _ := repo.GetObservationExports(context.Background(), []string{trace.RequestID})
	if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusExported {
		t.Fatalf("batched status = %+v", states)
	}
}

func TestPersistExportStateQueueFullFallsBackSynchronously(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport)}
	queue := make(chan logstore.ObservationExport, 1)
	queue <- logstore.ObservationExport{LogID: "occupied", TargetID: "occupied"}
	plugin := &OtelPlugin{exportStore: repo, statusQueue: queue}
	target := &otelTarget{id: "profile-0", mediaUploader: &successfulManualUploader{}}
	trace := selectionTrace("sync-fallback", schemas.ImageGenerationRequest, time.Second)
	trace.RequestID = "sync-fallback"

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	plugin.persistExportState(cancelled, trace, target, logstore.ObservationExportStatusExported, logstore.ObservationExportSourceAutomatic, "selected", "rule")
	states, _ := repo.GetObservationExports(context.Background(), []string{trace.RequestID})
	if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusExported {
		t.Fatalf("synchronous fallback state = %+v", states)
	}
	if plugin.statusFallbackWrites.Load() != 1 {
		t.Fatalf("fallback writes = %d, want 1", plugin.statusFallbackWrites.Load())
	}
}

func TestExportStatusWriterRetainsLatestAfterTransientBatchFailure(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport), batchFailures: 1}
	ctx, cancel := context.WithCancel(context.Background())
	plugin := &OtelPlugin{ctx: ctx, cancel: cancel, exportStore: repo, statusQueue: make(chan logstore.ObservationExport, 8)}
	target := &otelTarget{id: "profile-0", mediaUploader: &successfulManualUploader{}}
	trace := selectionTrace("retry-batch", schemas.ImageGenerationRequest, time.Second)
	trace.RequestID = "retry-batch"
	plugin.startExportStatusWriter()
	plugin.persistExportState(context.Background(), trace, target, logstore.ObservationExportStatusNotExported, logstore.ObservationExportSourceAutomatic, "sampled_out", "rule")
	plugin.persistExportState(context.Background(), trace, target, logstore.ObservationExportStatusExported, logstore.ObservationExportSourceAutomatic, "selected", "rule")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		states, _ := repo.GetObservationExports(context.Background(), []string{trace.RequestID})
		if len(states) == 1 && states[0].Status == logstore.ObservationExportStatusExported {
			cancel()
			plugin.statusWG.Wait()
			if repo.batchCalls < 2 {
				t.Fatalf("batch calls = %d, want retry", repo.batchCalls)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	plugin.statusWG.Wait()
	t.Fatal("retained latest status was not persisted after retry")
}

func TestExportStatusWriterShutdownDrainsPendingState(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport)}
	ctx, cancel := context.WithCancel(context.Background())
	plugin := &OtelPlugin{ctx: ctx, cancel: cancel, exportStore: repo, statusQueue: make(chan logstore.ObservationExport, 8)}
	target := &otelTarget{id: "profile-0", mediaUploader: &successfulManualUploader{}}
	trace := selectionTrace("shutdown-drain", schemas.ImageGenerationRequest, time.Second)
	trace.RequestID = "shutdown-drain"
	plugin.startExportStatusWriter()
	plugin.persistExportState(context.Background(), trace, target, logstore.ObservationExportStatusExported, logstore.ObservationExportSourceAutomatic, "selected", "rule")
	cancel()
	plugin.statusWG.Wait()
	states, _ := repo.GetObservationExports(context.Background(), []string{trace.RequestID})
	if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusExported {
		t.Fatalf("shutdown-drained state = %+v", states)
	}
}

func TestExportStatusWriterBackpressuresDuringPersistentFailure(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport), batchFailures: 100}
	ctx, cancel := context.WithCancel(context.Background())
	plugin := &OtelPlugin{ctx: ctx, cancel: cancel, exportStore: repo, statusQueue: make(chan logstore.ObservationExport, 2)}
	target := &otelTarget{id: "profile-0", mediaUploader: &successfulManualUploader{}}
	plugin.startExportStatusWriter()
	for i := 0; i < 2; i++ {
		trace := selectionTrace(fmt.Sprintf("seed-%d", i), schemas.ImageGenerationRequest, time.Second)
		trace.RequestID = fmt.Sprintf("seed-%d", i)
		plugin.persistExportState(context.Background(), trace, target, logstore.ObservationExportStatusPending, logstore.ObservationExportSourceAutomatic, "seed", "rule")
	}
	// First ticker fails and leaves two unique states in the bounded pending map.
	time.Sleep(600 * time.Millisecond)
	for i := 0; i < 4; i++ {
		trace := selectionTrace(fmt.Sprintf("overflow-%d", i), schemas.ImageGenerationRequest, time.Second)
		trace.RequestID = fmt.Sprintf("overflow-%d", i)
		plugin.persistExportState(context.Background(), trace, target, logstore.ObservationExportStatusFailed, logstore.ObservationExportSourceAutomatic, "overflow", "rule")
	}
	if plugin.statusFallbackWrites.Load() == 0 {
		t.Fatal("persistent batch failure did not backpressure producers into synchronous fallback")
	}
	if maximum := plugin.statusMaxPending.Load(); maximum > int64(cap(plugin.statusQueue)) {
		t.Fatalf("pending map reached %d entries, queue capacity is %d", maximum, cap(plugin.statusQueue))
	}
	states, _ := repo.GetObservationExports(context.Background(), []string{"overflow-3"})
	if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusFailed {
		t.Fatalf("synchronous overflow state = %+v", states)
	}
	started := time.Now()
	cancel()
	plugin.statusWG.Wait()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("shutdown retry exceeded bound: %s", elapsed)
	}
}
