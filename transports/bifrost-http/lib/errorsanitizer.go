package lib

import (
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
)

const ClientSafeInternalErrorMessage = "internal server error"

const (
	clientTimeoutSourceRequestContext     schemas.TimeoutSource = "request_context_deadline"
	clientTimeoutSourceConfiguredProvider schemas.TimeoutSource = "configured_provider_timeout"
)

type clientErrorExtraFields struct {
	Latency                  int64                         `json:"latency,omitempty"`
	TimeoutSource            schemas.TimeoutSource         `json:"timeout_source,omitempty"`
	ConfiguredTimeoutSeconds int                           `json:"configured_timeout_seconds,omitempty"`
	ElapsedMS                int64                         `json:"elapsed_ms,omitempty"`
	UpstreamResponseReceived *bool                         `json:"upstream_response_received,omitempty"`
	MCPAuthRequired          *schemas.MCPAuthRequiredError `json:"mcp_auth_required,omitempty"`
	BilledUsage              *schemas.BifrostLLMUsage      `json:"billed_usage,omitempty"`
}

// clientBifrostErrorResponse is the public representation of a BifrostError.
// IsBifrostError and provider/routing metadata are intentionally omitted.
type clientBifrostErrorResponse struct {
	EventID     *string                `json:"event_id,omitempty"`
	Type        *string                `json:"type,omitempty"`
	StatusCode  *int                   `json:"status_code,omitempty"`
	Error       *schemas.ErrorField    `json:"error"`
	ExtraFields clientErrorExtraFields `json:"extra_fields"`
}

