package utils

import (
	"context"
	"net/http"
	"strings"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

// CheckFirstStreamChunkForError reads the first chunk from a streaming channel to detect
// errors returned inside HTTP 200 SSE streams (e.g., providers that send rate limit
// errors as SSE events instead of HTTP 429).
//
// If the first chunk is an error, it drains the source channel in the background
// (so the provider goroutine can exit cleanly) and returns the error for synchronous
// handling, enabling retries and fallbacks. The returned drainDone channel is closed
// once the drain completes — callers must wait on it before releasing any resources
// (e.g., plugin pipelines) that the provider goroutine's postHookRunner may still reference.
//
// If the first chunk is valid data, it returns a wrapped channel that re-emits
// the first chunk followed by all remaining chunks from the source. drainDone is
// closed when the wrapper goroutine finishes forwarding the source stream.
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
) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	firstChunk, ok := <-stream
	if !ok {
		// Channel closed immediately (empty stream) — return nil so callers
		// can distinguish this from a live stream channel.
		done := make(chan struct{})
		close(done)
		if requestType == schemas.ImageGenerationStreamRequest || requestType == schemas.ImageEditStreamRequest {
			return nil, done, invalidImageStreamResponseError("upstream provider returned an empty image stream")
		}
		return nil, done, nil
	}

	// Check if first chunk is an error
	if firstChunk.BifrostError != nil && firstChunk.BifrostError.Error != nil &&
		(firstChunk.BifrostError.Error.Message != "" || firstChunk.BifrostError.Error.Code != nil || firstChunk.BifrostError.Error.Type != nil) {
		// Drain source channel to let the provider goroutine exit cleanly
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range stream {
			}
		}()
		return nil, done, firstChunk.BifrostError
	}

	if image := firstChunk.BifrostImageGenerationStreamResponse; image != nil &&
		(image.Type == schemas.ImageGenerationEventTypeCompleted || image.Type == schemas.ImageEditEventTypeCompleted) &&
		strings.TrimSpace(image.URL) == "" && strings.TrimSpace(image.B64JSON) == "" {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range stream {
			}
		}()
		return nil, done, invalidImageStreamResponseError("upstream provider completed image generation without url or b64_json")
	}

	// First chunk is valid data — wrap channel to re-inject it
	done := make(chan struct{})
	wrapped := make(chan *schemas.BifrostStreamChunk, max(cap(stream), 1))
	wrapped <- firstChunk
	go func() {
		defer close(done)
		defer close(wrapped)
		for chunk := range stream {
			select {
			case wrapped <- chunk:
			case <-ctx.Done():
				// Consumer abandoned the wrapped channel. Drain the source so the
				// provider's blocked send unblocks and its goroutine can exit.
				for range stream {
				}
				return
			}
		}
	}()
	return wrapped, done, nil
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
