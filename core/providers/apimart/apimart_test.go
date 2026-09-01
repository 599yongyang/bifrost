package apimart

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type testLogger struct{}

func (testLogger) Debug(string, ...any)                   {}
func (testLogger) Info(string, ...any)                    {}
func (testLogger) Warn(string, ...any)                    {}
func (testLogger) Error(string, ...any)                   {}
func (testLogger) Fatal(string, ...any)                   {}
func (testLogger) SetLevel(schemas.LogLevel)              {}
func (testLogger) SetOutputType(schemas.LoggerOutputType) {}
func (testLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

var tinyPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52}

func testKey() schemas.Key {
	return schemas.Key{Value: *schemas.NewSecretVar("test-key")}
}

func newTestProvider(t *testing.T, handler http.Handler) (*APIMartProvider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	provider, err := NewAPIMartProvider(&schemas.ProviderConfig{NetworkConfig: schemas.NetworkConfig{
		BaseURL: server.URL, AllowPrivateNetwork: true, DefaultRequestTimeoutInSeconds: 2,
	}}, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	provider.initialPollDelay = 0
	provider.pollInterval = time.Millisecond
	provider.downloadRetryDelay = time.Millisecond
	provider.downloadImage = func(_ context.Context, _ string, _ int64) (string, int64, error) {
		return base64.StdEncoding.EncodeToString(tinyPNG), int64(len(tinyPNG)), nil
	}
	return provider, server
}

func completedTask(created int64, urls ...string) string {
	images := make([]map[string]interface{}, len(urls))
	for i, url := range urls {
		images[i] = map[string]interface{}{"url": []string{url}}
	}
	body, _ := json.Marshal(map[string]interface{}{"code": 200, "data": map[string]interface{}{
		"id": "task-secret", "status": "completed", "completed": created,
		"result": map[string]interface{}{"images": images},
	}})
	return string(body)
}

func TestImageGenerationPollsAndReturnsURLWithoutTaskMetadata(t *testing.T) {
	statuses := []string{"submitted", "processing", "completed"}
	var polls atomic.Int32
	var submitted APIMartImageRequest
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&submitted)
			_, _ = w.Write([]byte(`{"code":200,"data":[{"status":"submitted","task_id":"task-secret"}]}`))
			return
		}
		index := int(polls.Add(1)) - 1
		status := statuses[index]
		if status == "completed" {
			_, _ = w.Write([]byte(completedTask(1776748726, "https://images.example/one.png", "https://images.example/two.png")))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"id":"task-secret","status":"` + status + `"}}`))
	}))
	defer server.Close()

	format := "url"
	size := "1536x1024"
	n := 2
	response, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{
		Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"},
		Params: &schemas.ImageGenerationParameters{N: &n, Size: &size, ResponseFormat: &format},
	})
	if bifrostErr != nil {
		t.Fatal(bifrostErr)
	}
	if submitted.Size == nil || *submitted.Size != size || submitted.N == nil || *submitted.N != 2 {
		t.Fatalf("unexpected submission: %#v", submitted)
	}
	if len(response.Data) != 2 || response.Data[0].URL != "https://images.example/one.png" || response.Data[1].URL != "https://images.example/two.png" {
		t.Fatalf("unexpected image data: %#v", response.Data)
	}
	if response.ExtraFields.RawRequest != nil || response.ExtraFields.RawResponse != nil {
		t.Fatalf("raw fields must be absent by default: %#v", response.ExtraFields)
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), "task-secret") || strings.Contains(string(encoded), "processing") {
		t.Fatalf("task metadata leaked: %s", encoded)
	}
}

func TestPendingAndInProgressAreNonTerminal(t *testing.T) {
	statuses := []string{"pending", "in_progress", "completed"}
	var polls atomic.Int32
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"code":200,"data":[{"status":"submitted","task_id":"task"}]}`))
			return
		}
		status := statuses[int(polls.Add(1))-1]
		if status == "completed" {
			_, _ = w.Write([]byte(completedTask(1, "https://images.example/one.png")))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"status":"` + status + `"}}`))
	}))
	defer server.Close()
	format := "url"
	_, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{
		Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}, Params: &schemas.ImageGenerationParameters{ResponseFormat: &format},
	})
	if bifrostErr != nil || polls.Load() != 3 {
		t.Fatalf("error=%v polls=%d", bifrostErr, polls.Load())
	}
}

func TestPollGETRetriesTransientFailureWithoutResubmitting(t *testing.T) {
	var submissions atomic.Int32
	var gets atomic.Int32
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			submissions.Add(1)
			_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"task"}]}`))
			return
		}
		if gets.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"type":"service_unavailable","message":"temporary"}}`))
			return
		}
		_, _ = w.Write([]byte(completedTask(1, "https://images.example/one.png")))
	}))
	defer server.Close()
	format := "url"
	_, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{
		Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}, Params: &schemas.ImageGenerationParameters{ResponseFormat: &format},
	})
	if bifrostErr != nil || submissions.Load() != 1 || gets.Load() != 3 {
		t.Fatalf("error=%v submissions=%d gets=%d", bifrostErr, submissions.Load(), gets.Load())
	}
}

func TestB64JSONResponseContainsNoURL(t *testing.T) {
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"task"}]}`))
			return
		}
		_, _ = w.Write([]byte(completedTask(1, "https://images.example/one.png")))
	}))
	defer server.Close()
	format := "b64_json"
	response, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{
		Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}, Params: &schemas.ImageGenerationParameters{ResponseFormat: &format},
	})
	if bifrostErr != nil {
		t.Fatal(bifrostErr)
	}
	if len(response.Data) != 1 || response.Data[0].B64JSON == "" || response.Data[0].URL != "" {
		t.Fatalf("unexpected b64 response: %#v", response.Data)
	}
}