type clientAsyncJobResponse struct {
	ID          string                 `json:"id"`
	RequestID   string                 `json:"request_id,omitempty"`
	Status      schemas.AsyncJobStatus `json:"status"`
	ExpiresAt   *time.Time             `json:"expires_at,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	StatusCode  int                    `json:"status_code,omitempty"`
	Result      interface{}            `json:"result,omitempty"`
	Error       interface{}            `json:"error,omitempty"`
}

// SanitizeBifrostErrorForClient returns a copy safe to serialize to API clients.
// Internal errors can contain stack traces or database details; keep those in logs only.
func SanitizeBifrostErrorForClient(err *schemas.BifrostError) *schemas.BifrostError {
	if err == nil {
		return nil
	}

	sanitized := *err
	sanitized.Type = redactGatewayIdentityPtr(err.Type)
	sanitized.ExtraFields = sanitizePublicErrorExtraFields(err.ExtraFields)
	if err.Error != nil {
		errorField := *err.Error
		if err.ExtraFields.TimeoutSource != "" {
			errorField.Message = clientSafeTimeoutMessage(err.ExtraFields.TimeoutSource)
			errorField.Error = nil
			errorField.Param = nil
		} else if shouldHideErrorDetails(err, err.Error) {
			errorField.Message = ClientSafeInternalErrorMessage
			errorField.Error = nil
			errorField.Param = nil
		}
		errorField.Type = redactGatewayIdentityPtr(errorField.Type)
		errorField.Code = redactGatewayIdentityPtr(errorField.Code)
		errorField.Message = redactGatewayIdentity(errorField.Message)
		if errorField.Error != nil && containsGatewayIdentity(errorField.Error.Error()) {
			errorField.Error = nil
		}
		sanitized.Error = &errorField
	}

	return &sanitized
}

// ClientErrorResponse converts an internal BifrostError into the public JSON shape.
func ClientErrorResponse(err *schemas.BifrostError) interface{} {
	if err == nil {
		return nil
	}
	err = SanitizeBifrostErrorForClient(err)
	return &clientBifrostErrorResponse{
		EventID:    err.EventID,
		Type:       err.Type,
		StatusCode: err.StatusCode,
		Error:      err.Error,
		ExtraFields: clientErrorExtraFields{
			Latency:                  err.ExtraFields.Latency,
			TimeoutSource:            err.ExtraFields.TimeoutSource,
			ConfiguredTimeoutSeconds: err.ExtraFields.ConfiguredTimeoutSeconds,
			ElapsedMS:                err.ExtraFields.ElapsedMS,
			UpstreamResponseReceived: err.ExtraFields.UpstreamResponseReceived,
			MCPAuthRequired:          err.ExtraFields.MCPAuthRequired,
			BilledUsage:              err.ExtraFields.BilledUsage,
		},
	}
}

// ClientErrorPayload wraps only BifrostError payloads. Provider-native error
// shapes pass through unchanged.
func ClientErrorPayload(payload interface{}) interface{} {
	switch typed := payload.(type) {
	case *schemas.BifrostError:
		return ClientErrorResponse(typed)
	case schemas.BifrostError:
		return ClientErrorResponse(&typed)
	default:
		return payload
	}
}

// ClientAsyncJobResponse applies the public error/result sanitization used by
// the async polling endpoints.
func ClientAsyncJobResponse(resp *schemas.AsyncJobResponse) interface{} {
	if resp == nil {
		return nil
	}
	return &clientAsyncJobResponse{
		ID:          resp.ID,
		RequestID:   resp.RequestID,
		Status:      resp.Status,
		ExpiresAt:   resp.ExpiresAt,
		CreatedAt:   resp.CreatedAt,
		CompletedAt: resp.CompletedAt,
		StatusCode:  resp.StatusCode,
		Result:      clientSafeResponsePayload(resp.Result),
		Error:       ClientErrorResponse(resp.Error),
	}
}

func sanitizePublicErrorExtraFields(extra schemas.BifrostErrorExtraFields) schemas.BifrostErrorExtraFields {
	return schemas.BifrostErrorExtraFields{
		RequestType:               extra.RequestType,
		MCPRequestType:            extra.MCPRequestType,
		ConvertedRequestType:      extra.ConvertedRequestType,
		DroppedCompatPluginParams: extra.DroppedCompatPluginParams,
		Latency:                   extra.Latency,
		TimeoutSource:             clientSafeTimeoutSource(extra.TimeoutSource),
		ConfiguredTimeoutSeconds:  extra.ConfiguredTimeoutSeconds,
		ElapsedMS:                 extra.ElapsedMS,
		UpstreamResponseReceived:  extra.UpstreamResponseReceived,
		MCPAuthRequired:           extra.MCPAuthRequired,
		BilledUsage:               extra.BilledUsage,
	}
}

func clientSafeTimeoutMessage(source schemas.TimeoutSource) string {
	switch source {
	case schemas.TimeoutSourceBifrostContextDeadline, clientTimeoutSourceRequestContext:
		return "request exceeded the configured deadline"
	case schemas.TimeoutSourceBifrostHTTPClient, clientTimeoutSourceConfiguredProvider:
		return "provider request reached the configured timeout"
	default:
		return source.SafeMessage()
	}
}

func clientSafeTimeoutSource(source schemas.TimeoutSource) schemas.TimeoutSource {
	switch source {
	case schemas.TimeoutSourceBifrostContextDeadline:
		return clientTimeoutSourceRequestContext
	case schemas.TimeoutSourceBifrostHTTPClient:
		return clientTimeoutSourceConfiguredProvider
	default:
		return source
	}
}

func clientSafeResponsePayload(payload interface{}) interface{} {
	if payload == nil {
		return nil
	}

	raw, err := schemas.MarshalSorted(payload)
	if err != nil {
		return nil
	}

	var decoded interface{}
	if err := sonic.Unmarshal(raw, &decoded); err != nil {
		return nil
	}

	return sanitizeClientResponseValue(decoded)
}

func sanitizeClientResponseValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		cleaned := make(map[string]interface{}, len(typed))
		restoredModel := extractOriginalModelRequested(typed)
		for key, child := range typed {
			switch strings.ToLower(key) {
			case "extra_fields", "system_fingerprint", "is_bifrost_error":
				continue
			}
			cleaned[key] = sanitizeClientResponseValue(child)
		}
		if restoredModel != "" {
			cleaned["model"] = restoredModel
		}
		return cleaned
	case []interface{}:
		cleaned := make([]interface{}, len(typed))
		for i, child := range typed {
			cleaned[i] = sanitizeClientResponseValue(child)
		}
		return cleaned
	default:
		return value
	}
}

func extractOriginalModelRequested(value map[string]interface{}) string {
	extraFields, ok := value["extra_fields"].(map[string]interface{})
	if !ok {
		return ""
	}
	model, _ := extraFields["original_model_requested"].(string)
	return model
}

func redactGatewayIdentityPtr(value *string) *string {
	if value == nil {
		return nil
	}
	redacted := redactGatewayIdentity(*value)
	return &redacted
}

func redactGatewayIdentity(value string) string {
	return strings.NewReplacer(
		"Bifrost", "gateway",
		"bifrost", "gateway",
		"BIFROST", "GATEWAY",
	).Replace(value)
}

func containsGatewayIdentity(value string) bool {
	return strings.Contains(strings.ToLower(value), "bifrost")
}

func shouldHideErrorDetails(_ *schemas.BifrostError, field *schemas.ErrorField) bool {
	message := field.Message
	if field.Error != nil {
		message += " " + field.Error.Error()
	}

	return containsStackTrace(message) || containsSQLDetails(message)
}

func containsStackTrace(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "stack trace") ||
		strings.Contains(lower, "traceback (most recent call last)") ||
		strings.Contains(lower, "runtime/debug.stack") ||
		strings.Contains(lower, "goroutine ") ||
		strings.Contains(lower, "panic:") ||
		strings.Contains(lower, ".go:")
}

func containsSQLDetails(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "sqlstate") ||
		strings.Contains(lower, "sql:") ||
		strings.Contains(lower, "pq:") ||
		strings.Contains(lower, "pgx:") ||
		strings.Contains(lower, "duplicate key value violates") ||
		strings.Contains(lower, "violates foreign key constraint") ||
		strings.Contains(lower, "violates unique constraint") ||
		strings.Contains(lower, "syntax error at or near") ||
		strings.Contains(lower, "relation does not exist") ||
		strings.Contains(lower, "database/sql")
}
