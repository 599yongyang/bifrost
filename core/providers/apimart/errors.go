package apimart

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

func newAPIMartError(statusCode int, detail *APIMartErrorDetail, fallbackMessage string) *schemas.BifrostError {
	code, upstreamType, message := APIMartErrorFields(detail)
	if message == "" {
		message = fallbackMessage
	}
	combined := strings.ToLower(strings.Join([]string{code, upstreamType, message}, " "))
	errorType := classifyAPIMartErrorType(statusCode, combined)
	if code == "" {
		code = errorType
	}
	allowFallbacks := errorType != "invalid_request_error"
	return &schemas.BifrostError{
		IsBifrostError: false,
		StatusCode:     &statusCode,
		Type:           &errorType,
		AllowFallbacks: &allowFallbacks,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Code:    &code,
			Message: message,
		},
	}
}

func APIMartErrorFields(detail *APIMartErrorDetail) (code, errorType, message string) {
	if detail == nil {
		return "", "", ""
	}
	switch value := detail.Code.(type) {
	case string:
		code = value
	case float64:
		code = strconv.FormatInt(int64(value), 10)
	case int:
		code = strconv.Itoa(value)
	case nil:
	default:
		code = fmt.Sprint(value)
	}
	return code, detail.Type, strings.TrimSpace(detail.Message)
}

func classifyAPIMartErrorType(statusCode int, combined string) string {
	if containsAny(combined, "content_moderation", "content policy", "content_policy", "safety policy", "安全策略", "内容安全", "内容政策") {
		return "content_policy"
	}
	if containsAny(combined, "build_request_failed", "invalid_request", "invalid size", "size 不合法", "参数错误") {
		return "invalid_request_error"
	}
	switch statusCode {
	case 400:
		return "invalid_request_error"
	case 401:
		return "authentication_error"
	case 402:
		return "billing_error"
	case 403:
		return "permission_error"
	case 429:
		return "rate_limit_error"
	case 502, 503, 504:
		return "service_unavailable"
	default:
		if statusCode >= 500 {
			return "server_error"
		}
		return "provider_error"
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func newAPIMartTaskError(task *APIMartTask) *schemas.BifrostError {
	if task == nil {
		bifrostErr := newAPIMartError(502, nil, "APIMart task failed without task details")
		bifrostErr.IsBifrostError = true // terminal task result: never resubmit the generation
		return bifrostErr
	}
	if strings.EqualFold(task.Status, "cancelled") {
		detail := task.Error
		if detail == nil {
			detail = &APIMartErrorDetail{Code: "task_cancelled", Type: "task_cancelled", Message: "APIMart task was cancelled"}
		}
		bifrostErr := newAPIMartError(409, detail, "APIMart task was cancelled")
		cancelledType := "task_cancelled"
		bifrostErr.Type = &cancelledType
		bifrostErr.Error.Type = &cancelledType
		bifrostErr.IsBifrostError = true // terminal task result: fall back, but do not retry this provider
		return bifrostErr
	}
	code, upstreamType, message := APIMartErrorFields(task.Error)
	classified := classifyAPIMartErrorType(502, strings.ToLower(strings.Join([]string{code, upstreamType, message}, " ")))
	statusCode := 502
	if classified == "content_policy" || classified == "invalid_request_error" {
		statusCode = 400
	}
	bifrostErr := newAPIMartError(statusCode, task.Error, "APIMart image task failed")
	bifrostErr.IsBifrostError = true // terminal task result: fall back, but do not create another task
	return bifrostErr
}