func TestImageGenerationInputImagesURLAndDataURI(t *testing.T) {
	dataURI, err := APIMartImageBytesToDataURI(tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	request, err := ToAPIMartImageGenerationRequest(&schemas.BifrostImageGenerationRequest{
		Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "edit"},
		Params: &schemas.ImageGenerationParameters{InputImages: []string{"https://203.0.113.1/source.png", dataURI}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.ImageURLs) != 2 || request.ImageURLs[0] != "https://203.0.113.1/source.png" || request.ImageURLs[1] != dataURI {
		t.Fatalf("unexpected image URLs: %#v", request.ImageURLs)
	}
}

func TestSizeValidationRejectsRatiosAndNeverSendsResolution(t *testing.T) {
	for _, invalidSize := range []string{"16:9", "auto", "2048", "0x2560", "2048x0", "2048x2560x1"} {
		_, err := ToAPIMartImageGenerationRequest(&schemas.BifrostImageGenerationRequest{
			Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}, Params: &schemas.ImageGenerationParameters{Size: &invalidSize},
		})
		if err == nil {
			t.Fatalf("expected size %q to be rejected", invalidSize)
		}
	}
	for _, size := range []string{"1024x1024", "2048x2560", "1881x836"} {
		request, err := ToAPIMartImageGenerationRequest(&schemas.BifrostImageGenerationRequest{
			Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"},
			Params: &schemas.ImageGenerationParameters{Size: &size, ExtraParams: map[string]interface{}{"resolution": "4k"}},
		})
		if err != nil {
			t.Fatalf("pixel size %q was rejected: %v", size, err)
		}
		body, _ := json.Marshal(request)
		if request.Size == nil || *request.Size != size || strings.Contains(string(body), "resolution") {
			t.Fatalf("size was not passed through cleanly: %s", body)
		}
	}
}

func TestPrivateInputImageURLIsBlockedBeforeSubmission(t *testing.T) {
	provider := &APIMartProvider{logger: testLogger{}}
	_, bifrostErr := provider.executeImageTask(
		schemas.NewBifrostContext(context.Background(), time.Time{}),
		testKey(),
		"gpt-image-2",
		"url",
		&APIMartImageRequest{Model: "gpt-image-2", Prompt: "edit", ImageURLs: []string{"http://127.0.0.1/private.png"}},
	)
	if bifrostErr == nil || bifrostErr.Error == nil || bifrostErr.Error.Code == nil || *bifrostErr.Error.Code != "invalid_image_url" {
		t.Fatalf("unexpected error: %v", bifrostErr)
	}
}

func TestImageEditConvertsMultipartBytesAndURLInOrder(t *testing.T) {
	request, err := ToAPIMartImageEditRequest(&schemas.BifrostImageEditRequest{
		Model: "gpt-image-2", Input: &schemas.ImageEditInput{Prompt: "edit", Images: []schemas.ImageInput{
			{Image: tinyPNG}, {URL: "https://203.0.113.2/source.png"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.ImageURLs) != 2 || !strings.HasPrefix(request.ImageURLs[0], "data:image/png;base64,") || request.ImageURLs[1] != "https://203.0.113.2/source.png" {
		t.Fatalf("unexpected image URLs: %#v", request.ImageURLs)
	}
}

func TestTaskFailedAndCancelled(t *testing.T) {
	for _, test := range []struct {
		status        string
		errorType     string
		statusCode    int
		allowFallback bool
	}{
		{status: "failed", errorType: "content_policy", statusCode: 400, allowFallback: true},
		{status: "cancelled", errorType: "task_cancelled", statusCode: 409, allowFallback: true},
	} {
		t.Run(test.status, func(t *testing.T) {
			provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"task"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"code":200,"data":{"status":"` + test.status + `","error":{"code":"content_moderation","type":"policy_error","message":"content blocked"}}}`))
			}))
			defer server.Close()
			_, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}})
			if bifrostErr == nil || !bifrostErr.IsBifrostError || bifrostErr.Type == nil || *bifrostErr.Type != test.errorType || bifrostErr.StatusCode == nil || *bifrostErr.StatusCode != test.statusCode || bifrostErr.AllowFallbacks == nil || *bifrostErr.AllowFallbacks != test.allowFallback {
				t.Fatalf("unexpected error: %v", bifrostErr)
			}
		})
	}
}

func TestTaskBuildRequestFailureCannotRetryOrFallback(t *testing.T) {
	bifrostErr := newAPIMartTaskError(&APIMartTask{Status: "failed", Error: &APIMartErrorDetail{
		Code: "build_request_failed", Type: "invalid_request_error", Message: "invalid size",
	}})
	if !bifrostErr.IsBifrostError || bifrostErr.StatusCode == nil || *bifrostErr.StatusCode != http.StatusBadRequest || bifrostErr.AllowFallbacks == nil || *bifrostErr.AllowFallbacks {
		t.Fatalf("invalid task error must be terminal: %v", bifrostErr)
	}
}

func TestSubmissionHTTPErrorMapping(t *testing.T) {
	tests := map[int]string{400: "invalid_request_error", 401: "authentication_error", 402: "billing_error", 403: "permission_error", 429: "rate_limit_error", 500: "server_error", 502: "service_unavailable", 503: "service_unavailable"}
	for status, wantType := range tests {
		t.Run(http.StatusText(status), func(t *testing.T) {
			provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"code":"upstream_error","message":"failed"}}`))
			}))
			defer server.Close()
			_, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}})
			if bifrostErr == nil || bifrostErr.Type == nil || *bifrostErr.Type != wantType {
				t.Fatalf("status %d: %v", status, bifrostErr)
			}
		})
	}
}

