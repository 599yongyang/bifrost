package handlers

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	alertengine "github.com/maximhq/bifrost/transports/bifrost-http/alerting"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

type AlertingHandler struct {
	manager *alertengine.Manager
	store   configstore.AlertStore
}

func NewAlertingHandler(manager *alertengine.Manager, store configstore.AlertStore) *AlertingHandler {
	if manager == nil || store == nil {
		return nil
	}
	return &AlertingHandler{manager: manager, store: store}
}

func (h *AlertingHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/alerting/channels", lib.ChainMiddlewares(h.listChannels, middlewares...))
	r.GET("/api/alerting/channels/{id}", lib.ChainMiddlewares(h.getChannel, middlewares...))
	r.POST("/api/alerting/channels", lib.ChainMiddlewares(h.createChannel, middlewares...))
	r.POST("/api/alerting/channels/{id}/test", lib.ChainMiddlewares(h.testChannel, middlewares...))
	r.PUT("/api/alerting/channels/{id}", lib.ChainMiddlewares(h.updateChannel, middlewares...))
	r.DELETE("/api/alerting/channels/{id}", lib.ChainMiddlewares(h.deleteChannel, middlewares...))

	r.GET("/api/alerting/rules", lib.ChainMiddlewares(h.listRules, middlewares...))
	r.GET("/api/alerting/rules/evaluation-status", lib.ChainMiddlewares(h.getRuleEvaluationStatus, middlewares...))
	r.GET("/api/alerting/rules/{id}", lib.ChainMiddlewares(h.getRule, middlewares...))
	r.POST("/api/alerting/rules", lib.ChainMiddlewares(h.createRule, middlewares...))
	r.POST("/api/alerting/rules/{id}/evaluate", lib.ChainMiddlewares(h.evaluateRuleNow, middlewares...))
	r.PUT("/api/alerting/rules/{id}", lib.ChainMiddlewares(h.updateRule, middlewares...))
	r.DELETE("/api/alerting/rules/{id}", lib.ChainMiddlewares(h.deleteRule, middlewares...))

	r.GET("/api/alerting/history", lib.ChainMiddlewares(h.listHistory, middlewares...))
	r.POST("/api/alerting/evaluate", lib.ChainMiddlewares(h.evaluateNow, middlewares...))
}

