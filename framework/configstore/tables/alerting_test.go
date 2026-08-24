package tables

import (
	"reflect"
	"testing"
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
