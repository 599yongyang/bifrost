package otel

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
)

const (
	manualMediaMaxAttachments = 16
	manualMediaMaxBytes       = 32 << 20
	manualImageMaxBytes       = 20 << 20
	manualPendingStaleAfter   = 2 * time.Minute
)

var (
	ErrManualExportUnavailable = errors.New("manual Langfuse export is unavailable")
	ErrManualExportQueueFull   = errors.New("manual Langfuse export queue is full")
)

var irrecoverableManualExportReasons = map[string]struct{}{
	"content_hidden": {}, "missing_input": {}, "missing_input_media": {}, "missing_output_media": {},
	"media_too_large": {}, "unsupported_mime": {}, "request_type_unsupported": {},
	"invalid_url": {},
}

type ManualExportRepository interface {
	logstore.ObservationExportStore
	GetLog(ctx context.Context, id string) (*logstore.Log, error)
}

type manualExportJob struct {
	logID     string
	attempts  map[string]int
	repo      ManualExportRepository
	completed map[string]struct{}
}

type manualMediaStore struct {
	mu    sync.RWMutex
	items []schemas.TraceMedia
	bytes int
}

func (s *manualMediaStore) Store(_ string, media schemas.TraceMedia) bool {
	if len(media.Data) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= manualMediaMaxAttachments || s.bytes+len(media.Data) > manualMediaMaxBytes {
		return false
	}
	media.Data = append([]byte(nil), media.Data...)
	s.items = append(s.items, media)
	s.bytes += len(media.Data)
	return true
}

func (s *manualMediaStore) List(_ string) []schemas.TraceMedia {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]schemas.TraceMedia(nil), s.items...)
}

func (s *manualMediaStore) Delete(_ string) {
	s.mu.Lock()
	s.items = nil
	s.bytes = 0
	s.mu.Unlock()
}

func (p *OtelPlugin) manualTargets() []*otelTarget {
	if p == nil {
		return nil
	}
	targets := make([]*otelTarget, 0, len(p.targets))
	for _, target := range p.targets {
		if target != nil && target.client != nil && target.mediaUploader != nil && !target.disableContentLogging {
			targets = append(targets, target)
		}
	}
	return targets
}

func (p *OtelPlugin) EnqueueManualExport(ctx context.Context, logID string) (string, string, error) {
	if p == nil || strings.TrimSpace(logID) == "" {
		return logstore.ObservationExportStatusUnavailable, "manual_export_unavailable", ErrManualExportUnavailable
	}
	_, repo := p.observationExportStores()
	if repo == nil {
		return logstore.ObservationExportStatusUnavailable, "manual_export_unavailable", ErrManualExportUnavailable
	}
	targets := p.manualTargets()
	if len(targets) == 0 {
		return logstore.ObservationExportStatusUnavailable, "target_unavailable", ErrManualExportUnavailable
	}
	states, err := repo.GetObservationExports(ctx, []string{logID})
	if err != nil {
		return logstore.ObservationExportStatusFailed, "status_read_failed", err
	}
	byTarget := make(map[string]logstore.ObservationExport, len(states))
	for _, state := range states {
		byTarget[state.TargetID] = state
	}
	allExported := true
	attempts := make(map[string]int, len(targets))
	for _, target := range targets {
		state, exists := byTarget[target.id]
		if exists && state.Source == logstore.ObservationExportSourceManual && state.Status == logstore.ObservationExportStatusUnavailable {
			if _, irrecoverable := irrecoverableManualExportReasons[state.Reason]; irrecoverable {
				return state.Status, state.Reason, nil
			}
		}
		if exists && state.Status == logstore.ObservationExportStatusPending && time.Since(state.UpdatedAt) < manualPendingStaleAfter {
			return logstore.ObservationExportStatusPending, "already_pending", nil
		}
		if !exists || state.Status != logstore.ObservationExportStatusExported {
			allExported = false
		}
		attempts[target.id] = state.Attempts + 1
	}
	if allExported {
		return logstore.ObservationExportStatusExported, "already_exported", nil
	}
	for _, target := range targets {
		p.upsertManualState(ctx, repo, logID, target, logstore.ObservationExportStatusPending, "queued", "", attempts[target.id])
	}
	job := manualExportJob{logID: logID, attempts: attempts, repo: repo}
	select {
	case p.manualQueue <- job:
		return logstore.ObservationExportStatusPending, "queued", nil
	default:
		for _, target := range targets {
			p.upsertManualState(ctx, repo, logID, target, logstore.ObservationExportStatusFailed, "queue_full", "", attempts[target.id])
		}
		return logstore.ObservationExportStatusFailed, "queue_full", ErrManualExportQueueFull
	}
}

