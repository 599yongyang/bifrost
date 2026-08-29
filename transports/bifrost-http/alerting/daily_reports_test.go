package alerting

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/stretchr/testify/require"
)

type noopDailyReportLogger struct{}

func (noopDailyReportLogger) Debug(string, ...any)                   {}
func (noopDailyReportLogger) Info(string, ...any)                    {}
func (noopDailyReportLogger) Warn(string, ...any)                    {}
func (noopDailyReportLogger) Error(string, ...any)                   {}
func (noopDailyReportLogger) Fatal(string, ...any)                   {}
func (noopDailyReportLogger) SetLevel(schemas.LogLevel)              {}
func (noopDailyReportLogger) SetOutputType(schemas.LoggerOutputType) {}
func (noopDailyReportLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func TestDailyReportPreviewAndGenerateAreIdempotent(t *testing.T) {
	ctx := context.Background()
	logs, err := logstore.NewLogStore(ctx, &logstore.Config{
		Enabled: true,
		Type:    logstore.LogStoreTypeSQLite,
		Config:  &logstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "daily-report-logstore.db")},
	}, noopDailyReportLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logs.Close(context.Background()) })

	windowStart := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	require.NoError(t, logs.Create(ctx, &logstore.Log{
		ID:            "root-success",
		Timestamp:     windowStart.Add(2 * time.Hour),
		Object:        "chat.completion",
		Provider:      "openai",
		Model:         "gpt-4o",
		Status:        "success",
		SelectedKeyID: "sk1",
		CreatedAt:     windowStart.Add(2 * time.Hour),
		Latency:       floatPtr(1100),
		TotalTokens:   12,
	}))

	store := &memoryAlertStore{
		channels: []tables.TableAlertChannel{
			{ID: "ops-webhook", Name: "Ops Webhook", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "http://127.0.0.1/report"}},
		},
		settings: &tables.TableDailyReportSettings{
			ID:                 tables.DefaultDailyReportSettingsID,
			Enabled:            true,
			Timezone:           "UTC",
			SendTime:           "09:00",
			SlowThresholdMs:    30000,
			InternalEnabled:    true,
			InternalChannelIDs: []string{"ops-webhook"},
		},
	}
	manager, err := NewManager(store, nil, logs, logs, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	require.NoError(t, err)
	manager.now = func() time.Time { return time.Date(2026, 8, 27, 3, 30, 0, 0, time.UTC) }

	var payloads []map[string]any
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		payloads = append(payloads, payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}

	preview, err := manager.PreviewDailyReport(ctx, nil, "2026-08-26")
	require.NoError(t, err)
	require.Contains(t, preview.InternalContent, "openai")
	require.NotContains(t, preview.ExternalContent, "openai")

	firstRun, err := manager.GenerateDailyReportNow(ctx, "2026-08-26")
	require.NoError(t, err)
	require.True(t, firstRun.Created)
	require.Len(t, firstRun.Deliveries, 1)
	require.Len(t, payloads, 1)

	secondRun, err := manager.GenerateDailyReportNow(ctx, "2026-08-26")
	require.NoError(t, err)
	require.False(t, secondRun.Created)
	require.Len(t, payloads, 1)
	manager.now = func() time.Time { return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC) }
	reusedPreview, err := manager.PreviewDailyReport(ctx, nil, "2026-08-26")
	require.NoError(t, err)
	require.Equal(t, firstRun.Run.Snapshot.Overview.UserRequests, reusedPreview.Snapshot.Overview.UserRequests)
	reusedRun, err := manager.GenerateDailyReportNow(ctx, "2026-08-26")
	require.NoError(t, err)
	require.False(t, reusedRun.Created)
	manualPreview, err := manager.PreviewDailyReport(ctx, nil, "2026-08-24")
	require.NoError(t, err)
	require.Equal(t, "2026-08-24", manualPreview.BusinessDate)
	manager.now = func() time.Time { return time.Date(2026, 8, 27, 3, 30, 0, 0, time.UTC) }

	originalCompletedAt := firstRun.Run.CompletedAt
	originalStatus := firstRun.Run.Status
	redelivered, err := manager.DeliverDailyReportRun(ctx, firstRun.Run.ID, []logstore.DailyReportAudience{logstore.DailyReportAudienceInternal})
	require.NoError(t, err)
	require.Len(t, redelivered.Deliveries, 2)
	require.Equal(t, originalStatus, redelivered.Run.Status)
	require.Equal(t, originalCompletedAt, redelivered.Run.CompletedAt)
	require.Len(t, payloads, 2)

	require.NoError(t, logs.CreateDailyReportDelivery(ctx, &logstore.DailyReportDelivery{
		ID: "delivery-failed-latest", RunID: firstRun.Run.ID, Audience: logstore.DailyReportAudienceInternal,
		ChannelID: "ops-webhook", AttemptNo: 3, Status: logstore.DailyReportDeliveryStatusFailed, CreatedAt: time.Now().UTC(),
	}))
	failedCurrent, err := manager.GetDailyReportRunDetail(ctx, firstRun.Run.ID)
	require.NoError(t, err)
	require.Equal(t, logstore.DailyReportRunStatusFailed, failedCurrent.CurrentStatus)
	require.Equal(t, originalStatus, failedCurrent.Run.Status, "persisted run remains the original audit outcome")

	require.NoError(t, logs.CreateDailyReportDelivery(ctx, &logstore.DailyReportDelivery{
		ID: "delivery-success-latest", RunID: firstRun.Run.ID, Audience: logstore.DailyReportAudienceInternal,
		ChannelID: "ops-webhook", AttemptNo: 4, Status: logstore.DailyReportDeliveryStatusDelivered, CreatedAt: time.Now().UTC(),
	}))
	successCurrent, err := manager.GetDailyReportRunDetail(ctx, firstRun.Run.ID)
	require.NoError(t, err)
	require.Equal(t, logstore.DailyReportRunStatusSuccess, successCurrent.CurrentStatus)

	prepared, err := manager.generateDailyReport(ctx, store.settings, "2026-08-25", "scheduled", false)
	require.NoError(t, err)
	require.Equal(t, logstore.DailyReportRunStatusPrepared, prepared.Run.Status)
	require.Len(t, payloads, 2, "off-peak generation must not send a notification")
	preparedDetail, err := manager.GetDailyReportRunDetail(ctx, prepared.Run.ID)
	require.NoError(t, err)
	require.Equal(t, logstore.DailyReportRunStatusPrepared, preparedDetail.CurrentStatus)
	_, err = manager.DeliverDailyReportRun(ctx, prepared.Run.ID, []logstore.DailyReportAudience{logstore.DailyReportAudienceInternal})
	require.ErrorContains(t, err, "not in a completed state")
	_, _, err = manager.deliverPreparedDailyReport(ctx, prepared.Run.ID)
	require.NoError(t, err)
	require.Len(t, payloads, 3, "send phase should reuse the frozen snapshot")

	jobMetadata, jobID, err := NewDailyReportJobMetadata("2026-08-23", false, "manual", store.settings)
	require.NoError(t, err)
	require.NotEmpty(t, jobID)
	store.settings.Timezone = "Asia/Shanghai"
	var progressUpdates []string
	finalMetadata, err := manager.RunDailyReportJob(ctx, jobMetadata, func(metadata string) error {
		progressUpdates = append(progressUpdates, metadata)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, progressUpdates)
	var finalJob DailyReportJobMeta
	require.NoError(t, sonic.Unmarshal([]byte(finalMetadata), &finalJob))
	require.Equal(t, "completed", finalJob.Stage)
	require.Equal(t, 100, finalJob.Percent)
	require.NotEmpty(t, finalJob.RunID)
	jobRun, err := manager.GetDailyReportRunDetail(ctx, finalJob.RunID)
	require.NoError(t, err)
	require.Equal(t, "UTC", jobRun.Run.Timezone, "job must use the settings snapshot captured at enqueue time")
	scheduledMetadata, _, err := NewDailyReportJobMetadata("2026-08-22", false, "scheduled", &finalJob.Settings)
	require.NoError(t, err)
	scheduledFinal, err := manager.RunDailyReportJob(ctx, scheduledMetadata, nil)
	require.NoError(t, err)
	var scheduledJob DailyReportJobMeta
	require.NoError(t, sonic.Unmarshal([]byte(scheduledFinal), &scheduledJob))
	scheduledRun, err := manager.GetDailyReportRunDetail(ctx, scheduledJob.RunID)
	require.NoError(t, err)
	require.Equal(t, "scheduled", scheduledRun.Run.Trigger)

	var scheduledDate string
	var scheduledDeliver bool
	manager.SetDailyReportJobEnqueuer(func(_ context.Context, businessDate string, deliver bool, _ *tables.TableDailyReportSettings) error {
		scheduledDate, scheduledDeliver = businessDate, deliver
		return nil
	})
	store.settings.Timezone = "UTC"
	store.settings.GenerateTime = "03:00"
	store.settings.SendTime = "09:00"
	manager.now = func() time.Time { return time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC) }
	require.NoError(t, manager.maybeDispatchScheduledDailyReport(ctx))
	require.Equal(t, "2026-08-28", scheduledDate)
	require.False(t, scheduledDeliver, "generation sweep must enqueue a snapshot-only job")

	_, err = manager.generateDailyReport(ctx, store.settings, "2026-08-27", "scheduled", false)
	require.NoError(t, err)
	scheduledDate = ""
	manager.now = func() time.Time { return time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC) }
	require.NoError(t, manager.maybeDispatchScheduledDailyReport(ctx))
	require.Equal(t, "2026-08-27", scheduledDate)
	require.True(t, scheduledDeliver, "send sweep must enqueue delivery instead of blocking the sweep")
}

