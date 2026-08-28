package logstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func TestBuildDailyReportSnapshotTracksFallbacksAndExcludesCancelled(t *testing.T) {
	ctx := context.Background()
	store, err := newSqliteLogStore(ctx, &SQLiteConfig{
		Path: filepath.Join(t.TempDir(), "dailyreports.db"),
	}, testLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	currentStart := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	currentEnd := currentStart.Add(24 * time.Hour)
	prevStart := currentStart.Add(-24 * time.Hour)

	err500 := marshalDailyReportError(t, 500, nil)
	err429 := marshalDailyReportError(t, 429, nil)

	mk := func(id string, ts time.Time, provider, model, status string, fallbackIndex int, parentID *string, latency float64, retries int, errDetails string) *Log {
		row := &Log{
			ID:               id,
			Timestamp:        ts,
			Object:           "chat.completion",
			Provider:         provider,
			Model:            model,
			Status:           status,
			FallbackIndex:    fallbackIndex,
			NumberOfRetries:  retries,
			SelectedKeyID:    "sk1",
			CreatedAt:        ts,
			Latency:          &latency,
			ParentRequestID:  parentID,
			ErrorDetails:     errDetails,
			CompletionTokens: 10,
			TotalTokens:      10,
		}
		return row
	}

	rootID := "root-1"
	require.NoError(t, store.Create(ctx, mk("prev-ok", prevStart.Add(2*time.Hour), "openai", "gpt-4o", "success", 0, nil, 400, 0, "")))
	require.NoError(t, store.Create(ctx, mk(rootID, currentStart.Add(2*time.Hour), "openai", "gpt-4o", "error", 0, nil, 1200, 1, err500)))
	// The fallback finishes after the business-day boundary. It still determines
	// the root request's user-facing outcome, while its provider attempt belongs
	// to the following day's provider/model metrics.
	require.NoError(t, store.Create(ctx, mk("fallback-ok", currentEnd.Add(5*time.Second), "anthropic", "claude-3.5", "success", 1, &rootID, 900, 0, "")))
	require.NoError(t, store.Create(ctx, mk("direct-ok", currentStart.Add(3*time.Hour), "openai", "gpt-4o", "success", 0, nil, 35000, 0, "")))
	require.NoError(t, store.Create(ctx, mk("cancelled", currentStart.Add(4*time.Hour), "openai", "gpt-4o", "cancelled", 0, nil, 1000, 0, err429)))

	var progressStages []string
	snapshot, err := store.BuildDailyReportSnapshot(ctx, DailyReportMetricsQuery{
		BusinessDate:    "2026-08-26",
		Timezone:        "UTC",
		WindowStart:     currentStart,
		WindowEnd:       currentEnd,
		SlowThresholdMs: 30000,
		GeneratedAt:     currentEnd,
		Progress: func(progress DailyReportMetricsProgress) {
			progressStages = append(progressStages, progress.Stage)
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), snapshot.Overview.UserRequests)
	require.Equal(t, int64(2), snapshot.Overview.ProviderAttempts)
	require.Equal(t, int64(1), snapshot.Overview.FallbackRecoveries)
	require.InDelta(t, 50.0, snapshot.Overview.FallbackRecoveryRate, 0.01)
	require.InDelta(t, 50.0, snapshot.Overview.SystemSuccessRate, 0.01)
	require.InDelta(t, 100.0, snapshot.Overview.UserSuccessRate, 0.01)
	require.Equal(t, int64(1), snapshot.Overview.SlowRequests)
	require.InDelta(t, 18550.0, snapshot.Overview.AverageLatencyMs, 0.01)
	require.Len(t, snapshot.Providers, 1)
	require.Equal(t, "openai", snapshot.Providers[0].Provider)
	require.Equal(t, int64(2), snapshot.Providers[0].Attempts)
	require.Equal(t, "provider_5xx", snapshot.Overview.ErrorBuckets[0].Key)
	require.InDelta(t, 100.0, snapshot.Trends.FallbackRecoveries.DeltaPercentage, 0.01)
	require.Contains(t, progressStages, "scanning_logs")
	require.Contains(t, progressStages, "building_report")
}

func TestPreviousDailyReportWindowStartUsesLocalCalendarDay(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	current := time.Date(2026, 3, 9, 0, 0, 0, 0, location)
	previous, err := previousDailyReportWindowStart(DailyReportMetricsQuery{
		BusinessDate: "2026-03-09",
		Timezone:     "America/New_York",
		WindowStart:  current.UTC(),
		WindowEnd:    current.AddDate(0, 0, 1).UTC(),
	})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 8, 0, 0, 0, 0, location).UTC(), previous)
}

func marshalDailyReportError(t *testing.T, statusCode int, errorType *string) string {
	t.Helper()
	payload := &schemas.BifrostError{
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "provider error",
			Type:    errorType,
		},
	}
	data, err := sonic.Marshal(payload)
	require.NoError(t, err)
	return string(data)
}
