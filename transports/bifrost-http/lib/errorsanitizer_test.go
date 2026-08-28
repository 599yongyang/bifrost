package lib

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TestSanitizeBifrostErrorForClientHidesInternalDetails(t *testing.T) {
	statusCode := fasthttp.StatusInternalServerError
	err := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Message: "failed to create customer: pq: duplicate key value violates unique constraint users_email_key",
			Error:   errors.New("goroutine 1 [running]:\nmain.handler\n\t/app/server.go:42"),
			Param:   "users_email_key",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized == err {
		t.Fatal("expected sanitizer to return a copy")
	}
	if sanitized.Error.Message != ClientSafeInternalErrorMessage {
		t.Fatalf("expected generic message, got %q", sanitized.Error.Message)
	}
	if sanitized.Error.Error != nil {
		t.Fatalf("expected sensitive nested error to be removed, got %v", sanitized.Error.Error)
	}
	if sanitized.Error.Param != nil {
		t.Fatalf("expected param to be removed, got %v", sanitized.Error.Param)
	}
	if err.Error.Message == ClientSafeInternalErrorMessage || err.Error.Error == nil || err.Error.Param == nil {
		t.Fatal("expected original error to remain unchanged")
	}
}

func TestSanitizeBifrostErrorForClientPreservesClientValidationMessage(t *testing.T) {
	statusCode := fasthttp.StatusBadRequest
	err := &schemas.BifrostError{
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "model is required",
			Error:   errors.New("missing model"),
			Param:   "model",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized.Error.Message != "model is required" {
		t.Fatalf("expected validation message to be preserved, got %q", sanitized.Error.Message)
	}
	if sanitized.Error.Param != "model" {
		t.Fatalf("expected param to be preserved, got %v", sanitized.Error.Param)
	}
	if sanitized.Error.Error == nil {
		t.Fatal("expected non-sensitive nested error to be preserved")
	}
}

func TestSanitizeBifrostErrorForClientPreservesNonSensitiveServerMessage(t *testing.T) {
	statusCode := fasthttp.StatusInternalServerError
	err := &schemas.BifrostError{
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: "failed to reload config",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)

	if sanitized.Error.Message != "failed to reload config" {
		t.Fatalf("expected non-sensitive server message to be preserved, got %q", sanitized.Error.Message)
	}
}

func TestSanitizeBifrostTimeoutErrorKeepsMetadataAndHidesCause(t *testing.T) {
	err := &schemas.BifrostError{
		Error: &schemas.ErrorField{Message: "upstream connection timed out", Error: errors.New("dial tcp secret.internal: timeout"), Param: "secret"},
		ExtraFields: schemas.BifrostErrorExtraFields{
			TimeoutSource:            schemas.TimeoutSourceUpstreamConnection,
			ConfiguredTimeoutSeconds: 600,
			ElapsedMS:                27_000,
			UpstreamResponseReceived: schemas.Ptr(false),
			RawRequest:               "sensitive request",
			RawResponse:              "sensitive upstream response",
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)
	if sanitized.Error.Error != nil || sanitized.Error.Param != nil {
		t.Fatal("timeout cause and param must not be returned to clients")
	}
	if sanitized.ExtraFields.RawRequest != nil || sanitized.ExtraFields.RawResponse != nil {
		t.Fatal("timeout payloads must not be returned to clients")
	}
	if sanitized.ExtraFields.TimeoutSource != schemas.TimeoutSourceUpstreamConnection || sanitized.ExtraFields.ConfiguredTimeoutSeconds != 600 {
		t.Fatal("safe structured timeout metadata must be preserved")
	}
	if err.Error.Error == nil || err.ExtraFields.RawRequest == nil {
		t.Fatal("sanitizer must not mutate the original error")
	}
}

func TestSanitizeBifrostTimeoutErrorReplacesUpstreamMessage(t *testing.T) {
	err := &schemas.BifrostError{
		Error: &schemas.ErrorField{
			Message: "gateway timeout contacting https://user:secret@proxy.internal?X-Amz-Signature=top-secret",
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			TimeoutSource:            schemas.TimeoutSourceUpstreamHTTP504,
			ConfiguredTimeoutSeconds: 600,
			ElapsedMS:                27_000,
			UpstreamResponseReceived: schemas.Ptr(true),
		},
	}

	sanitized := SanitizeBifrostErrorForClient(err)
	if sanitized.Error.Message != "upstream returned HTTP 504 Gateway Timeout" {
		t.Fatalf("expected canonical timeout message, got %q", sanitized.Error.Message)
	}
	if err.Error.Message == sanitized.Error.Message {
		t.Fatal("sanitizer must not mutate the original error")
	}
}

func TestClientErrorResponseOmitsGatewayAndRoutingIdentity(t *testing.T) {
	err := &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     schemas.Ptr(504),
		Error: &schemas.ErrorField{
			Message: schemas.TimeoutSourceBifrostHTTPClient.SafeMessage(),
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RoutingInfo: schemas.RoutingInfo{
				Provider: schemas.Bedrock,
				Model:    "internal-model",
				Key:      "secret-key-name",
			},
			Provider:                 schemas.Bedrock,
			OriginalModelRequested:   "moon1.0",
			ResolvedModelUsed:        "internal-model",
			RawRequest:               "secret request",
			RawResponse:              "secret response",
			TimeoutSource:            schemas.TimeoutSourceBifrostHTTPClient,
			ConfiguredTimeoutSeconds: 600,
			ElapsedMS:                600_000,
			UpstreamResponseReceived: schemas.Ptr(false),
		},
	}

	payload, marshalErr := json.Marshal(ClientErrorResponse(err))
	if marshalErr != nil {
		t.Fatalf("marshal client error: %v", marshalErr)
	}
	lower := strings.ToLower(string(payload))
	for _, forbidden := range []string{
		"bifrost", "is_bifrost_error", "routing_info", "internal-model",
		"secret-key-name", "secret request", "secret response", `"provider":"bedrock"`,
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("client error leaked %q: %s", forbidden, payload)
		}
	}
	for _, required := range []string{
		`"status_code":504`, `"timeout_source":"configured_provider_timeout"`,
		`"configured_timeout_seconds":600`, `"message":"provider request reached the configured timeout"`,
	} {
		if !strings.Contains(lower, required) {
			t.Fatalf("client error lost %q: %s", required, payload)
		}
	}
	if !err.IsBifrostError || err.ExtraFields.RoutingInfo.Key != "secret-key-name" {
		t.Fatal("client conversion mutated the internal error")
	}
}

