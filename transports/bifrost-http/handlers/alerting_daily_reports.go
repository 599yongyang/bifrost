package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/sidekiq"
	alertengine "github.com/maximhq/bifrost/transports/bifrost-http/alerting"
	"github.com/valyala/fasthttp"
)

type dailyReportSettingsRequest struct {
	Enabled            *bool    `json:"enabled,omitempty"`
	Timezone           string   `json:"timezone,omitempty"`
	GenerateTime       string   `json:"generate_time,omitempty"`
	SendTime           string   `json:"send_time,omitempty"`
	SlowThresholdMs    *int64   `json:"slow_threshold_ms,omitempty"`
	InternalEnabled    *bool    `json:"internal_enabled,omitempty"`
	InternalChannelIDs []string `json:"internal_channel_ids,omitempty"`
	ExternalEnabled    *bool    `json:"external_enabled,omitempty"`
	ExternalChannelIDs []string `json:"external_channel_ids,omitempty"`
}

type dailyReportJobStatus struct {
	ID        string     `json:"id,omitempty"`
	Status    string     `json:"status"`
	Stage     string     `json:"stage,omitempty"`
	Processed int64      `json:"processed,omitempty"`
	Percent   int        `json:"percent,omitempty"`
	RunID     string     `json:"run_id,omitempty"`
	Deliver   bool       `json:"deliver,omitempty"`
	Message   string     `json:"message,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type DailyReportJobStore interface {
	SidekiqJobStore
	TryAcquireLock(context.Context, *tables.TableDistributedLock) (bool, error)
	GetLock(context.Context, string) (*tables.TableDistributedLock, error)
	CleanupExpiredLockByKey(context.Context, string) (bool, error)
	ReleaseLock(context.Context, string, string) (bool, error)
}

func (h *AlertingHandler) SetDailyReportJobBackend(runner *sidekiq.Runner, store DailyReportJobStore) {
	h.dailyReportRunner = runner
	h.dailyReportJobStore = store
	runner.Register(alertengine.DailyReportJobKind, func(ctx context.Context, job tables.TableSidekiqJob, progress sidekiq.ProgressFunc) (string, error) {
		return h.manager.RunDailyReportJob(ctx, job.Metadata, progress)
	})
	h.manager.SetDailyReportJobEnqueuer(h.enqueueScheduledDailyReportJob)
}

func (h *AlertingHandler) enqueueScheduledDailyReportJob(ctx context.Context, businessDate string, deliver bool, settings *tables.TableDailyReportSettings) error {
	metadata, jobID, err := alertengine.NewDailyReportJobMetadata(businessDate, deliver, "scheduled", settings)
	if err != nil {
		return err
	}
	release, err := h.acquireDailyReportEnqueueLock(ctx)
	if err != nil || release == nil {
		return err
	}
	defer release()
	if existing, findErr := h.dailyReportJobStore.GetInFlightSidekiqJobByKind(ctx, alertengine.DailyReportJobKind); findErr != nil {
		return findErr
	} else if existing != nil {
		return nil
	}
	if existing, findErr := h.dailyReportJobStore.GetSidekiqJob(ctx, jobID); findErr != nil {
		return findErr
	} else if existing != nil {
		if existing.Status != tables.SidekiqStatusFailed {
			return nil
		}
		jobID += "-" + uuid.NewString()
	}
	return h.dailyReportRunner.Enqueue(ctx, jobID, alertengine.DailyReportJobKind, metadata, "scheduled")
}

func (h *AlertingHandler) startDailyReportJob(ctx *fasthttp.RequestCtx) {
	requestCtx := context.WithoutCancel(ctx)
	if h.dailyReportRunner == nil || h.dailyReportJobStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Background report runner is not available")
		return
	}
	var request struct {
		BusinessDate string                      `json:"business_date"`
		Deliver      bool                        `json:"deliver"`
		Settings     *dailyReportSettingsRequest `json:"settings,omitempty"`
	}
	if len(ctx.PostBody()) > 0 {
		if err := sonic.Unmarshal(ctx.PostBody(), &request); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
			return
		}
	}
	settings, err := h.manager.GetDailyReportSettings(requestCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load report settings")
		return
	}
	if request.Settings != nil {
		applyDailyReportSettingsRequest(settings, *request.Settings)
	}
	metadata, jobID, err := alertengine.NewDailyReportJobMetadata(request.BusinessDate, request.Deliver, "manual", settings)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	var requestedMeta alertengine.DailyReportJobMeta
	if err := sonic.Unmarshal([]byte(metadata), &requestedMeta); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to prepare report job")
		return
	}
	releaseEnqueueLock, err := h.acquireDailyReportEnqueueLock(requestCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to coordinate report job")
		return
	}
	if releaseEnqueueLock == nil {
		SendError(ctx, fasthttp.StatusConflict, "Another report request is being started; retry shortly")
		return
	}
	defer releaseEnqueueLock()
	if existing, err := h.dailyReportJobStore.GetInFlightSidekiqJobByKind(requestCtx, alertengine.DailyReportJobKind); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to check running report jobs")
		return
	} else if existing != nil {
		var existingMeta alertengine.DailyReportJobMeta
		if err := sonic.Unmarshal([]byte(existing.Metadata), &existingMeta); err == nil && existingMeta.Fingerprint == requestedMeta.Fingerprint {
			SendJSON(ctx, dailyReportJobStatusFromRow(existing))
			return
		}
		SendError(ctx, fasthttp.StatusConflict, "A different daily report task is already running")
		return
	}
	if existing, err := h.dailyReportJobStore.GetSidekiqJob(requestCtx, jobID); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to check existing report job")
		return
	} else if existing != nil {
		if existing.Status != tables.SidekiqStatusFailed {
			SendJSON(ctx, dailyReportJobStatusFromRow(existing))
			return
		}
		jobID += "-" + uuid.NewString()
	}
	createdBy, _ := ctx.UserValue(schemas.BifrostContextKeyUserID).(string)
	if err := h.dailyReportRunner.Enqueue(requestCtx, jobID, alertengine.DailyReportJobKind, metadata, createdBy); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to start report job")
		return
	}
	ctx.SetStatusCode(fasthttp.StatusAccepted)
	job, err := h.dailyReportJobStore.GetSidekiqJob(requestCtx, jobID)
	if err == nil && job != nil {
		SendJSON(ctx, dailyReportJobStatusFromRow(job))
		return
	}
	SendJSON(ctx, dailyReportJobStatus{ID: jobID, Status: tables.SidekiqStatusPending, Stage: "pending"})
}

func (h *AlertingHandler) getDailyReportJobStatus(ctx *fasthttp.RequestCtx) {
	requestCtx := context.WithoutCancel(ctx)
	if h.dailyReportJobStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Background report runner is not available")
		return
	}
	if id := strings.TrimSpace(string(ctx.QueryArgs().Peek("id"))); id != "" {
		job, err := h.dailyReportJobStore.GetSidekiqJob(requestCtx, id)
		if err != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load report job")
			return
		}
		if job == nil {
			SendError(ctx, fasthttp.StatusNotFound, "Report job not found")
			return
		}
		SendJSON(ctx, dailyReportJobStatusFromRow(job))
		return
	}
	job, err := h.dailyReportJobStore.GetInFlightSidekiqJobByKind(requestCtx, alertengine.DailyReportJobKind)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load running report job")
		return
	}
	if job == nil {
		SendJSON(ctx, dailyReportJobStatus{Status: "idle"})
		return
	}
	SendJSON(ctx, dailyReportJobStatusFromRow(job))
}

func dailyReportJobStatusFromRow(job *tables.TableSidekiqJob) dailyReportJobStatus {
	updatedAt := job.UpdatedAt
	status := dailyReportJobStatus{
		ID:        job.ID,
		Status:    job.Status,
		LastError: job.LastError,
		StartedAt: job.StartedAt,
		UpdatedAt: &updatedAt,
	}
	if job.Metadata != "" {
		var metadata alertengine.DailyReportJobMeta
		if err := sonic.Unmarshal([]byte(job.Metadata), &metadata); err == nil {
			status.Stage = metadata.Stage
			status.Processed = metadata.Processed
			status.Percent = metadata.Percent
			status.RunID = metadata.RunID
			status.Deliver = metadata.Deliver
			status.Message = metadata.Message
		}
	}
	return status
}

func (h *AlertingHandler) acquireDailyReportEnqueueLock(ctx context.Context) (func(), error) {
	const lockKey = "bifrost:daily-report:enqueue"
	now := time.Now().UTC()
	lock, err := h.dailyReportJobStore.GetLock(ctx, lockKey)
	if err != nil {
		return nil, err
	}
	if lock != nil && lock.ExpiresAt.After(now) {
		return nil, nil
	}
	if lock != nil {
		if _, err := h.dailyReportJobStore.CleanupExpiredLockByKey(ctx, lockKey); err != nil {
			return nil, err
		}
	}
	holderID := uuid.NewString()
	acquired, err := h.dailyReportJobStore.TryAcquireLock(ctx, &tables.TableDistributedLock{
		LockKey:   lockKey,
		HolderID:  holderID,
		ExpiresAt: now.Add(15 * time.Second),
	})
	if err != nil || !acquired {
		return nil, err
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.dailyReportJobStore.ReleaseLock(releaseCtx, lockKey, holderID)
	}, nil
}

func (h *AlertingHandler) getDailyReportSettings(ctx *fasthttp.RequestCtx) {
	settings, err := h.manager.GetDailyReportSettings(context.WithoutCancel(ctx))
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report settings")
		return
	}
	SendJSON(ctx, map[string]any{"settings": dailyReportSettingsResponse(settings)})
}

func (h *AlertingHandler) updateDailyReportSettings(ctx *fasthttp.RequestCtx) {
	requestCtx := context.WithoutCancel(ctx)
	var req dailyReportSettingsRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
		return
	}
	settings, err := h.manager.GetDailyReportSettings(requestCtx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report settings")
		return
	}
	applyDailyReportSettingsRequest(settings, req)
	updated, err := h.manager.UpdateDailyReportSettings(requestCtx, settings)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"message": "Daily report settings updated successfully", "settings": dailyReportSettingsResponse(updated)})
}

func (h *AlertingHandler) previewDailyReport(ctx *fasthttp.RequestCtx) {
	requestCtx := context.WithoutCancel(ctx)
	var req struct {
		BusinessDate string                      `json:"business_date"`
		Settings     *dailyReportSettingsRequest `json:"settings,omitempty"`
	}
	if len(ctx.PostBody()) > 0 {
		if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
			return
		}
	}
	var settings *tables.TableDailyReportSettings
	if req.Settings != nil {
		loaded, err := h.manager.GetDailyReportSettings(requestCtx)
		if err != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report settings")
			return
		}
		applyDailyReportSettingsRequest(loaded, *req.Settings)
		settings = loaded
	}
	preview, _, resolvedDate, found, err := h.manager.CachedDailyReportPreview(requestCtx, settings, strings.TrimSpace(req.BusinessDate))
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if !found {
		jobRequest, marshalErr := sonic.Marshal(map[string]any{
			"business_date": resolvedDate,
			"deliver":       false,
			"settings":      req.Settings,
		})
		if marshalErr != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to prepare preview job")
			return
		}
		ctx.Request.SetBody(jobRequest)
		h.startDailyReportJob(ctx)
		return
	}
	SendJSON(ctx, map[string]any{
		"preview": map[string]any{
			"business_date":    preview.BusinessDate,
			"settings":         dailyReportSettingsResponse(&preview.Settings),
			"snapshot":         preview.Snapshot,
			"internal_content": preview.InternalContent,
			"external_content": preview.ExternalContent,
		},
	})
}

func (h *AlertingHandler) generateDailyReport(ctx *fasthttp.RequestCtx) {
	var req struct {
		BusinessDate string `json:"business_date"`
	}
	if len(ctx.PostBody()) > 0 {
		if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
			return
		}
	}
	jobRequest, err := sonic.Marshal(map[string]any{
		"business_date": strings.TrimSpace(req.BusinessDate),
		"deliver":       false,
	})
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to prepare report job")
		return
	}
	ctx.Request.SetBody(jobRequest)
	h.startDailyReportJob(ctx)
}

func (h *AlertingHandler) listDailyReportRuns(ctx *fasthttp.RequestCtx) {
	query := logstore.DailyReportHistoryQuery{
		Limit:     queryInt(ctx, "limit", 25),
		Offset:    queryInt(ctx, "offset", 0),
		Audiences: parseDailyReportAudiences(string(ctx.QueryArgs().Peek("audience"))),
	}
	if query.Limit < 1 || query.Limit > 200 || query.Offset < 0 {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid pagination parameters")
		return
	}
	runs, total, err := h.manager.ListDailyReportRuns(context.WithoutCancel(ctx), query)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report runs")
		return
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		items = append(items, dailyReportRunDetailResponse(run))
	}
	SendJSON(ctx, map[string]any{"runs": items, "total": total, "limit": query.Limit, "offset": query.Offset})
}

func (h *AlertingHandler) getDailyReportRun(ctx *fasthttp.RequestCtx) {
	detail, err := h.manager.GetDailyReportRunDetail(context.WithoutCancel(ctx), pathID(ctx))
	if err != nil {
		if errors.Is(err, logstore.ErrNotFound) {
			SendError(ctx, fasthttp.StatusNotFound, "Daily report run not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report run")
		return
	}
	SendJSON(ctx, map[string]any{"run": dailyReportRunDetailResponse(*detail)})
}

func (h *AlertingHandler) deliverDailyReportRun(ctx *fasthttp.RequestCtx) {
	var req struct {
		Audience []string `json:"audience"`
	}
	if len(ctx.PostBody()) > 0 {
		if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
			return
		}
	}
	audiences := make([]logstore.DailyReportAudience, 0, len(req.Audience))
	for _, value := range req.Audience {
		switch strings.TrimSpace(value) {
		case string(logstore.DailyReportAudienceInternal):
			audiences = append(audiences, logstore.DailyReportAudienceInternal)
		case string(logstore.DailyReportAudienceExternal):
			audiences = append(audiences, logstore.DailyReportAudienceExternal)
		}
	}
	detail, err := h.manager.DeliverDailyReportRun(context.WithoutCancel(ctx), pathID(ctx), audiences)
	if err != nil {
		if errors.Is(err, logstore.ErrNotFound) {
			SendError(ctx, fasthttp.StatusNotFound, "Daily report run not found")
			return
		}
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"message": "Daily report delivery attempted", "run": dailyReportRunDetailResponse(*detail)})
}

func applyDailyReportSettingsRequest(settings *tables.TableDailyReportSettings, req dailyReportSettingsRequest) {
	if req.Enabled != nil {
		settings.Enabled = *req.Enabled
	}
	if req.Timezone != "" {
		settings.Timezone = req.Timezone
	}
	if req.GenerateTime != "" {
		settings.GenerateTime = req.GenerateTime
	}
	if req.SendTime != "" {
		settings.SendTime = req.SendTime
	}
	if req.SlowThresholdMs != nil {
		settings.SlowThresholdMs = *req.SlowThresholdMs
	}
	if req.InternalEnabled != nil {
		settings.InternalEnabled = *req.InternalEnabled
	}
	if req.InternalChannelIDs != nil {
		settings.InternalChannelIDs = append([]string(nil), req.InternalChannelIDs...)
	}
	if req.ExternalEnabled != nil {
		settings.ExternalEnabled = *req.ExternalEnabled
	}
	if req.ExternalChannelIDs != nil {
		settings.ExternalChannelIDs = append([]string(nil), req.ExternalChannelIDs...)
	}
}

func dailyReportSettingsResponse(settings *tables.TableDailyReportSettings) map[string]any {
	if settings == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                   settings.ID,
		"enabled":              settings.Enabled,
		"timezone":             settings.Timezone,
		"generate_time":        settings.GenerateTime,
		"send_time":            settings.SendTime,
		"slow_threshold_ms":    settings.SlowThresholdMs,
		"internal_enabled":     settings.InternalEnabled,
		"internal_channel_ids": settings.InternalChannelIDs,
		"external_enabled":     settings.ExternalEnabled,
		"external_channel_ids": settings.ExternalChannelIDs,
		"created_at":           settings.CreatedAt,
		"updated_at":           settings.UpdatedAt,
	}
}

func dailyReportRunDetailResponse(detail alertengine.DailyReportRunDetail) map[string]any {
	return map[string]any{
		"current_status":          detail.CurrentStatus,
		"current_internal_status": detail.CurrentInternalStatus,
		"current_external_status": detail.CurrentExternalStatus,
		"run": map[string]any{
			"id":                     detail.Run.ID,
			"business_date":          detail.Run.BusinessDate,
			"timezone":               detail.Run.Timezone,
			"window_start":           detail.Run.WindowStart,
			"window_end":             detail.Run.WindowEnd,
			"slow_threshold_ms":      detail.Run.SlowThresholdMs,
			"trigger":                detail.Run.Trigger,
			"status":                 detail.Run.Status,
			"internal_status":        detail.Run.InternalStatus,
			"external_status":        detail.Run.ExternalStatus,
			"internal_status_detail": detail.Run.InternalStatusDetail,
			"external_status_detail": detail.Run.ExternalStatusDetail,
			"generated_at":           detail.Run.GeneratedAt,
			"internal_content":       detail.Run.InternalContent,
			"external_content":       detail.Run.ExternalContent,
			"internal_channel_ids":   detail.Run.InternalChannelIDs,
			"external_channel_ids":   detail.Run.ExternalChannelIDs,
			"started_at":             detail.Run.StartedAt,
			"completed_at":           detail.Run.CompletedAt,
			"created_at":             detail.Run.CreatedAt,
			"snapshot":               detail.Run.Snapshot,
			"public_snapshot":        detail.Run.Snapshot.PublicView(),
		},
		"deliveries": detail.Deliveries,
	}
}

func parseDailyReportAudiences(raw string) []logstore.DailyReportAudience {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]logstore.DailyReportAudience, 0, len(parts))
	for _, part := range parts {
		switch strings.TrimSpace(part) {
		case string(logstore.DailyReportAudienceInternal):
			result = append(result, logstore.DailyReportAudienceInternal)
		case string(logstore.DailyReportAudienceExternal):
			result = append(result, logstore.DailyReportAudienceExternal)
		}
	}
	return result
}

func ternaryDailyReportMessage(ok bool, whenTrue, whenFalse string) string {
	if ok {
		return whenTrue
	}
	return whenFalse
}
