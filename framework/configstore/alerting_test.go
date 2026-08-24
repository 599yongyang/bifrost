package configstore

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

func TestAlertCooldownUpsertAndChannelDetach(t *testing.T) {
	store := setupRDBTestStore(t)
	if err := store.DB().AutoMigrate(&tables.TableAlertChannel{}, &tables.TableAlertRule{}, &tables.TableAlertCooldown{}); err != nil {
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
}
