package webhooks

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
)

// eventEnvelope is the JSON body of every delivery request.
type eventEnvelope struct {
	Event     tables.WebhookEvent `json:"event"`
	CreatedAt time.Time           `json:"created_at"`
	Data      eventData           `json:"data"`
}

// eventData describes the async job the event refers to. All fields beyond
// JobID and Status are best-effort: they are omitted when the job row is no
// longer available at send time (see renderExpiredPayload).
type eventData struct {
	JobID           string                 `json:"job_id"`
	RequestType     schemas.RequestType    `json:"request_type,omitempty"`
	Status          schemas.AsyncJobStatus `json:"status"`
	StatusCode      int                    `json:"status_code,omitempty"`
	CreatedAt       *time.Time             `json:"created_at,omitempty"`
	CompletedAt     *time.Time             `json:"completed_at,omitempty"`
	ResultURL       string                 `json:"result_url,omitempty"`
	ResultExpiresAt *time.Time             `json:"result_expires_at,omitempty"`
	Response        json.RawMessage        `json:"response,omitempty"`
	ResponseOmitted bool                   `json:"response_omitted,omitempty"`
	Error           json.RawMessage        `json:"error,omitempty"`
	ErrorOmitted    bool                   `json:"error_omitted,omitempty"`
	ResultExpired   bool                   `json:"result_expired,omitempty"`
}

// asyncResultPathByType maps each async-capable request type to its
// result-fetch route. This is the canonical reverse of the submit-route
// mapping in the HTTP layer: result routes are typed
// (/v1/async/<type>/{job_id}); there is no generic job route.
var asyncResultPathByType = map[schemas.RequestType]string{
	schemas.TextCompletionRequest:  "/v1/async/completions",
	schemas.ChatCompletionRequest:  "/v1/async/chat/completions",
	schemas.ResponsesRequest:       "/v1/async/responses",
	schemas.EmbeddingRequest:       "/v1/async/embeddings",
	schemas.SpeechRequest:          "/v1/async/audio/speech",
	schemas.TranscriptionRequest:   "/v1/async/audio/transcriptions",
	schemas.ImageGenerationRequest: "/v1/async/images/generations",
	schemas.ImageEditRequest:       "/v1/async/images/edits",
	schemas.ImageVariationRequest:  "/v1/async/images/variations",
	schemas.RerankRequest:          "/v1/async/rerank",
	schemas.OCRRequest:             "/v1/async/ocr",
}

// AsyncResultPath returns the relative URL a caller can GET to fetch the
// result of the given job, or false when the request type has no async
// result route.
func AsyncResultPath(requestType schemas.RequestType, jobID string) (string, bool) {
	base, ok := asyncResultPathByType[requestType]
	if !ok {
		return "", false
	}
	return base + "/" + jobID, true
}

// EventForJobStatus maps a terminal async job status to the webhook event it
// fires; non-terminal statuses return false.
func EventForJobStatus(status schemas.AsyncJobStatus) (tables.WebhookEvent, bool) {
	switch status {
	case schemas.AsyncJobStatusCompleted:
		return tables.WebhookEventAsyncJobCompleted, true
	case schemas.AsyncJobStatusFailed:
		return tables.WebhookEventAsyncJobFailed, true
	default:
		return "", false
	}
}

// statusForEvent is the reverse of EventForJobStatus, used when the job row
// itself is gone and the status must be derived from the queued event.
func statusForEvent(event tables.WebhookEvent) schemas.AsyncJobStatus {
	if event == tables.WebhookEventAsyncJobFailed {
		return schemas.AsyncJobStatusFailed
	}
	return schemas.AsyncJobStatusCompleted
}

// renderPayload builds the delivery body for a live async job row. The
// job's response (on completion) or error (on failure) is inlined only when
// the endpoint opted in and the stored value fits within maxResponseBytes;
// an oversized value is dropped and flagged with response_omitted/
// error_omitted so the receiver knows to fetch it instead.
func renderPayload(job *logstore.AsyncJob, event tables.WebhookEvent, includeResponse bool, maxResponseBytes int, now time.Time) ([]byte, error) {
	data := eventData{
		JobID:           job.ID,
		RequestType:     job.RequestType,
		Status:          job.Status,
		StatusCode:      job.StatusCode,
		CreatedAt:       &job.CreatedAt,
		CompletedAt:     job.CompletedAt,
		ResultExpiresAt: job.ExpiresAt,
	}
	if url, ok := AsyncResultPath(job.RequestType, job.ID); ok {
		data.ResultURL = url
	}
	if includeResponse && job.Response != "" {
		if len(job.Response) <= maxResponseBytes {
			response, err := clientSafeWebhookPayload(job.Response)
			if err != nil {
				return nil, fmt.Errorf("sanitize webhook response: %w", err)
			}
			data.Response = response
		} else {
			data.ResponseOmitted = true
		}
	}
	if includeResponse && job.Error != "" {
		if len(job.Error) <= maxResponseBytes {
			errorPayload, err := clientSafeWebhookPayload(job.Error)
			if err != nil {
				return nil, fmt.Errorf("sanitize webhook error: %w", err)
			}
			data.Error = errorPayload
		} else {
			data.ErrorOmitted = true
		}
	}
	return json.Marshal(eventEnvelope{Event: event, CreatedAt: now, Data: data})
}

func clientSafeWebhookPayload(raw string) (json.RawMessage, error) {
	var payload interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(sanitizeWebhookValue(payload))
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func sanitizeWebhookValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		cleaned := make(map[string]interface{}, len(typed))
		originalModel := ""
		if extra, ok := typed["extra_fields"].(map[string]interface{}); ok {
			originalModel, _ = extra["original_model_requested"].(string)
		}
		for key, child := range typed {
			lowerKey := strings.ToLower(key)
			if lowerKey == "extra_fields" || lowerKey == "system_fingerprint" || lowerKey == "is_bifrost_error" || strings.Contains(lowerKey, "bifrost") {
				continue
			}
			cleaned[key] = sanitizeWebhookValue(child)
		}
		if originalModel != "" {
			if _, hasModel := cleaned["model"]; hasModel {
				cleaned["model"] = originalModel
			}
		}
		return cleaned
	case []interface{}:
		cleaned := make([]interface{}, len(typed))
		for index, child := range typed {
			cleaned[index] = sanitizeWebhookValue(child)
		}
		return cleaned
	case string:
		switch typed {
		case "bifrost_http_client_timeout":
			return "configured_provider_timeout"
		case "bifrost_context_deadline":
			return "request_context_deadline"
		}
		return strings.NewReplacer(
			"Bifrost HTTP client reached the configured provider timeout", "provider request reached the configured timeout",
			"request exceeded the Bifrost context deadline", "request exceeded the configured deadline",
			"Bifrost", "gateway",
			"bifrost", "gateway",
			"BIFROST", "GATEWAY",
		).Replace(typed)
	default:
		return value
	}
}

// renderExpiredPayload builds the degraded delivery body used when the async
// job row expired before this attempt fired. Only fields carried by the
// queue row survive; result_expired tells the receiver the outcome is known
// but the result — and any fetch-back — is gone. A result URL is not
// included: it cannot be built without the request type and would be dead
// anyway.
func renderExpiredPayload(job *tables.TableWebhookJob, now time.Time) ([]byte, error) {
	return json.Marshal(eventEnvelope{
		Event:     job.Event,
		CreatedAt: now,
		Data: eventData{
			JobID:         job.AsyncJobID,
			Status:        statusForEvent(job.Event),
			ResultExpired: true,
		},
	})
}