func (p *OtelPlugin) startManualExportWorkers(count int) {
	for range count {
		p.manualWG.Add(1)
		go func() {
			defer p.manualWG.Done()
			for {
				select {
				case <-p.ctx.Done():
					return
				case job := <-p.manualQueue:
					p.runManualExportSafely(job)
				}
			}
		}()
	}
}

func (p *OtelPlugin) runManualExportSafely(job manualExportJob) {
	if job.completed == nil {
		job.completed = make(map[string]struct{}, len(job.attempts))
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if logger != nil {
				logger.Error("recovered manual OTEL export panic: log_id=%s panic_type=%T\n%s", job.logID, recovered, debug.Stack())
			}
			// The job was persisted as pending before it entered the worker. Make a
			// best-effort terminal update so a contained panic cannot leave the UI
			// showing an export that will never finish.
			func() {
				defer func() {
					if stateRecovered := recover(); stateRecovered != nil && logger != nil {
						logger.Error("recovered manual OTEL panic-state update panic: log_id=%s panic_type=%T\n%s", job.logID, stateRecovered, debug.Stack())
					}
				}()
				if p == nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				p.failPendingManualTargets(ctx, job, logstore.ObservationExportStatusFailed, "worker_panic", errors.New("manual export worker failed unexpectedly"))
			}()
		}
	}()
	p.runManualExport(job)
}

func (p *OtelPlugin) runManualExport(job manualExportJob) {
	ctx, cancel := context.WithTimeout(p.ctx, time.Minute)
	defer cancel()
	entry, err := job.repo.GetLog(ctx, job.logID)
	if err != nil {
		p.failManualTargets(ctx, job, logstore.ObservationExportStatusFailed, "log_read_failed", err)
		return
	}
	trace, err := manualTraceFromLog(entry)
	if err != nil {
		p.failManualTargets(ctx, job, logstore.ObservationExportStatusUnavailable, manualFailureReason(err), err)
		return
	}
	for _, target := range p.manualTargets() {
		status, reason, exportErr := p.emitManualTrace(ctx, target, trace)
		p.upsertManualState(ctx, job.repo, job.logID, target, status, reason, trace.TraceID, job.attempts[target.id])
		job.completed[target.id] = struct{}{}
		if exportErr != nil && logger != nil {
			logger.Error("manual Langfuse export failed log_id=%s trace_id=%s target_id=%s reason=%s: %v", job.logID, trace.TraceID, target.id, reason, exportErr)
		}
	}
}

func (p *OtelPlugin) failPendingManualTargets(ctx context.Context, job manualExportJob, status, reason string, err error) {
	for _, target := range p.manualTargets() {
		if _, completed := job.completed[target.id]; completed {
			continue
		}
		p.upsertManualState(ctx, job.repo, job.logID, target, status, reason, "", job.attempts[target.id])
	}
	if err != nil && logger != nil {
		logger.Error("manual Langfuse export worker failed log_id=%s reason=%s: %v", job.logID, reason, err)
	}
}

func (p *OtelPlugin) failManualTargets(ctx context.Context, job manualExportJob, status, reason string, err error) {
	for _, target := range p.manualTargets() {
		p.upsertManualState(ctx, job.repo, job.logID, target, status, reason, "", job.attempts[target.id])
	}
	if err != nil && logger != nil {
		logger.Error("manual Langfuse export preparation failed log_id=%s reason=%s: %v", job.logID, reason, err)
	}
}

func (p *OtelPlugin) upsertManualState(ctx context.Context, repo ManualExportRepository, logID string, target *otelTarget, status, reason, traceID string, attempts int) {
	if repo == nil || target == nil {
		return
	}
	now := time.Now().UTC()
	state := &logstore.ObservationExport{
		LogID: logID, TargetID: target.id, Status: status, Source: logstore.ObservationExportSourceManual,
		Reason: reason, ExternalTraceID: traceID, Attempts: attempts, UpdatedAt: now,
	}
	if status == logstore.ObservationExportStatusExported {
		state.ExportedAt = &now
	}
	if err := repo.UpsertObservationExport(ctx, state); err != nil && logger != nil {
		logger.Warn("failed to persist manual observability export status log_id=%s target_id=%s: %v", logID, target.id, err)
	}
}