func TestB64DownloadFailureDoesNotResubmit(t *testing.T) {
	var submissions atomic.Int32
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			submissions.Add(1)
			_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"task"}]}`))
			return
		}
		_, _ = w.Write([]byte(completedTask(1, "https://images.example/signed.png?sig=secret")))
	}))
	defer server.Close()
	var downloads atomic.Int32
	provider.downloadImage = func(context.Context, string, int64) (string, int64, error) {
		downloads.Add(1)
		return "", 0, errors.New("temporary network failure")
	}
	_, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}})
	if bifrostErr == nil || submissions.Load() != 1 || downloads.Load() != int32(provider.downloadAttempts) {
		t.Fatalf("error=%v submissions=%d downloads=%d", bifrostErr, submissions.Load(), downloads.Load())
	}
	if strings.Contains(bifrostErr.GetErrorString(), "sig=secret") {
		t.Fatalf("signed query leaked: %s", bifrostErr.GetErrorString())
	}
}

func TestB64OutputHasTotalEncodedResponseLimit(t *testing.T) {
	provider := &APIMartProvider{
		maxImageBytes:      1024,
		maxTotalBytes:      7,
		downloadAttempts:   1,
		downloadRetryDelay: time.Millisecond,
		downloadImage: func(context.Context, string, int64) (string, int64, error) {
			return "MTIzNDU=", 5, nil
		},
	}
	_, bifrostErr := provider.buildImageData(context.Background(), []string{"https://images.example/one.png"}, "b64_json")
	if bifrostErr == nil || !strings.Contains(bifrostErr.GetErrorString(), "total encoded response size") {
		t.Fatalf("unexpected error: %v", bifrostErr)
	}
}

func TestB64DownloadPreservesCancellation(t *testing.T) {
	provider := &APIMartProvider{
		maxImageBytes:    1024,
		maxTotalBytes:    4096,
		downloadAttempts: 1,
		downloadImage: func(context.Context, string, int64) (string, int64, error) {
			return "", 0, context.Canceled
		},
	}
	_, bifrostErr := provider.buildImageData(context.Background(), []string{"https://images.example/one.png"}, "b64_json")
	if bifrostErr == nil || bifrostErr.Type == nil || *bifrostErr.Type != schemas.RequestCancelled || bifrostErr.StatusCode == nil || *bifrostErr.StatusCode != 499 {
		t.Fatalf("unexpected cancellation error: %v", bifrostErr)
	}
}

func TestImageGenerationPreservesDownloadCancellationAfterEnrichment(t *testing.T) {
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"task"}]}`))
			return
		}
		_, _ = w.Write([]byte(completedTask(1, "https://images.example/one.png")))
	}))
	defer server.Close()
	provider.downloadAttempts = 1
	provider.downloadImage = func(context.Context, string, int64) (string, int64, error) {
		return "", 0, context.Canceled
	}
	_, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}})
	if bifrostErr == nil || bifrostErr.Type == nil || *bifrostErr.Type != schemas.RequestCancelled {
		t.Fatalf("unexpected enriched cancellation error: %v", bifrostErr)
	}
}