func TestClientAsyncJobResponseRestoresPublicModelAndRemovesInternalMetadata(t *testing.T) {
	resp := &schemas.AsyncJobResponse{
		ID:        "job-1",
		RequestID: "request-1",
		Status:    schemas.AsyncJobStatusCompleted,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		Result: map[string]interface{}{
			"model":              "internal-provider-model",
			"system_fingerprint": "provider-fingerprint",
			"extra_fields": map[string]interface{}{
				"original_model_requested": "moon1.0",
				"provider":                 "bedrock",
				"routing_info": map[string]interface{}{
					"key": "secret-key-name",
				},
			},
			"nested": map[string]interface{}{
				"model": "internal-nested-model",
				"extra_fields": map[string]interface{}{
					"original_model_requested": "moon1.0",
				},
			},
		},
	}

	payload, err := json.Marshal(ClientAsyncJobResponse(resp))
	if err != nil {
		t.Fatalf("marshal async client response: %v", err)
	}
	lower := strings.ToLower(string(payload))
	for _, forbidden := range []string{"extra_fields", "system_fingerprint", "internal-provider-model", "internal-nested-model", "secret-key-name", "bedrock"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("async response leaked %q: %s", forbidden, payload)
		}
	}
	if strings.Count(lower, `"model":"moon1.0"`) != 2 {
		t.Fatalf("public model was not restored recursively: %s", payload)
	}
}

func TestClientErrorPayloadLeavesProviderNativeShapeUnchanged(t *testing.T) {
	providerPayload := map[string]interface{}{"error": map[string]interface{}{"type": "validation_error"}}
	if got := ClientErrorPayload(providerPayload); got == nil {
		t.Fatal("provider-native error was removed")
	} else if gotMap, ok := got.(map[string]interface{}); !ok || gotMap["error"] == nil {
		t.Fatalf("provider-native error shape changed: %#v", got)
	}
}