type manualTraceError struct{ reason string }

func (e *manualTraceError) Error() string { return e.reason }

func manualFailureReason(err error) string {
	var traceErr *manualTraceError
	if errors.As(err, &traceErr) {
		return traceErr.reason
	}
	return "trace_rebuild_failed"
}

func manualTraceFromLog(entry *logstore.Log) (*schemas.Trace, error) {
	if entry == nil || entry.ID == "" {
		return nil, &manualTraceError{reason: "log_not_found"}
	}
	if entry.ContentHidden {
		return nil, &manualTraceError{reason: "content_hidden"}
	}
	if entry.Status == "processing" {
		return nil, &manualTraceError{reason: "log_not_finalized"}
	}
	requestType := schemas.RequestType(entry.Object)
	switch requestType {
	case schemas.ImageGenerationRequest, schemas.ImageGenerationStreamRequest,
		schemas.ImageEditRequest, schemas.ImageEditStreamRequest, schemas.ImageVariationRequest:
	default:
		return nil, &manualTraceError{reason: "request_type_unsupported"}
	}

	digest := sha256.Sum256([]byte("manual-observation\x00" + entry.ID))
	traceID := hex.EncodeToString(digest[:16])
	rootID := hex.EncodeToString(digest[16:24])
	spanID := hex.EncodeToString(digest[24:32])
	start := entry.Timestamp
	if start.IsZero() {
		start = time.Now().UTC()
	}
	end := start
	if entry.Latency != nil && *entry.Latency > 0 {
		end = start.Add(time.Duration(*entry.Latency * float64(time.Millisecond)))
	}
	status := schemas.SpanStatusOk
	if entry.Status == "error" {
		status = schemas.SpanStatusError
	}
	attrs := map[string]any{
		schemas.AttrLegacyRequestType:    entry.Object,
		schemas.AttrRequestModel:         entry.Model,
		schemas.AttrResponseModel:        entry.Model,
		schemas.AttrBifrostProviderName:  entry.Provider,
		schemas.AttrBifrostRetries:       entry.NumberOfRetries,
		schemas.AttrBifrostFallbackIndex: entry.FallbackIndex,
		schemas.AttrBifrostRequestID:     entry.ID,
	}
	if entry.Alias != nil && *entry.Alias != "" {
		attrs[schemas.AttrBifrostAlias] = *entry.Alias
	}
	if entry.RoutingRuleID != nil {
		attrs[schemas.AttrBifrostRoutingRuleID] = *entry.RoutingRuleID
	}
	if entry.RoutingRuleName != nil {
		attrs[schemas.AttrBifrostRoutingRuleName] = *entry.RoutingRuleName
	}
	if entry.Cost != nil {
		attrs[schemas.AttrUsageCost] = *entry.Cost
	}
	if usage := entry.TokenUsageParsed; usage != nil {
		attrs[schemas.AttrInputTokens] = usage.PromptTokens
		attrs[schemas.AttrOutputTokens] = usage.CompletionTokens
		attrs[schemas.AttrTotalTokens] = usage.TotalTokens
	}
	if entry.ErrorDetailsParsed != nil && entry.ErrorDetailsParsed.Error != nil {
		attrs[schemas.AttrError] = entry.ErrorDetailsParsed.Error.Message
		if entry.ErrorDetailsParsed.Error.Type != nil {
			attrs[schemas.AttrErrorTypeSpec] = *entry.ErrorDetailsParsed.Error.Type
		}
		if entry.ErrorDetailsParsed.StatusCode != nil {
			attrs[schemas.AttrHTTPResponseStatusCode] = *entry.ErrorDetailsParsed.StatusCode
		}
	}

	root := &schemas.Span{SpanID: rootID, TraceID: traceID, Name: entry.Object, Kind: schemas.SpanKindInternal, StartTime: start, EndTime: end, Status: status, Attributes: map[string]any{schemas.AttrBifrostRequestID: entry.ID}}
	span := &schemas.Span{SpanID: spanID, ParentID: rootID, TraceID: traceID, Name: "generate_content " + entry.Model, Kind: schemas.SpanKindLLMCall, StartTime: start, EndTime: end, Status: status, Attributes: attrs}
	trace := &schemas.Trace{RequestID: entry.ID, InternalID: "manual-" + entry.ID, TraceID: traceID, RootSpan: root, Spans: []*schemas.Span{root, span}, StartTime: start, EndTime: end, Attributes: map[string]any{}}
	trace.SetMediaStore(&manualMediaStore{}, trace.InternalID)
	if err := populateManualImageAttributes(trace, span, entry, requestType); err != nil {
		return nil, err
	}
	if !traceMediaSummariesComplete(trace) {
		return nil, &manualTraceError{reason: "incomplete_media"}
	}
	return trace, nil
}

