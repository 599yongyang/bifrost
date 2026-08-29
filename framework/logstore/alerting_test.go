package logstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAlertHistorySQLiteLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := NewLogStore(ctx, &Config{Enabled: true, Type: LogStoreTypeSQLite, Config: &SQLiteConfig{Path: filepath.Join(t.TempDir(), "logs.db")}}, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	base := time.Now().UTC().Add(-time.Hour)
	rows := []*AlertHistory{
		{ID: "1", RuleID: "r1", RuleName: "Rule", ChannelID: "c1", ChannelType: "slack", ScopeType: "provider", ScopeID: "openai", Status: "sent", Evaluation: map[string]any{"provider_error_rate": 10.0}, CreatedAt: base},
		{ID: "2", RuleID: "r1", RuleName: "Rule", ChannelID: "c1", ChannelType: "slack", ScopeType: "provider", ScopeID: "openai", Status: "sent", Evaluation: map[string]any{"provider_error_rate": 20.0}, CreatedAt: base.Add(time.Minute)},
		{ID: "3", RuleID: "r2", RuleName: "Other", ChannelID: "c2", ChannelType: "webhook", ScopeType: "team", ScopeID: "team-1", Status: "failed", Evaluation: map[string]any{}, CreatedAt: base.Add(2 * time.Minute)},
	}
	for _, row := range rows {
		if err := store.CreateAlertHistory(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	history, total, err := store.ListAlertHistory(ctx, AlertHistoryQuery{Statuses: []string{"sent"}, ScopeTypes: []string{"provider"}, ChannelTypes: []string{"slack"}, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(history) != 2 || history[0].Evaluation["provider_error_rate"] != 20.0 {
		t.Fatalf("unexpected history: total=%d rows=%#v", total, history)
	}
	latestRules, err := store.ListLatestAlertRuleSends(ctx)
	if err != nil || len(latestRules) != 1 || !latestRules[0].CreatedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("unexpected latest rule sends: %#v, %v", latestRules, err)
	}
	deleted, err := store.DeleteAlertHistoryBefore(ctx, base.Add(30*time.Second))
	if err != nil || deleted != 1 {
		t.Fatalf("unexpected delete result: %d, %v", deleted, err)
	}
}
