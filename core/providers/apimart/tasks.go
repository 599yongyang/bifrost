package apimart

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

const (
	apimartPollRequestAttempts = 3
	apimartPollRetryDelay      = 500 * time.Millisecond
)

func (provider *APIMartProvider) submitTask(ctx *schemas.BifrostContext, key schemas.Key, requestBody []byte) (string, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/v1/images/generations"))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}
	req.SetBody(requestBody)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return "", providerUtils.EnrichError(ctx, bifrostErr, requestBody, nil, provider.sendBackRawRequest, false, latency)
	}
	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return "", providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), requestBody, nil, provider.sendBackRawRequest, false, latency)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		var envelope APIMartTaskResponse
		_ = decodeAPIMartResponse(body, &envelope)
		return "", providerUtils.EnrichError(ctx, newAPIMartError(resp.StatusCode(), envelope.Error, fmt.Sprintf("APIMart task submission failed with HTTP %d", resp.StatusCode())), requestBody, body, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	var submission APIMartSubmitResponse
	if err := decodeAPIMartResponse(body, &submission); err != nil {
		return "", providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err), requestBody, nil, provider.sendBackRawRequest, false, latency)
	}
	if submission.Code != 0 && submission.Code != http.StatusOK {
		return "", providerUtils.EnrichError(ctx, newAPIMartError(submission.Code, submission.Error, "APIMart task submission failed"), requestBody, body, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}
	if len(submission.Data) == 0 || submission.Data[0].TaskID == "" {
		return "", providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, fmt.Errorf("APIMart submission response did not contain task_id")), requestBody, nil, provider.sendBackRawRequest, false, latency)
	}
	return submission.Data[0].TaskID, nil
}

func (provider *APIMartProvider) pollTask(ctx *schemas.BifrostContext, key schemas.Key, taskID string) (*APIMartTask, int, *schemas.BifrostError) {
	pollCtx, cancel := schemas.NewBifrostContextWithTimeout(ctx, time.Duration(provider.networkConfig.DefaultRequestTimeoutInSeconds)*time.Second)
	defer cancel()

	if err := waitForAPIMartPoll(pollCtx, provider.initialPollDelay); err != nil {
		return nil, 0, apimartContextError(err)
	}
	polls := 0
	for {
		polls++
		task, bifrostErr := provider.retrieveTask(pollCtx, key, taskID)
		if bifrostErr != nil {
			return nil, polls, bifrostErr
		}
		status := strings.ToLower(task.Status)
		switch status {
		case "completed":
			return task, polls, nil
		case "failed", "cancelled":
			return task, polls, newAPIMartTaskError(task)
		case "submitted", "pending", "processing", "in_progress":
		default:
			return task, polls, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, fmt.Errorf("unknown APIMart task status %q", task.Status))
		}
		if err := waitForAPIMartPoll(pollCtx, provider.pollInterval); err != nil {
			return nil, polls, apimartContextError(err)
		}
	}
}

func waitForAPIMartPoll(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func apimartContextError(err error) *schemas.BifrostError {
	if err == context.DeadlineExceeded {
		bifrostErr := providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err)
		errorType := schemas.RequestTimedOut
		bifrostErr.Type = &errorType
		return bifrostErr
	}
	statusCode := 499
	errorType := schemas.RequestCancelled
	allowFallbacks := false
	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Type:           &errorType,
		AllowFallbacks: &allowFallbacks,
		Error:          &schemas.ErrorField{Type: &errorType, Code: &errorType, Message: schemas.ErrRequestCancelled},
	}
}

func (provider *APIMartProvider) retrieveTask(ctx *schemas.BifrostContext, key schemas.Key, taskID string) (*APIMartTask, *schemas.BifrostError) {
	var lastErr *schemas.BifrostError
	for attempt := 1; attempt <= apimartPollRequestAttempts; attempt++ {
		task, bifrostErr := provider.retrieveTaskOnce(ctx, key, taskID)
		if bifrostErr == nil {
			return task, nil
		}
		lastErr = bifrostErr
		if !isRetryableAPIMartPollError(bifrostErr) || attempt == apimartPollRequestAttempts {
			break
		}
		if err := waitForAPIMartPoll(ctx, apimartPollRetryDelay); err != nil {
			return nil, apimartContextError(err)
		}
	}
	return nil, lastErr
}

func (provider *APIMartProvider) retrieveTaskOnce(ctx *schemas.BifrostContext, key schemas.Key, taskID string) (*APIMartTask, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/v1/tasks/"+taskID))
	req.Header.SetMethod(http.MethodGet)
	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}
	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), latency)
	}
	var envelope APIMartTaskResponse
	if err := decodeAPIMartResponse(body, &envelope); err != nil {
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err), latency)
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return nil, providerUtils.SetErrorLatency(newAPIMartError(resp.StatusCode(), envelope.Error, fmt.Sprintf("APIMart task query failed with HTTP %d", resp.StatusCode())), latency)
	}
	if envelope.Code != 0 && envelope.Code != http.StatusOK {
		return nil, providerUtils.SetErrorLatency(newAPIMartError(envelope.Code, envelope.Error, "APIMart task query failed"), latency)
	}
	if envelope.Data == nil {
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, fmt.Errorf("APIMart task response did not contain data")), latency)
	}
	if envelope.Data.Error == nil && envelope.Error != nil {
		envelope.Data.Error = envelope.Error
	}
	return envelope.Data, nil
}

func isRetryableAPIMartPollError(err *schemas.BifrostError) bool {
	if err == nil {
		return false
	}
	if err.StatusCode == nil {
		return true
	}
	switch *err.StatusCode {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}
