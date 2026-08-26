package lib

import (
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const ClientSafeInternalErrorMessage = "internal server error"

const (
	clientTimeoutSourceRequestContext     schemas.TimeoutSource = "request_context_deadline"
	clientTimeoutSourceConfiguredProvider schemas.TimeoutSource = "configured_provider_timeout"
)

// clientBifrostErrorResponse is the public representation of a BifrostError.
// IsBifrostError is intentionally omitted: it is an internal routing detail and
// exposing its JSON field leaks the gateway implementation to API callers.
type clientBifrostErrorResponse struct {
	EventID     *string                         `json:"event_id,omitempty"`
	Type        *string                         `json:"type,omitempty"`
	StatusCode  *int                            `json:"status_code,omitempty"`
	Error       *schemas.ErrorField             `json:"error"`
	ExtraFields schemas.BifrostErrorExtraFields `json:"extra_fields"`
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
	// Upstream IDs are an operator-only log correlation aid. Keep them out of
	// public API error bodies even when the rest of the error is client-safe.
	sanitized.ExtraFields.UpstreamRequestID = ""
	sanitized.ExtraFields.UpstreamResponseHeaders = nil
	if err.ExtraFields.TimeoutSource != "" {
		sanitized.ExtraFields.RawRequest = nil
		sanitized.ExtraFields.RawResponse = nil
	}
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
		errorField.Message = redactGatewayIdentity(errorField.Message)
		if errorField.Error != nil && containsGatewayIdentity(errorField.Error.Error()) {
			errorField.Error = nil
		}
		sanitized.Error = &errorField
	}
	sanitized.ExtraFields.TimeoutSource = clientSafeTimeoutSource(err.ExtraFields.TimeoutSource)

	return &sanitized
}

// ClientErrorResponse sanitizes an internal error and converts it into its public JSON shape.
// Internal code should continue using BifrostError so retry/fallback decisions
// retain IsBifrostError; only the final serialized response uses this type.
func ClientErrorResponse(err *schemas.BifrostError) interface{} {
	if err == nil {
		return nil
	}
	err = SanitizeBifrostErrorForClient(err)
	return &clientBifrostErrorResponse{
		EventID:     err.EventID,
		Type:        err.Type,
		StatusCode:  err.StatusCode,
		Error:       err.Error,
		ExtraFields: err.ExtraFields,
	}
}

// ClientErrorPayload removes internal gateway identity when an integration's
// error converter returns BifrostError directly. Provider-native error payloads
// and raw SSE strings pass through unchanged.
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

// ClientAsyncJobResponse applies the same public error shape to async polling
// responses while preserving all non-error job fields.
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
		Result:      resp.Result,
		Error:       ClientErrorResponse(resp.Error),
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