func TestDailyReportRunIDIsDeterministicPerBusinessDay(t *testing.T) {
	require.Equal(t, int64(10000), defaultDailyReportSettings().SlowThresholdMs)
	require.Equal(t, dailyReportRunID("Asia/Shanghai", "2026-08-26"), dailyReportRunID("Asia/Shanghai", "2026-08-26"))
	require.NotEqual(t, dailyReportRunID("Asia/Shanghai", "2026-08-26"), dailyReportRunID("UTC", "2026-08-26"))
	require.NotEqual(t, dailyReportRunID("Asia/Shanghai", "2026-08-26"), dailyReportRunID("Asia/Shanghai", "2026-08-27"))
	settings := defaultDailyReportSettings()
	_, previewJobID, err := NewDailyReportJobMetadata("2026-08-26", false, "manual", settings)
	require.NoError(t, err)
	settings.UpdatedAt = time.Now().UTC()
	_, samePreviewJobID, err := NewDailyReportJobMetadata("2026-08-26", false, "manual", settings)
	require.NoError(t, err)
	require.Equal(t, previewJobID, samePreviewJobID, "non-semantic timestamps must not change the job fingerprint")
	_, deliveryJobID, err := NewDailyReportJobMetadata("2026-08-26", true, "manual", settings)
	require.NoError(t, err)
	require.NotEqual(t, previewJobID, deliveryJobID)
	_, scheduledJobID, err := NewDailyReportJobMetadata("2026-08-26", false, "scheduled", settings)
	require.NoError(t, err)
	require.NotEqual(t, previewJobID, scheduledJobID, "trigger is audit-significant and must affect the fingerprint")
}

