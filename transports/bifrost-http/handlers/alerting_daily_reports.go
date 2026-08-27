package handlers

import (
	"errors"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
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

func (h *AlertingHandler) getDailyReportSettings(ctx *fasthttp.RequestCtx) {
	settings, err := h.manager.GetDailyReportSettings(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report settings")
		return
	}
	SendJSON(ctx, map[string]any{"settings": dailyReportSettingsResponse(settings)})
}

func (h *AlertingHandler) updateDailyReportSettings(ctx *fasthttp.RequestCtx) {
	var req dailyReportSettingsRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
		return
	}
	settings, err := h.manager.GetDailyReportSettings(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report settings")
		return
	}
	applyDailyReportSettingsRequest(settings, req)
	updated, err := h.manager.UpdateDailyReportSettings(ctx, settings)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"message": "Daily report settings updated successfully", "settings": dailyReportSettingsResponse(updated)})
}

func (h *AlertingHandler) previewDailyReport(ctx *fasthttp.RequestCtx) {
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
		loaded, err := h.manager.GetDailyReportSettings(ctx)
		if err != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load daily report settings")
			return
		}
		applyDailyReportSettingsRequest(loaded, *req.Settings)
		settings = loaded
	}
	preview, err := h.manager.PreviewDailyReport(ctx, settings, strings.TrimSpace(req.BusinessDate))
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
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
	result, err := h.manager.GenerateDailyReportNow(ctx, strings.TrimSpace(req.BusinessDate))
	if err != nil {
		switch {
		case errors.Is(err, alertengine.ErrDailyReportGenerationInProgress):
			SendError(ctx, fasthttp.StatusConflict, err.Error())
		default:
			SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		}
		return
	}
	detail, err := h.manager.GetDailyReportRunDetail(ctx, result.Run.ID)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "Daily report generated but its detail could not be loaded")
		return
	}
	SendJSON(ctx, map[string]any{
		"message": ternaryDailyReportMessage(result.Created, "Daily report generated successfully", "Daily report already exists for that business date"),
		"result":  dailyReportRunDetailResponse(*detail),
		"created": result.Created,
	})
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
	runs, total, err := h.manager.ListDailyReportRuns(ctx, query)
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
	detail, err := h.manager.GetDailyReportRunDetail(ctx, pathID(ctx))
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
	detail, err := h.manager.DeliverDailyReportRun(ctx, pathID(ctx), audiences)
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