func TestPollStopsOnContextCancellationAndTimeout(t *testing.T) {
	provider := &APIMartProvider{initialPollDelay: time.Second, networkConfig: schemas.NetworkConfig{DefaultRequestTimeoutInSeconds: 300}}
	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	cancel()
	_, _, bifrostErr := provider.pollTask(ctx, testKey(), "task")
	if bifrostErr == nil || bifrostErr.Type == nil || *bifrostErr.Type != schemas.RequestCancelled {
		t.Fatalf("unexpected cancellation error: %v", bifrostErr)
	}

	deadlineCtx, deadlineCancel := schemas.NewBifrostContextWithTimeout(context.Background(), time.Millisecond)
	defer deadlineCancel()
	_, _, bifrostErr = provider.pollTask(deadlineCtx, testKey(), "task")
	if bifrostErr == nil || bifrostErr.Type == nil || *bifrostErr.Type != schemas.RequestTimedOut {
		t.Fatalf("unexpected timeout error: %v", bifrostErr)
	}
}

func TestRawResponseIsSanitized(t *testing.T) {
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"task-secret"}]}`))
			return
		}
		_, _ = w.Write([]byte(completedTask(1, "https://images.example/one.png")))
	}))
	defer server.Close()
	provider.sendBackRawRequest = true
	provider.sendBackRawResponse = true
	format := "url"
	response, bifrostErr := provider.ImageGeneration(schemas.NewBifrostContext(context.Background(), time.Time{}), testKey(), &schemas.BifrostImageGenerationRequest{Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}, Params: &schemas.ImageGenerationParameters{ResponseFormat: &format}})
	if bifrostErr != nil {
		t.Fatal(bifrostErr)
	}
	encoded, _ := json.Marshal(response.ExtraFields.RawResponse)
	if len(response.Data) != 1 || response.Data[0].URL != "https://images.example/one.png" || strings.Contains(string(encoded), "task-secret") || response.ExtraFields.RawRequest == nil {
		t.Fatalf("raw fields are incorrect: request=%v response=%s", response.ExtraFields.RawRequest, encoded)
	}
}

func TestStorageOnlyRawResponseKeepsTaskMetadataButRedactsURLQuery(t *testing.T) {
	provider, server := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"task-secret"}]}`))
			return
		}
		_, _ = w.Write([]byte(completedTask(1, "https://images.example/one.png?sig=secret")))
	}))
	defer server.Close()
	ctx := schemas.NewBifrostContext(context.Background(), time.Time{})
	ctx.SetValue(schemas.BifrostContextKeyCaptureRawResponse, true)
	ctx.SetValue(schemas.BifrostContextKeyDropRawResponseFromClient, true)
	format := "b64_json"
	response, bifrostErr := provider.ImageGeneration(ctx, testKey(), &schemas.BifrostImageGenerationRequest{Model: "gpt-image-2", Input: &schemas.ImageGenerationInput{Prompt: "cat"}, Params: &schemas.ImageGenerationParameters{ResponseFormat: &format}})
	if bifrostErr != nil {
		t.Fatal(bifrostErr)
	}
	encoded, _ := json.Marshal(response.ExtraFields.RawResponse)
	if !strings.Contains(string(encoded), "task-secret") || strings.Contains(string(encoded), "sig=secret") {
		t.Fatalf("storage raw response not safely preserved: %s", encoded)
	}
}

func TestURLResponseRejectsCredentialBearingResult(t *testing.T) {
	provider := &APIMartProvider{}
	_, bifrostErr := provider.buildImageData(context.Background(), []string{"https://images.example/one.png?X-Amz-Signature=secret"}, "url")
	if bifrostErr == nil || !bifrostErr.IsBifrostError || !strings.Contains(bifrostErr.GetErrorString(), "credential-bearing") {
		t.Fatalf("unexpected signed URL result: %v", bifrostErr)
	}
}

func TestAPIMartURLCredentialDetection(t *testing.T) {
	for _, rawURL := range []string{
		"https://cdn.example/image.png?sig=secret",
		"https://cdn.example/image.png?X-Goog-Signature=secret",
		"https://cdn.example/image.png?access_token=secret",
		"https://cdn.example/image.png?auth_key=secret",
		"https://user:pass@cdn.example/image.png",
	} {
		if !apimartURLContainsCredentials(rawURL) {
			t.Errorf("credential URL was accepted: %s", rawURL)
		}
	}
	if apimartURLContainsCredentials("https://cdn.example/image.png?version=2") {
		t.Fatal("benign query was rejected")
	}
}
