package handlers

import (
	"reflect"
	"testing"
)

func TestMergeAlertChannelConfigPreservesRedactedSecretsAndAppliesPartialChanges(t *testing.T) {
	existing := map[string]any{
		"url":     "https://example.com/hook",
		"headers": map[string]any{"X-API-Key": "old-secret", "X-Tenant": "old-tenant"},
	}
	incoming := map[string]any{
		"url":     "***redacted***",
		"headers": map[string]any{"X-API-Key": "***redacted***", "X-Tenant": "new-tenant"},
	}
	want := map[string]any{
		"url":     "https://example.com/hook",
		"headers": map[string]any{"X-API-Key": "old-secret", "X-Tenant": "new-tenant"},
	}
	if got := mergeAlertChannelConfig(existing, incoming); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected merged config: %#v", got)
	}
}

func TestMergeAlertChannelConfigTreatsEmptyConfigAsNoSecretChange(t *testing.T) {
	existing := map[string]any{"routing_key": "secret"}
	if got := mergeAlertChannelConfig(existing, map[string]any{}); !reflect.DeepEqual(got, existing) {
		t.Fatalf("unexpected merged config: %#v", got)
	}
}
