package utils

import (
	"context"
	"net/http"
	"strings"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

const initialStreamInspectionLimit = 32

// InitialStreamChunkInspector classifies provider metadata that can appear
// before the first user-visible stream delta. Returning an error promotes the
// stream failure into synchronous retry/fallback orchestration. visible=true
// stops preflight buffering and resumes normal incremental delivery.
type InitialStreamChunkInspector func(*schemas.BifrostStreamChunk) (err *schemas.BifrostError, visible bool)

// CheckFirstStreamChunkForError reads the first chunk from a streaming channel to detect
// errors returned inside HTTP 200 SSE streams (e.g., providers that send rate limit
// errors as SSE events instead of HTTP 429). When an optional inspector is supplied,
// it may continue across initial metadata-only chunks until the first visible delta,
// an inspected error, or a bounded 32-chunk preflight limit. Reaching the limit
// fails the attempt with a retryable protocol error rather than exposing a stream
// whose remaining safety status has not been inspected.
//
// If the first chunk is an error, it drains the source channel in the background
// (so the provider goroutine can exit cleanly) and returns the error for synchronous
// handling, enabling retries and fallbacks. The returned drainDone channel is closed
// once the drain completes — callers must wait on it before releasing any resources
// (e.g., plugin pipelines) that the provider goroutine's postHookRunner may still reference.
//
// If the inspected prefix is valid data, it returns a wrapped channel that re-emits
// the buffered prefix followed by all remaining chunks from the source. drainDone
// is closed when the wrapper goroutine finishes forwarding the source stream.
//
// If the source channel is closed immediately (empty stream), it returns a
// nil channel with nil error for non-image requests. Empty image generation
// and edit streams return invalid_image_response so callers can retry or fall
// back instead of treating a zero-chunk provider response as success.
//
// The ctx argument cancels the background forwarding goroutine if the consumer
// abandons the returned wrapped channel. On ctx.Done the goroutine drains the
// source stream so the upstream provider's blocked send can exit cleanly.
func CheckFirstStreamChunkForError(
	ctx context.Context,
	requestType schemas.RequestType,
	stream chan *schemas.BifrostStreamChunk,
	inspectors ...InitialStreamChunkInspector,
) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	var inspector InitialStreamChunkInspector
	if len(inspectors) > 0 {
		inspector = inspectors[0]
	}
	buffered := make([]*schemas.BifrostStreamChunk, 0, 4)

	for {
		chunk, ok := <-stream
		if !ok {
			if len(buffered) > 0 {
				return replayInitialChunks(ctx, buffered, nil)
			}
			done := closedStreamSignal()
			if requestType == schemas.ImageGenerationStreamRequest || requestType == schemas.ImageEditStreamRequest {
				return nil, done, invalidImageStreamResponseError("upstream provider returned an empty image stream")
			}
			return nil, done, nil
		}

		if chunk.BifrostError != nil && chunk.BifrostError.Error != nil &&
			(chunk.BifrostError.Error.Message != "" || chunk.BifrostError.Error.Code != nil || chunk.BifrostError.Error.Type != nil) {
			return nil, drainStream(stream), chunk.BifrostError
		}
		visible := true
		if inspector != nil {
			if err, chunkVisible := inspector(chunk); err != nil {
				return nil, drainStream(stream), err
			} else {
				visible = chunkVisible
			}
		}
		if image := chunk.BifrostImageGenerationStreamResponse; image != nil &&
			(image.Type == schemas.ImageGenerationEventTypeCompleted || image.Type == schemas.ImageEditEventTypeCompleted) &&
			strings.TrimSpace(image.URL) == "" && strings.TrimSpace(image.B64JSON) == "" {
			return nil, drainStream(stream), invalidImageStreamResponseError("upstream provider completed image generation without url or b64_json")
		}

		buffered = append(buffered, chunk)
		if visible {
			return replayInitialChunks(ctx, buffered, stream)
		}
		if len(buffered) >= initialStreamInspectionLimit {
			return nil, drainStream(stream), streamPreflightLimitError()
		}
	}
}

func replayInitialChunks(ctx context.Context, buffered []*schemas.BifrostStreamChunk, stream chan *schemas.BifrostStreamChunk) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	capacity := max(len(buffered), 1)
	wrapped := make(chan *schemas.BifrostStreamChunk, capacity)
	for _, chunk := range buffered {
		wrapped <- chunk
	}
	done := make(chan struct{})
	if stream == nil {
		close(wrapped)
		close(done)
		return wrapped, done, nil
	}
	go func() {
		defer close(done)
		defer close(wrapped)
		for chunk := range stream {
			select {
			case wrapped <- chunk:
			case <-ctx.Done():
				for range stream {
				}
				return
			}
		}
	}()
	return wrapped, done, nil
}

func drainStream(stream chan *schemas.BifrostStreamChunk) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream {
		}
	}()
	return done
}

func closedStreamSignal() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func invalidImageStreamResponseError(message string) *schemas.BifrostError {
	statusCode := http.StatusBadGateway
	errorType := "invalid_image_response"
	allowFallbacks := true
	return &schemas.BifrostError{
		StatusCode:     &statusCode,
		Type:           &errorType,
		AllowFallbacks: &allowFallbacks,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Message: message,
		},
	}
}

func streamPreflightLimitError() *schemas.BifrostError {
	statusCode := http.StatusBadGateway
	errorType := "stream_preflight_limit_exceeded"
	allowFallbacks := true
	return &schemas.BifrostError{
		StatusCode:     &statusCode,
		Type:           &errorType,
		AllowFallbacks: &allowFallbacks,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Message: "upstream provider emitted too many metadata chunks before visible output",
		},
	}
}