func populateManualImageAttributes(trace *schemas.Trace, span *schemas.Span, entry *logstore.Log, requestType schemas.RequestType) error {
	input := map[string]any{"request_type": string(requestType)}
	switch requestType {
	case schemas.ImageGenerationRequest, schemas.ImageGenerationStreamRequest:
		if entry.ImageGenerationInputParsed == nil {
			return &manualTraceError{reason: "missing_input"}
		}
		input["prompt"] = entry.ImageGenerationInputParsed.Prompt
		var typed schemas.ImageGenerationParameters
		if entry.Params != "" && sonic.Unmarshal([]byte(entry.Params), &typed) == nil {
			copyManualImageParams(input, typed.Size, typed.Quality, typed.N)
		}
	case schemas.ImageEditRequest, schemas.ImageEditStreamRequest:
		if entry.ImageEditInputParsed == nil || len(entry.ImageEditInputParsed.Images) == 0 {
			return &manualTraceError{reason: "missing_input_media"}
		}
		input["prompt"] = entry.ImageEditInputParsed.Prompt
		images := make([]map[string]any, 0, len(entry.ImageEditInputParsed.Images))
		for index, image := range entry.ImageEditInputParsed.Images {
			item, err := addManualMedia(trace, span, "input", "image", index, image.Image)
			if err != nil {
				return err
			}
			images = append(images, item)
		}
		input["images"] = images
		var typed schemas.ImageEditParameters
		if entry.Params != "" && sonic.Unmarshal([]byte(entry.Params), &typed) == nil {
			copyManualImageParams(input, typed.Size, typed.Quality, typed.N)
			if len(typed.Mask) > 0 {
				mask, err := addManualMedia(trace, span, "input", "mask", 0, typed.Mask)
				if err != nil {
					return err
				}
				input["mask"] = mask
			}
		}
	case schemas.ImageVariationRequest:
		if entry.ImageVariationInputParsed == nil || len(entry.ImageVariationInputParsed.Image.Image) == 0 {
			return &manualTraceError{reason: "missing_input_media"}
		}
		image, err := addManualMedia(trace, span, "input", "image", 0, entry.ImageVariationInputParsed.Image.Image)
		if err != nil {
			return err
		}
		input["images"] = []map[string]any{image}
		var typed schemas.ImageVariationParameters
		if entry.Params != "" && sonic.Unmarshal([]byte(entry.Params), &typed) == nil {
			copyManualImageParams(input, typed.Size, nil, typed.N)
		}
	}
	inputJSON, err := sonic.Marshal(input)
	if err != nil {
		return err
	}
	span.Attributes[schemas.AttrBifrostImageInput] = string(inputJSON)

	if entry.Status == "error" {
		return nil
	}
	response := entry.ImageGenerationOutputParsed
	if response == nil || len(response.Data) == 0 {
		return &manualTraceError{reason: "missing_output_media"}
	}
	images := make([]map[string]any, 0, len(response.Data))
	for index, image := range response.Data {
		item := map[string]any{"index": index}
		if image.RevisedPrompt != "" {
			item["revised_prompt"] = image.RevisedPrompt
		}
		if image.URL != "" {
			safeURL := safeManualImageURL(image.URL)
			if safeURL == "" {
				return &manualTraceError{reason: "invalid_url"}
			}
			item["url"] = safeURL
		} else if image.B64JSON != "" {
			decoded, _, err := decodeManualImage(image.B64JSON)
			if err != nil {
				return err
			}
			media, err := addManualMedia(trace, span, "output", "image", index, decoded)
			if err != nil {
				return err
			}
			for key, value := range media {
				item[key] = value
			}
		} else {
			return &manualTraceError{reason: "missing_output_media"}
		}
		images = append(images, item)
	}
	outputJSON, err := sonic.Marshal(map[string]any{"images": images, "image_count": len(images)})
	if err != nil {
		return err
	}
	span.Attributes[schemas.AttrBifrostImageOutput] = string(outputJSON)
	return nil
}

func safeManualImageURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func copyManualImageParams(input map[string]any, size, quality *string, n *int) {
	if size != nil {
		input["size"] = *size
	}
	if quality != nil {
		input["quality"] = *quality
	}
	if n != nil {
		input["n"] = *n
	}
}