type channelRequest struct {
	Name        string         `json:"name"`
	Description *string        `json:"description,omitempty"`
	Type        string         `json:"type"`
	Enabled     *bool          `json:"enabled,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
}

func (h *AlertingHandler) listChannels(ctx *fasthttp.RequestCtx) {
	channels, err := h.store.ListAlertChannels(ctx)
	if err != nil {
		SendError(ctx, 500, "Failed to list alert channels")
		return
	}
	redacted := make([]tables.TableAlertChannel, len(channels))
	for i := range channels {
		redacted[i] = channels[i].Redacted()
	}
	SendJSON(ctx, map[string]any{"channels": redacted, "count": len(redacted)})
}

func (h *AlertingHandler) getChannel(ctx *fasthttp.RequestCtx) {
	channel, err := h.store.GetAlertChannel(ctx, pathID(ctx))
	if err != nil {
		sendAlertStoreError(ctx, err, "Alert channel not found", "Failed to get alert channel")
		return
	}
	redacted := channel.Redacted()
	SendJSON(ctx, map[string]any{"channel": redacted})
}

func (h *AlertingHandler) createChannel(ctx *fasthttp.RequestCtx) {
	var req channelRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	channel := &tables.TableAlertChannel{ID: uuid.NewString(), Name: req.Name, Description: description, Type: req.Type, Enabled: enabled, Config: req.Config}
	if err := h.manager.ValidateChannel(channel); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	if err := h.store.CreateAlertChannel(ctx, channel); err != nil {
		SendError(ctx, 500, "Failed to create alert channel: "+err.Error())
		return
	}
	redacted := channel.Redacted()
	SendJSONWithStatus(ctx, map[string]any{"message": "Alert channel created successfully", "channel": redacted}, fasthttp.StatusCreated)
}

func (h *AlertingHandler) updateChannel(ctx *fasthttp.RequestCtx) {
	channel, err := h.store.GetAlertChannel(ctx, pathID(ctx))
	if err != nil {
		sendAlertStoreError(ctx, err, "Alert channel not found", "Failed to get alert channel")
		return
	}
	var req channelRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if req.Name != "" {
		channel.Name = req.Name
	}
	if req.Description != nil {
		channel.Description = *req.Description
	}
	if req.Type != "" {
		channel.Type = req.Type
	}
	if req.Enabled != nil {
		channel.Enabled = *req.Enabled
	}
	if len(req.Config) > 0 {
		channel.Config = mergeAlertChannelConfig(channel.Config, req.Config)
	}
	if err := h.manager.ValidateChannel(channel); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	if err := h.store.UpdateAlertChannel(ctx, channel); err != nil {
		SendError(ctx, 500, "Failed to update alert channel: "+err.Error())
		return
	}
	redacted := channel.Redacted()
	SendJSON(ctx, map[string]any{"message": "Alert channel updated successfully", "channel": redacted})
}

func mergeAlertChannelConfig(existing, incoming map[string]any) map[string]any {
	merged := make(map[string]any, len(existing)+len(incoming))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range incoming {
		if value == "***redacted***" {
			continue
		}
		incomingMap, incomingIsMap := value.(map[string]any)
		existingMap, existingIsMap := merged[key].(map[string]any)
		if incomingIsMap && existingIsMap {
			merged[key] = mergeAlertChannelConfig(existingMap, incomingMap)
			continue
		}
		merged[key] = value
	}
	return merged
}

func (h *AlertingHandler) deleteChannel(ctx *fasthttp.RequestCtx) {
	if err := h.store.DeleteAlertChannel(ctx, pathID(ctx)); err != nil {
		sendAlertStoreError(ctx, err, "Alert channel not found", "Failed to delete alert channel")
		return
	}
	SendJSON(ctx, map[string]any{"message": "Alert channel deleted successfully"})
}

func (h *AlertingHandler) testChannel(ctx *fasthttp.RequestCtx) {
	channel, err := h.store.GetAlertChannel(ctx, pathID(ctx))
	if err != nil {
		sendAlertStoreError(ctx, err, "Alert channel not found", "Failed to get alert channel")
		return
	}
	// fasthttp.RequestCtx implements context.Context for value access, but its
	// Done method panics. Strip cancellation before handing it to net/http.
	if err := h.manager.TestChannel(context.WithoutCancel(ctx), channel); err != nil {
		SendError(ctx, fasthttp.StatusBadGateway, "Test notification failed: "+err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"message": "Test notification sent successfully"})
}

type ruleRequest struct {
	Name                    string         `json:"name"`
	Description             *string        `json:"description,omitempty"`
	Enabled                 *bool          `json:"enabled,omitempty"`
	ScopeType               string         `json:"scope_type"`
	ScopeID                 string         `json:"scope_id"`
	TargetType              *string        `json:"target_type,omitempty"`
	TargetID                *string        `json:"target_id,omitempty"`
	CELExpression           string         `json:"cel_expression"`
	ChannelIDs              []string       `json:"channel_ids"`
	Query                   map[string]any `json:"query,omitempty"`
	LegacyQueryBuilder      map[string]any `json:"query_builder,omitempty"`
	CooldownMilliseconds    *int64         `json:"cooldown_milliseconds,omitempty"`
	WindowSeconds           *int64         `json:"window_seconds,omitempty"`
	MinRequests             *int64         `json:"min_requests,omitempty"`
	NotifyOncePerResetCycle *bool          `json:"notify_once_per_reset_cycle,omitempty"`
}

func (h *AlertingHandler) listRules(ctx *fasthttp.RequestCtx) {
	rules, err := h.store.ListAlertRules(ctx)
	if err != nil {
		SendError(ctx, 500, "Failed to list alert rules")
		return
	}
	SendJSON(ctx, map[string]any{"rules": rules, "count": len(rules)})
}

func (h *AlertingHandler) getRule(ctx *fasthttp.RequestCtx) {
	rule, err := h.store.GetAlertRule(ctx, pathID(ctx))
	if err != nil {
		sendAlertStoreError(ctx, err, "Alert rule not found", "Failed to get alert rule")
		return
	}
	SendJSON(ctx, map[string]any{"rule": rule})
}

func (h *AlertingHandler) createRule(ctx *fasthttp.RequestCtx) {
	var req ruleRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cooldown := int64(alertengine.DefaultRuleCooldown / time.Millisecond)
	if req.CooldownMilliseconds != nil {
		cooldown = *req.CooldownMilliseconds
	}
	windowSeconds := int64(300)
	if req.WindowSeconds != nil {
		windowSeconds = *req.WindowSeconds
	}
	minRequests := int64(1)
	if req.MinRequests != nil {
		minRequests = *req.MinRequests
	}
	notifyOnce := req.NotifyOncePerResetCycle != nil && *req.NotifyOncePerResetCycle
	query := req.Query
	if query == nil {
		query = req.LegacyQueryBuilder
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	rule := &tables.TableAlertRule{ID: uuid.NewString(), Name: req.Name, Description: description, Enabled: enabled, ScopeType: req.ScopeType, ScopeID: req.ScopeID, TargetType: req.TargetType, TargetID: req.TargetID, CELExpression: req.CELExpression, ChannelIDs: req.ChannelIDs, QueryBuilder: query, CooldownMilliseconds: cooldown, WindowSeconds: windowSeconds, MinRequests: minRequests, NotifyOncePerResetCycle: notifyOnce}
	if err := h.manager.ValidateRule(ctx, rule); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	if err := h.store.CreateAlertRule(ctx, rule); err != nil {
		SendError(ctx, 500, "Failed to create alert rule: "+err.Error())
		return
	}
	SendJSONWithStatus(ctx, map[string]any{"message": "Alert rule created successfully", "rule": rule}, fasthttp.StatusCreated)
}

func (h *AlertingHandler) updateRule(ctx *fasthttp.RequestCtx) {
	rule, err := h.store.GetAlertRule(ctx, pathID(ctx))
	if err != nil {
		sendAlertStoreError(ctx, err, "Alert rule not found", "Failed to get alert rule")
		return
	}
	var req ruleRequest
	if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
		SendError(ctx, 400, "Invalid JSON")
		return
	}
	if req.Name != "" {
		rule.Name = req.Name
	}
	if req.Description != nil {
		rule.Description = *req.Description
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.ScopeType != "" {
		rule.ScopeType = req.ScopeType
	}
	if req.ScopeID != "" {
		rule.ScopeID = req.ScopeID
	}
	rule.TargetType, rule.TargetID = req.TargetType, req.TargetID
	if req.CELExpression != "" {
		rule.CELExpression = req.CELExpression
	}
	if req.ChannelIDs != nil {
		rule.ChannelIDs = req.ChannelIDs
	}
	if req.Query != nil {
		rule.QueryBuilder = req.Query
	} else if req.LegacyQueryBuilder != nil {
		rule.QueryBuilder = req.LegacyQueryBuilder
	}
	if req.CooldownMilliseconds != nil {
		rule.CooldownMilliseconds = *req.CooldownMilliseconds
	}
	if req.WindowSeconds != nil {
		rule.WindowSeconds = *req.WindowSeconds
	}
	if req.MinRequests != nil {
		rule.MinRequests = *req.MinRequests
	}
	if req.NotifyOncePerResetCycle != nil {
		rule.NotifyOncePerResetCycle = *req.NotifyOncePerResetCycle
	}
	if err := h.manager.ValidateRule(ctx, rule); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	if err := h.store.UpdateAlertRule(ctx, rule); err != nil {
		SendError(ctx, 500, "Failed to update alert rule: "+err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"message": "Alert rule updated successfully", "rule": rule})
}

func (h *AlertingHandler) deleteRule(ctx *fasthttp.RequestCtx) {
	if err := h.store.DeleteAlertRule(ctx, pathID(ctx)); err != nil {
		sendAlertStoreError(ctx, err, "Alert rule not found", "Failed to delete alert rule")
		return
	}
	SendJSON(ctx, map[string]any{"message": "Alert rule deleted successfully"})
}

func (h *AlertingHandler) getRuleEvaluationStatus(ctx *fasthttp.RequestCtx) {
	SendJSON(ctx, map[string]any{"running_rule_ids": h.manager.RunningRuleEvaluations()})
}

func (h *AlertingHandler) evaluateRuleNow(ctx *fasthttp.RequestCtx) {
	var req struct {
		IgnoreCooldown bool `json:"ignore_cooldown"`
	}
	if len(ctx.PostBody()) > 0 {
		if err := sonic.Unmarshal(ctx.PostBody(), &req); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
			return
		}
	}
	result, err := h.manager.EvaluateRuleNow(context.WithoutCancel(ctx), pathID(ctx), req.IgnoreCooldown)
	if err != nil {
		switch {
		case errors.Is(err, configstore.ErrNotFound):
			SendError(ctx, fasthttp.StatusNotFound, "Alert rule not found")
		case errors.Is(err, alertengine.ErrRuleEvaluationInProgress):
			SendError(ctx, fasthttp.StatusConflict, err.Error())
		case errors.Is(err, alertengine.ErrAlertingNotLeader):
			SendError(ctx, fasthttp.StatusServiceUnavailable, err.Error())
		case errors.Is(err, alertengine.ErrAlertRuleDisabled):
			SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		default:
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to evaluate alert rule: "+err.Error())
		}
		return
	}
	SendJSON(ctx, map[string]any{"message": "Alert rule evaluation completed", "result": result})
}

func (h *AlertingHandler) listHistory(ctx *fasthttp.RequestCtx) {
	limit := queryInt(ctx, "limit", 25)
	offset := queryInt(ctx, "offset", 0)
	if limit < 1 || limit > 1000 || offset < 0 {
		SendError(ctx, 400, "Invalid pagination parameters")
		return
	}
	params := logstore.AlertHistoryQuery{
		Limit: limit, Offset: offset,
		Statuses:     splitAlertFilter(string(ctx.QueryArgs().Peek("status"))),
		ScopeTypes:   splitAlertFilter(string(ctx.QueryArgs().Peek("scope_type"))),
		ChannelTypes: splitAlertFilter(string(ctx.QueryArgs().Peek("channel_type"))),
	}
	history, total, err := h.manager.ListHistory(ctx, params)
	if err != nil {
		SendError(ctx, 500, "Failed to list alert history")
		return
	}
	SendJSON(ctx, map[string]any{"history": history, "total": total, "limit": limit, "offset": offset})
}

func splitAlertFilter(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func (h *AlertingHandler) evaluateNow(ctx *fasthttp.RequestCtx) {
	if err := h.manager.EvaluateNow(context.WithoutCancel(ctx)); err != nil {
		SendError(ctx, 500, "Failed to evaluate alerts: "+err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"message": "Alert evaluation completed"})
}

func pathID(ctx *fasthttp.RequestCtx) string {
	id, _ := ctx.UserValue("id").(string)
	return id
}

func queryInt(ctx *fasthttp.RequestCtx, name string, fallback int) int {
	raw := string(ctx.QueryArgs().Peek(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	return value
}

func sendAlertStoreError(ctx *fasthttp.RequestCtx, err error, notFound, internal string) {
	if errors.Is(err, configstore.ErrNotFound) {
		SendError(ctx, 404, notFound)
		return
	}
	SendError(ctx, 500, internal)
}