func TestExternalDailyReportWebhookDoesNotLeakInternalSnapshot(t *testing.T) {
	ctx := context.Background()
	store := &memoryAlertStore{}
	manager, err := NewManager(store, nil, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	require.NoError(t, err)

	var body []byte
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err = io.ReadAll(req.Body)
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	run := &logstore.DailyReportRun{
		ID:           "run-1",
		BusinessDate: "2026-08-26",
		Timezone:     "Asia/Shanghai",
		Snapshot: logstore.DailyReportSnapshot{
			BusinessDate: "2026-08-26",
			Timezone:     "Asia/Shanghai",
			Overview:     logstore.DailyReportOverview{UserRequests: 10, UserSuccessRate: 99},
			Providers: []logstore.DailyProviderReportRow{{
				Provider: "secret-provider",
				Models:   []logstore.DailyModelReportRow{{Provider: "secret-provider", Model: "secret-model"}},
			}},
			Trends: logstore.DailyReportTrends{SystemSuccessRate: logstore.DailyReportTrendValue{Current: 12.34}},
		},
	}
	channel := &tables.TableAlertChannel{
		ID:      "external-webhook",
		Name:    "External Webhook",
		Type:    tables.AlertChannelWebhook,
		Enabled: true,
		Config:  map[string]any{"url": "http://127.0.0.1/report"},
	}
	require.NoError(t, manager.sendDailyReport(ctx, channel, run, logstore.DailyReportAudienceExternal, "customer-safe content"))
	require.NotContains(t, string(body), "secret-provider")
	require.NotContains(t, string(body), "secret-model")
	require.NotContains(t, string(body), "system_success_rate")
	require.Contains(t, string(body), "customer-safe content")
}

func TestDailyReportWeComDeliveryDoesNotLeakResponseMessage(t *testing.T) {
	const secretResponse = "secret response token"
	store := &memoryAlertStore{}
	manager, err := NewManager(store, nil, nil, store, nil, &Config{WebhookNetwork: NetworkConfig{AllowHTTP: true, AllowPrivateNetwork: true}})
	require.NoError(t, err)
	manager.privateClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"errcode":93000,"errmsg":"` + secretResponse + `"}`)), Header: make(http.Header)}, nil
	})}
	err = manager.SendNotification(context.Background(), &tables.TableAlertChannel{
		ID: "wecom", Name: "WeCom", Type: tables.AlertChannelWeCom, Enabled: true,
		Config: map[string]any{"webhook_url": "http://127.0.0.1/report"},
	}, ChannelNotification{Title: "Daily", Text: "Daily", Markdown: "Daily", Timestamp: time.Now()})
	require.ErrorContains(t, err, "errcode 93000")
	require.NotContains(t, err.Error(), secretResponse)
}

func floatPtr(value float64) *float64 {
	return &value
}