func decodeManualImage(value string) ([]byte, string, error) {
	encoded := strings.TrimSpace(value)
	mimeHint := ""
	if strings.HasPrefix(encoded, "data:") {
		comma := strings.IndexByte(encoded, ',')
		if comma < 0 || !strings.Contains(encoded[:comma], ";base64") {
			return nil, "", &manualTraceError{reason: "invalid_base64"}
		}
		mimeHint = strings.TrimPrefix(strings.Split(encoded[:comma], ";")[0], "data:")
		encoded = encoded[comma+1:]
	}
	if base64.StdEncoding.DecodedLen(len(encoded)) > manualImageMaxBytes {
		return nil, "", &manualTraceError{reason: "media_too_large"}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return nil, "", &manualTraceError{reason: "invalid_base64"}
	}
	return data, mimeHint, nil
}

func addManualMedia(trace *schemas.Trace, span *schemas.Span, field, role string, index int, data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return nil, &manualTraceError{reason: "missing_input_media"}
	}
	if len(data) > manualImageMaxBytes {
		return nil, &manualTraceError{reason: "media_too_large"}
	}
	mimeType := strings.Split(http.DetectContentType(data), ";")[0]
	if !manualImageMIMESupported(mimeType) {
		return nil, &manualTraceError{reason: "unsupported_mime"}
	}
	digest := sha256.Sum256(data)
	sha := hex.EncodeToString(digest[:])
	idDigest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", trace.RequestID, field, role, index, sha)))
	id := hex.EncodeToString(idDigest[:12])
	media := schemas.TraceMedia{ID: id, SpanID: span.SpanID, Field: field, Role: role, Index: index, MIMEType: mimeType, Bytes: len(data), SHA256: sha, Data: data}
	if !trace.AddMedia(media) {
		return nil, &manualTraceError{reason: "media_limit"}
	}
	return map[string]any{"media_ref": "bifrost-media://" + id, "mime_type": mimeType, "bytes": len(data), "sha256": sha, "index": index}, nil
}

func manualImageMIMESupported(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func (p *OtelPlugin) emitManualTrace(ctx context.Context, target *otelTarget, trace *schemas.Trace) (string, string, error) {
	if target == nil || target.client == nil || target.mediaUploader == nil || target.disableContentLogging {
		return logstore.ObservationExportStatusUnavailable, "target_unavailable", ErrManualExportUnavailable
	}
	if target.breakerOpen() {
		return logstore.ObservationExportStatusFailed, "circuit_open", ErrManualExportUnavailable
	}
	target.ensureMediaRuntime()
	attachments := trace.MediaAttachments()
	mediaRefs := make(map[string]string, len(attachments))
	for _, media := range attachments {
		mediaRefs["bifrost-media://"+media.ID] = ""
	}
	if len(attachments) > 0 {
		if target.mediaBreakerOpen() {
			return logstore.ObservationExportStatusFailed, "media_circuit_open", ErrManualExportUnavailable
		}
		mediaCtx, cancel := context.WithTimeout(ctx, target.exportTimeout)
		defer cancel()
		for _, media := range attachments {
			select {
			case target.mediaSem <- struct{}{}:
			case <-mediaCtx.Done():
				return logstore.ObservationExportStatusFailed, "media_upload_timeout", mediaCtx.Err()
			}
			token, err := func() (string, error) {
				defer func() { <-target.mediaSem }()
				return target.mediaUploader.Upload(mediaCtx, trace.TraceID, media)
			}()
			if err != nil {
				target.tripMediaBreaker()
				return logstore.ObservationExportStatusFailed, mediaUploadFailureReason(err), err
			}
			mediaRefs["bifrost-media://"+media.ID] = token
		}
		target.resetMediaBreaker()
	}
	resourceSpan := p.convertTraceToResourceSpanWithMedia(target.serviceName, trace, target.requestHeaders, false, target.groupTracesBySession, target.disableRootSpanContent, mediaRefs)
	emitCtx, cancel := context.WithTimeout(ctx, target.exportTimeout)
	defer cancel()
	if err := target.client.Emit(emitCtx, []*ResourceSpan{resourceSpan}); err != nil {
		target.tripBreaker()
		return logstore.ObservationExportStatusFailed, "trace_emit_failed", err
	}
	target.resetBreaker()
	return logstore.ObservationExportStatusExported, "manual", nil
}
