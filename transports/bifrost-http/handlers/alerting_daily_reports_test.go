package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/sidekiq"
	alertengine "github.com/maximhq/bifrost/transports/bifrost-http/alerting"
	"github.com/valyala/fasthttp"
)

func TestDailyReportHandlersUseDurableBackgroundJobs(t *testing.T) {
	baseCtx := context.Background()
	config, err := configstore.NewConfigStore(baseCtx, &configstore.Config{
		Enabled: true, Type: configstore.ConfigStoreTypeSQLite,
		Config: &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "daily-config.db")},
	}, &mockLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Close(context.Background()) })
	logs, err := logstore.NewLogStore(baseCtx, &logstore.Config{
		Enabled: true, Type: logstore.LogStoreTypeSQLite,
		Config: &logstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "daily-logs.db")},
	}, &mockLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logs.Close(context.Background()) })
	alertStore := config.(configstore.AlertStore)
	manager, err := alertengine.NewManager(alertStore, nil, logs, logs, &mockLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := sidekiq.New(config, &mockLogger{}, 1, "daily-report-test")
	t.Cleanup(runner.Shutdown)
	handler := NewAlertingHandler(manager, alertStore)
	handler.SetDailyReportJobBackend(runner, config)

	put := &fasthttp.RequestCtx{}
	put.Request.SetBodyString(`{"enabled":false,"timezone":"UTC","generate_time":"03:00","send_time":"09:00","internal_enabled":false,"external_enabled":false}`)
	handler.updateDailyReportSettings(put)
	if put.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("settings PUT status=%d body=%s", put.Response.StatusCode(), put.Response.Body())
	}
	get := &fasthttp.RequestCtx{}
	handler.getDailyReportSettings(get)
	if get.Response.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(get.Response.Body()), `"timezone":"UTC"`) {
		t.Fatalf("settings GET status=%d body=%s", get.Response.StatusCode(), get.Response.Body())
	}
	invalid := &fasthttp.RequestCtx{}
	invalid.Request.SetBodyString(`{"generate_time":"10:00","send_time":"09:00"}`)
	handler.updateDailyReportSettings(invalid)
	if invalid.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("invalid settings status=%d body=%s", invalid.Response.StatusCode(), invalid.Response.Body())
	}

	missing := &fasthttp.RequestCtx{}
	missing.SetUserValue("id", "missing")
	handler.getDailyReportRun(missing)
	if missing.Response.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("missing detail status=%d body=%s", missing.Response.StatusCode(), missing.Response.Body())
	}
	previewMiss := &fasthttp.RequestCtx{}
	previewMiss.Request.SetBodyString(`{"business_date":"2026-08-24"}`)
	handler.previewDailyReport(previewMiss)
	if previewMiss.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("cache-miss preview must enqueue: status=%d body=%s", previewMiss.Response.StatusCode(), previewMiss.Response.Body())
	}
	var previewJob dailyReportJobStatus
	if err := json.Unmarshal(previewMiss.Response.Body(), &previewJob); err != nil || previewJob.ID == "" || previewJob.Deliver {
		t.Fatalf("invalid preview job: %#v err=%v", previewJob, err)
	}
	waitForDailyReportJob(t, baseCtx, config, previewJob.ID)
	previewHit := &fasthttp.RequestCtx{}
	previewHit.Request.SetBodyString(`{"business_date":"2026-08-24"}`)
	handler.previewDailyReport(previewHit)
	if previewHit.Response.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(previewHit.Response.Body()), `"preview"`) {
		t.Fatalf("cache-hit preview status=%d body=%s", previewHit.Response.StatusCode(), previewHit.Response.Body())
	}

	generate := &fasthttp.RequestCtx{}
	generate.Request.SetBodyString(`{"business_date":"2026-08-25"}`)
	handler.generateDailyReport(generate)
	if generate.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("generate alias must enqueue: status=%d body=%s", generate.Response.StatusCode(), generate.Response.Body())
	}
	var accepted dailyReportJobStatus
	if err := json.Unmarshal(generate.Response.Body(), &accepted); err != nil || accepted.ID == "" {
		t.Fatalf("invalid accepted job: %#v err=%v body=%s", accepted, err, generate.Response.Body())
	}
	if accepted.Deliver {
		t.Fatal("legacy generate alias must enqueue snapshot generation without delivery")
	}

	waitForDailyReportJob(t, baseCtx, config, accepted.ID)

	status := &fasthttp.RequestCtx{}
	status.QueryArgs().Set("id", accepted.ID)
	handler.getDailyReportJobStatus(status)
	if status.Response.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(status.Response.Body()), accepted.ID) {
		t.Fatalf("job status=%d body=%s", status.Response.StatusCode(), status.Response.Body())
	}
	duplicate := &fasthttp.RequestCtx{}
	duplicate.Request.SetBodyString(`{"business_date":"2026-08-25","deliver":false}`)
	handler.startDailyReportJob(duplicate)
	if duplicate.Response.StatusCode() >= fasthttp.StatusBadRequest || !strings.Contains(string(duplicate.Response.Body()), accepted.ID) {
		t.Fatalf("idempotent duplicate status=%d body=%s", duplicate.Response.StatusCode(), duplicate.Response.Body())
	}
}

func waitForDailyReportJob(t *testing.T, ctx context.Context, store configstore.ConfigStore, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := store.GetSidekiqJob(ctx, id)
		if err == nil && job != nil && (job.Status == "completed" || job.Status == "failed") {
			if job.Status != "completed" {
				t.Fatalf("daily report job failed: %#v", job)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("daily report job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
