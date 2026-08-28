package tables

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAlertChannelRedactedPreservesConfigShape(t *testing.T) {
	original := TableAlertChannel{Config: map[string]any{
		"url":     "https://example.com/hook",
		"headers": map[string]any{"X-API-Key": "secret", "X-Tenant": "tenant"},
	}}
	redacted := original.Redacted()
	want := map[string]any{
		"url":     "***redacted***",
		"headers": map[string]any{"X-API-Key": "***redacted***", "X-Tenant": "***redacted***"},
	}
	if !reflect.DeepEqual(redacted.Config, want) {
		t.Fatalf("unexpected redacted config: %#v", redacted.Config)
	}
	if original.Config["url"] != "https://example.com/hook" {
		t.Fatal("redaction mutated the stored config")
	}
}

func TestAlertChannelConfigEncryptedAndRedacted(t *testing.T) {
	db := setupTestDB(t)
	require.NoError(t, db.AutoMigrate(&TableAlertChannel{}))
	channel := &TableAlertChannel{ID: "alert-secret", Name: "secret", Type: AlertChannelWebhook, Enabled: true, Config: map[string]any{
		"url": "https://example.com/hook?token=secret", "headers": map[string]any{"Authorization": "Bearer secret"},
	}}
	require.NoError(t, db.Create(channel).Error)
	row := rawRow(t, db, "alert_channels", channel.ID)
	if row["encryption_status"] != EncryptionStatusEncrypted || strings.Contains(fmt.Sprint(row["config_json"]), "secret") {
		t.Fatalf("alert config was not encrypted: %#v", row)
	}
	var fetched TableAlertChannel
	require.NoError(t, db.First(&fetched, "id = ?", channel.ID).Error)
	if fetched.Config["url"] != "https://example.com/hook?token=secret" {
		t.Fatalf("decrypted config = %#v", fetched.Config)
	}
	if strings.Contains(toJSONString(t, fetched.Redacted().Config), "Bearer secret") {
		t.Fatal("redacted config leaked authorization")
	}
}

func toJSONString(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
