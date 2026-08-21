package otel

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")

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
	span := finalImageSpan(trace)
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
	mu     sync.Mutex
	logs   map[string]*logstore.Log
	states map[string]logstore.ObservationExport
}

func (r *manualTestRepo) GetLog(_ context.Context, id string) (*logstore.Log, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.logs[id]
	if entry == nil {
		return nil, logstore.ErrNotFound
	}
	return entry, nil
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

func TestAutomaticInjectPersistsSelectionAndExportStatuses(t *testing.T) {
	repo := &manualTestRepo{states: make(map[string]logstore.ObservationExport)}
	target := &otelTarget{id: "profile-0", serviceName: "test", client: &countingTestOtelClient{}, mediaUploader: &successfulManualUploader{}, exportTimeout: time.Second, mediaSem: make(chan struct{}, 1)}
	dropSelector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{ID: "drop", ExportRate: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	p := &OtelPlugin{targets: []*otelTarget{target}, selector: dropSelector, exportStore: repo, pluginSpanFilter: &PluginSpanFilter{}}
	dropped := imageSelectionTrace("automatic-drop", time.Second)
	dropped.RequestID = "automatic-drop"
	if err := p.Inject(context.Background(), dropped); err != nil {
		t.Fatal(err)
	}
	states, _ := repo.GetObservationExports(context.Background(), []string{dropped.RequestID})
	if len(states) != 1 || states[0].Status != logstore.ObservationExportStatusNotExported || states[0].Reason != "sampled_out" {
		t.Fatalf("dropped export state = %+v", states)
	}

	p.selector = nil
	exported := imageSelectionTrace("automatic-export", time.Second)
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
	trace := imageSelectionTrace("status-batch", time.Second)
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
