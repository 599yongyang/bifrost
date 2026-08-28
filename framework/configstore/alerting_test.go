package configstore

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

func TestAlertCooldownUpsertAndChannelDetach(t *testing.T) {
	store := setupRDBTestStore(t)
	if err := store.DB().AutoMigrate(&tables.TableAlertChannel{}, &tables.TableAlertRule{}, &tables.TableAlertCooldown{}, &tables.TableDailyReportSettings{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := time.Now().UTC().Add(-time.Minute)
	second := first.Add(time.Minute)
	if err := store.UpsertAlertCooldown(ctx, "rule:key", first); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAlertCooldown(ctx, "rule:key", second); err != nil {
		t.Fatal(err)
	}
	cooldowns, err := store.ListAlertCooldowns(ctx)
	if err != nil || len(cooldowns) != 1 || !cooldowns[0].LastSentAt.Equal(second) {
		t.Fatalf("unexpected cooldowns: %#v, %v", cooldowns, err)
	}
	if err := store.UpsertAlertCooldown(ctx, "suppression:key", first); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().Model(&tables.TableAlertCooldown{}).Where("key = ?", "suppression:key").Update("updated_at", first).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteAlertSuppressionsBefore(ctx, second)
	if err != nil || deleted != 1 {
		t.Fatalf("unexpected suppression cleanup: %d, %v", deleted, err)
	}
	cooldowns, err = store.ListAlertCooldowns(ctx)
	if err != nil || len(cooldowns) != 1 || cooldowns[0].Key != "rule:key" {
		t.Fatalf("cleanup removed live cooldown state: %#v, %v", cooldowns, err)
	}
	channel := &tables.TableAlertChannel{ID: "c1", Name: "Channel", Type: tables.AlertChannelWebhook, Enabled: true, Config: map[string]any{"url": "https://example.com"}}
	if err := store.CreateAlertChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	rule := &tables.TableAlertRule{ID: "r1", Name: "Rule", Enabled: true, ScopeType: "provider", ScopeID: "openai", CELExpression: "true", ChannelIDs: []string{"c1"}, WindowSeconds: 300, MinRequests: 1}
	if err := store.CreateAlertRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAlertChannel(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetAlertRule(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || len(updated.ChannelIDs) != 0 {
		t.Fatalf("expected rule to be disabled and detached: %#v", updated)
	}
	settings := &tables.TableDailyReportSettings{
		Timezone: "Asia/Shanghai", GenerateTime: "03:00", SendTime: "09:00", SlowThresholdMs: 15000,
		InternalEnabled: true, ExternalEnabled: true,
		InternalChannelIDs: []string{"c-internal"}, ExternalChannelIDs: []string{"c-external"},
	}
	if err := store.UpsertDailyReportSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	loadedSettings, err := store.GetDailyReportSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSettings.ID != tables.DefaultDailyReportSettingsID || loadedSettings.GenerateTime != "03:00" || loadedSettings.SendTime != "09:00" || len(loadedSettings.InternalChannelIDs) != 1 || len(loadedSettings.ExternalChannelIDs) != 1 {
		t.Fatalf("unexpected daily report settings: %#v", loadedSettings)
	}
	loadedSettings.GenerateTime = "10:00"
	if err := store.UpsertDailyReportSettings(ctx, loadedSettings); err == nil {
		t.Fatal("expected generate_time later than send_time to be rejected")
	}
}
