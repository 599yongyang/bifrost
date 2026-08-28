package otel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type otelTestMediaStore struct {
	items map[string][]schemas.TraceMedia
}

func (s *otelTestMediaStore) Store(key string, media schemas.TraceMedia) bool {
	media.Data = append([]byte(nil), media.Data...)
	s.items[key] = append(s.items[key], media)
	return true
}
func (s *otelTestMediaStore) List(key string) []schemas.TraceMedia {
	return append([]schemas.TraceMedia(nil), s.items[key]...)
}
func (s *otelTestMediaStore) Delete(key string) { delete(s.items, key) }

func attachTestMedia(trace *schemas.Trace, media ...schemas.TraceMedia) {
	store := &otelTestMediaStore{items: make(map[string][]schemas.TraceMedia)}
	trace.InternalID = "test-media-store-key"
	trace.SetMediaStore(store, trace.InternalID)
	for _, attachment := range media {
		trace.AddMedia(attachment)
	}
}

type failingTestMediaUploader struct{}

func (f *failingTestMediaUploader) Upload(context.Context, string, schemas.TraceMedia) (string, error) {
	return "", io.ErrUnexpectedEOF
}
func (f *failingTestMediaUploader) Close() {}

type countingFailingMediaUploader struct {
	calls atomic.Int32
}

func (u *countingFailingMediaUploader) Upload(context.Context, string, schemas.TraceMedia) (string, error) {
	u.calls.Add(1)
	return "", io.ErrUnexpectedEOF
}

func (u *countingFailingMediaUploader) Close() {}

type countingTestOtelClient struct {
	calls atomic.Int32
}

func (c *countingTestOtelClient) Emit(context.Context, []*ResourceSpan) error {
	c.calls.Add(1)
	return nil
}
func (c *countingTestOtelClient) Close() error { return nil }

type blockingTestMediaUploader struct {
	active    atomic.Int32
	maxActive atomic.Int32
}

func (u *blockingTestMediaUploader) Upload(ctx context.Context, _ string, _ schemas.TraceMedia) (string, error) {
	active := u.active.Add(1)
	defer u.active.Add(-1)
	for {
		maximum := u.maxActive.Load()
		if active <= maximum || u.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func (u *blockingTestMediaUploader) Close() {}

type capturingTestOtelClient struct {
	mu            sync.Mutex
	resourceSpans []*ResourceSpan
}

func (c *capturingTestOtelClient) Emit(_ context.Context, spans []*ResourceSpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resourceSpans = spans
	return nil
}
func (c *capturingTestOtelClient) Close() error { return nil }

func TestInjectUploadsLangfuseMediaAndReplacesObservationReference(t *testing.T) {
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	const localRef = "bifrost-media://bbbbbbbbbbbbbbbb-image-0-0123456789abcdef"
	var createCalls, putCalls, patchCalls, otlpCalls atomic.Int32
	var exportedInput string

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/media":
			createCalls.Add(1)
			if got := r.Header.Get("Authorization"); got != "Basic test-credentials" {
				t.Errorf("media auth = %q, want configured collector auth", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mediaId":"media-123","uploadUrl":"` + server.URL + `/upload"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			putCalls.Add(1)
			body, _ := io.ReadAll(r.Body)
			if !bytes.Equal(body, image) {
				t.Errorf("uploaded bytes = %x, want %x", body, image)
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("collector Authorization header leaked to presigned upload URL")
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/public/media/media-123":
			patchCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/media/media-123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mediaId":"media-123","url":"https://media.example.test/image.png","uploadedAt":"2026-08-17T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/otel/v1/traces":
			otlpCalls.Add(1)
			payload, _ := io.ReadAll(r.Body)
			var request collectorpb.ExportTraceServiceRequest
			if err := proto.Unmarshal(payload, &request); err != nil {
				t.Errorf("decode OTLP request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for _, rs := range request.ResourceSpans {
				for _, scope := range rs.ScopeSpans {
					for _, span := range scope.Spans {
						if value := attrString(span, "langfuse.observation.input"); value != "" {
							exportedInput = value
						}
					}
				}
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
			_, _ = w.Write([]byte{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	plugin, err := Init(context.Background(), &Config{Profiles: []*Profile{{
		Enabled:       true,
		TracesEnabled: true,
		ServiceName:   "bifrost-test",
		CollectorURL:  schemas.NewSecretVar(server.URL + "/api/public/otel/v1/traces"),
		Headers:       map[string]string{"Authorization": "Basic test-credentials"},
		Protocol:      ProtocolHTTP,
		Insecure:      true,
	}}}, bifrost.NewDefaultLogger(schemas.LogLevelError), nil, "test")
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer plugin.Cleanup()
	// Production uses the SSRF-safe upload dialer. This integration test owns the
	// loopback server, so replace only the untrusted-content client's transport.
	mediaClient, ok := plugin.targets[0].mediaUploader.(*langfuseMediaClient)
	if !ok {
		t.Fatalf("media uploader = %T", plugin.targets[0].mediaUploader)
	}
	mediaClient.uploadClient = server.Client()
	mediaClient.uploadClient.CheckRedirect = rejectMediaUploadRedirect

	now := time.Now()
	root := &schemas.Span{SpanID: "aaaaaaaaaaaaaaaa", Name: "request", Kind: schemas.SpanKindInternal, StartTime: now, EndTime: now.Add(time.Millisecond)}
	child := &schemas.Span{
		SpanID: "bbbbbbbbbbbbbbbb", ParentID: root.SpanID, Name: "generate_content gpt-image-2", Kind: schemas.SpanKindLLMCall,
		StartTime: now, EndTime: now.Add(time.Second),
		Attributes: map[string]any{
			schemas.AttrLegacyRequestType: string(schemas.ImageEditRequest),
			schemas.AttrBifrostImageInput: `{"prompt":"edit","images":[{"media_ref":"` + localRef + `","mime_type":"image/png","bytes":16,"sha256":"0123456789abcdef"}]}`,
		},
	}
	trace := &schemas.Trace{
		TraceID:  "00000000000000000000000000000004",
		RootSpan: root,
		Spans:    []*schemas.Span{root, child},
	}
	attachTestMedia(trace, schemas.TraceMedia{
		ID: strings.TrimPrefix(localRef, "bifrost-media://"), SpanID: child.SpanID, Field: "input", Role: "image",
		MIMEType: "image/png", Bytes: len(image), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Data: image,
	})
	if err := plugin.Inject(context.Background(), trace); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}

	if createCalls.Load() != 1 || putCalls.Load() != 1 || patchCalls.Load() != 1 || otlpCalls.Load() != 1 {
		t.Fatalf("calls create/put/patch/otlp = %d/%d/%d/%d, want 1/1/1/1", createCalls.Load(), putCalls.Load(), patchCalls.Load(), otlpCalls.Load())
	}
	wantToken := "@@@langfuseMedia:type=image/png|id=media-123|source=bytes@@@"
	if !strings.Contains(exportedInput, wantToken) || strings.Contains(exportedInput, localRef) {
		t.Fatalf("exported input = %q, want media token and no local reference", exportedInput)
	}
}

func TestLangfuseMediaUploadRejectsPendingExistingMedia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/media":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mediaId":"pending-media","uploadUrl":null}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/media/pending-media":
			http.Error(w, "media not yet uploaded", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newLangfuseMediaClient(server.URL+"/api/public/otel/v1/traces", nil, time.Second, "", false)
	if err != nil {
		t.Fatalf("new media client: %v", err)
	}
	token, uploadErr := client.Upload(context.Background(), "trace-1", schemas.TraceMedia{
		ID: "local", SpanID: "span-1", Field: "output", MIMEType: "image/png", Data: []byte("image"),
	})
	if uploadErr == nil || token != "" {
		t.Fatalf("pending media result token/error = %q/%v, want no token and an error", token, uploadErr)
	}
}

func TestLangfuseMediaUploadRequiresConfirmedStatusAfterPatch(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/media":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mediaId":"unconfirmed-media","uploadUrl":"` + server.URL + `/upload"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/public/media/unconfirmed-media":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/media/unconfirmed-media":
			http.Error(w, "media not yet uploaded", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newLangfuseMediaClient(server.URL+"/api/public/otel/v1/traces", nil, time.Second, "", false)
	if err != nil {
		t.Fatalf("new media client: %v", err)
	}
	token, uploadErr := client.Upload(context.Background(), "trace-1", schemas.TraceMedia{
		ID: "local", SpanID: "span-1", Field: "output", MIMEType: "image/png", Data: []byte("image"),
	})
	if uploadErr == nil || token != "" {
		t.Fatalf("unconfirmed media result token/error = %q/%v, want no token and an error", token, uploadErr)
	}
}

func TestLangfuseMediaVerificationRejectsIncompleteSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mediaId":"media-1","url":"","uploadedAt":null}`))
	}))
	defer server.Close()
	client := &langfuseMediaClient{client: server.Client(), endpoint: server.URL}
	if err := client.verifyMedia(context.Background(), "media-1"); err == nil {
		t.Fatal("incomplete 2xx media response was accepted")
	}
}

func TestLangfuseMediaNetworkErrorIsClassifiedWithoutLeakingSignedURL(t *testing.T) {
	const signedURL = "https://minio.internal/upload?X-Amz-Signature=secret-value"
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"mediaId":"media-1","uploadUrl":"` + signedURL + `"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: "minio.internal", IsNotFound: true}
	})}
	client := &langfuseMediaClient{
		client:       httpClient,
		uploadClient: httpClient,
		endpoint:     "https://langfuse.example.test/api/public/media",
	}
	_, err := client.Upload(context.Background(), "trace-1", schemas.TraceMedia{
		ID: "local", SpanID: "span-1", Field: "output", MIMEType: "image/png", Data: []byte("image"),
	})
	if err == nil {
		t.Fatal("expected upload error")
	}
	message := err.Error()
	if !strings.Contains(message, "source=dns_error") || !strings.Contains(message, "host=minio.internal") {
		t.Fatalf("classified error = %q", message)
	}
	if strings.Contains(message, "secret-value") || strings.Contains(message, "X-Amz") {
		t.Fatalf("classified error leaked signed URL: %q", message)
	}
}

func TestLangfuseMediaClientUsesProfileTLSSettings(t *testing.T) {
	client, err := newLangfuseMediaClient("https://langfuse.example.test/api/public/otel/v1/traces", nil, time.Second, "", true)
	if err != nil {
		t.Fatalf("new media client: %v", err)
	}
	transport, ok := client.client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("media client did not inherit profile insecure TLS setting")
	}
}

func TestPresignedUploadURLBlocksNonPublicTargetsWithoutLeakingQuery(t *testing.T) {
	client, err := newLangfuseMediaClient("https://langfuse.example.test/api/public/otel/v1/traces", nil, time.Second, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{
		"http://127.0.0.1/upload?X-Amz-Signature=loopback-secret",
		"http://10.0.0.8/upload?token=rfc1918-secret",
		"http://169.254.169.254/latest/meta-data?token=link-local-secret",
		"http://[::1]/upload?token=ipv6-secret",
	} {
		t.Run(rawURL, func(t *testing.T) {
			_, putErr := client.putMedia(context.Background(), rawURL, "image/png", "checksum", []byte("image"))
			if putErr == nil || !strings.Contains(putErr.Error(), "source=") {
				t.Fatalf("putMedia error = %v", putErr)
			}
			for _, secret := range []string{"loopback-secret", "rfc1918-secret", "link-local-secret", "ipv6-secret", "X-Amz-Signature"} {
				if strings.Contains(putErr.Error(), secret) {
					t.Fatalf("error leaked upload query: %q", putErr)
				}
			}
		})
	}
}

func TestPresignedUploadClientRejectsRedirects(t *testing.T) {
	if err := rejectMediaUploadRedirect(nil, nil); err == nil {
		t.Fatal("upload redirect was allowed")
	}
}

func TestImageSelectionRequiresCompleteReferencedMedia(t *testing.T) {
	selector, err := newTraceSelector(&SelectiveExportConfig{Enabled: true, Rules: []SelectionRule{{ID: "all-images", ExportRate: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	span := &schemas.Span{SpanID: "image-span", Kind: schemas.SpanKindLLMCall, StartTime: now, EndTime: now.Add(time.Second), Attributes: map[string]any{
		schemas.AttrLegacyRequestType:  string(schemas.ImageEditRequest),
		schemas.AttrBifrostImageInput:  `{"images":[{"media_ref":"bifrost-media://input","capture_status":"captured"}]}`,
		schemas.AttrBifrostImageOutput: `{"images":[{"url":"https://images.example.test/result.png"}],"image_count":1}`,
	}}
	trace := &schemas.Trace{TraceID: "complete-image", InternalID: "complete-image", RootSpan: span, Spans: []*schemas.Span{span}}
	attachTestMedia(trace, schemas.TraceMedia{ID: "input", SpanID: span.SpanID, Field: "input", Role: "image", MIMEType: "image/png", Data: []byte("image")})
	if decision := selector.decide(trace); !decision.selected {
		t.Fatalf("complete media was rejected: %+v", decision)
	}

	missing := &schemas.Trace{TraceID: "missing-image", InternalID: "missing-image", RootSpan: span, Spans: []*schemas.Span{span}}
	if decision := selector.decide(missing); decision.selected || decision.reason != "incomplete_media" {
		t.Fatalf("missing media decision = %+v", decision)
	}

	errorSpan := &schemas.Span{SpanID: "error-span", Kind: schemas.SpanKindLLMCall, StartTime: now, EndTime: now.Add(time.Second), Status: schemas.SpanStatusError, Attributes: map[string]any{
		schemas.AttrLegacyRequestType: string(schemas.ImageGenerationRequest),
		schemas.AttrBifrostImageInput: `{"prompt":"blocked"}`,
	}}
	errorTrace := &schemas.Trace{TraceID: "error-image", InternalID: "error-image", RootSpan: errorSpan, Spans: []*schemas.Span{errorSpan}}
	if decision := selector.decide(errorTrace); !decision.selected {
		t.Fatalf("finalized image error should not require output media: %+v", decision)
	}
}

func TestImageOTELConversionKeepsCanonicalUsageAndAddsLangfuseDetails(t *testing.T) {
	now := time.Now()
	span := &schemas.Span{SpanID: "bbbbbbbbbbbbbbbb", TraceID: "00000000000000000000000000000001", Kind: schemas.SpanKindLLMCall, StartTime: now, EndTime: now.Add(time.Second), Attributes: map[string]any{
		schemas.AttrLegacyRequestType:      string(schemas.ImageGenerationRequest),
		schemas.AttrBifrostProviderName:    "openai",
		schemas.AttrRequestModel:           "gpt-image",
		schemas.AttrBifrostImageInput:      `{"prompt":"moon"}`,
		schemas.AttrBifrostImageOutput:     `{"images":[{"media_ref":"bifrost-media://output"}],"image_count":1}`,
		schemas.AttrInputTokens:            10,
		schemas.AttrOutputTokens:           20,
		schemas.AttrTotalTokens:            30,
		schemas.AttrInputTokenDetailsImage: 4,
		schemas.AttrUsageCost:              0.25,
	}}
	out := convertSpanToOTELSpanWithMedia(span.TraceID, span, false, map[string]string{"bifrost-media://output": "@@@media@@@"})
	if got := attrString(out, schemas.AttrBifrostProviderName); got != "openai" {
		t.Fatalf("canonical provider attr = %q", got)
	}
	if got := attrString(out, "langfuse.observation.output"); !strings.Contains(got, "@@@media@@@") || strings.Contains(got, "bifrost-media://") {
		t.Fatalf("langfuse output = %q", got)
	}
	if got := attrString(out, "langfuse.observation.usage_details"); !strings.Contains(got, `"input":10`) || !strings.Contains(got, `"input_image":4`) {
		t.Fatalf("usage details = %q", got)
	}
	if got := attrString(out, "langfuse.observation.cost_details"); !strings.Contains(got, `"total":0.25`) {
		t.Fatalf("cost details = %q", got)
	}
	for _, attribute := range out.Attributes {
		if value := attribute.GetValue().GetStringValue(); strings.Contains(value, "bifrost-media://") {
			t.Fatalf("OTLP attribute %q retained a local media reference: %q", attribute.Key, value)
		}
	}
}

func TestInjectMediaFailureDegradesWithoutDroppingTrace(t *testing.T) {
	logger = bifrost.NewDefaultLogger(schemas.LogLevelError)
	client := &capturingTestOtelClient{}
	plugin := &OtelPlugin{
		pluginSpanFilter: &PluginSpanFilter{},
		targets: []*otelTarget{{
			serviceName: "bifrost-test", client: client, mediaUploader: &failingTestMediaUploader{}, exportTimeout: time.Second,
		}},
	}
	now := time.Now()
	root := &schemas.Span{SpanID: "aaaaaaaaaaaaaaaa", Kind: schemas.SpanKindInternal, StartTime: now, EndTime: now.Add(time.Millisecond)}
	child := &schemas.Span{
		SpanID: "bbbbbbbbbbbbbbbb", ParentID: root.SpanID, Kind: schemas.SpanKindLLMCall, StartTime: now, EndTime: now.Add(time.Second),
		Attributes: map[string]any{
			schemas.AttrLegacyRequestType: string(schemas.ImageVariationRequest),
			schemas.AttrBifrostImageInput: `{"images":[{"media_ref":"bifrost-media://local-image","mime_type":"image/png","bytes":16}]}`,
		},
	}
	trace := &schemas.Trace{
		TraceID: "00000000000000000000000000000005", RootSpan: root, Spans: []*schemas.Span{root, child},
	}
	attachTestMedia(trace, schemas.TraceMedia{ID: "local-image", SpanID: child.SpanID, Field: "input", MIMEType: "image/png", Bytes: 16, Data: []byte("not exported raw")})

	if err := plugin.Inject(context.Background(), trace); err != nil {
		t.Fatalf("Inject() returned media failure: %v", err)
	}
	if len(client.resourceSpans) != 1 {
		t.Fatalf("OTLP emits = %d, want one despite media failure", len(client.resourceSpans))
	}
	var input string
	for _, span := range client.resourceSpans[0].ScopeSpans[0].Spans {
		if value := attrString(span, "langfuse.observation.input"); value != "" {
			input = value
		}
	}
	if input == "" || strings.Contains(input, "bifrost-media://") || strings.Contains(input, "not exported raw") {
		t.Fatalf("degraded observation input = %q, want metadata without local/raw media", input)
	}
}

func TestInjectBoundsMediaConcurrencyAcrossTracesAndBatchLifetime(t *testing.T) {
	logger = bifrost.NewDefaultLogger(schemas.LogLevelError)
	uploader := &blockingTestMediaUploader{}
	client := &capturingTestOtelClient{}
	target := &otelTarget{
		serviceName: "bifrost-test", client: client, mediaUploader: uploader,
		exportTimeout: 50 * time.Millisecond,
		mediaSem:      make(chan struct{}, 2),
	}
	plugin := &OtelPlugin{pluginSpanFilter: &PluginSpanFilter{}, targets: []*otelTarget{target}}

	started := time.Now()
	var wg sync.WaitGroup
	for traceIndex := 0; traceIndex < 8; traceIndex++ {
		trace := &schemas.Trace{TraceID: fmt.Sprintf("%032x", traceIndex+1)}
		attachments := make([]schemas.TraceMedia, 0, 4)
		for mediaIndex := 0; mediaIndex < 4; mediaIndex++ {
			attachments = append(attachments, schemas.TraceMedia{
				ID:     fmt.Sprintf("trace-%d-media-%d", traceIndex, mediaIndex),
				SpanID: "span-1", Field: "input", MIMEType: "image/png", Data: []byte("image"),
			})
		}
		attachTestMedia(trace, attachments...)
		wg.Go(func() {
			if err := plugin.Inject(context.Background(), trace); err != nil {
				t.Errorf("Inject() error = %v", err)
			}
		})
	}
	wg.Wait()

	if maximum := uploader.maxActive.Load(); maximum > 2 {
		t.Fatalf("maximum concurrent media uploads = %d, want target-wide limit 2", maximum)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("media batches took %s, want one shared deadline including semaphore wait", elapsed)
	}
}

func TestInjectMediaBreakerSuppressesFailedUploadsWithoutSuppressingOTLP(t *testing.T) {
	logger = bifrost.NewDefaultLogger(schemas.LogLevelError)
	uploader := &countingFailingMediaUploader{}
	client := &countingTestOtelClient{}
	target := &otelTarget{
		serviceName: "bifrost-test", client: client, mediaUploader: uploader,
		exportTimeout: time.Second,
		mediaSem:      make(chan struct{}, 2),
	}
	plugin := &OtelPlugin{pluginSpanFilter: &PluginSpanFilter{}, targets: []*otelTarget{target}}

	for traceIndex := 0; traceIndex < 12; traceIndex++ {
		trace := &schemas.Trace{TraceID: fmt.Sprintf("%032x", traceIndex+1)}
		attachTestMedia(trace, schemas.TraceMedia{
			ID: fmt.Sprintf("media-%d", traceIndex), SpanID: "span-1", Field: "input", MIMEType: "image/png", Data: []byte("image"),
		})
		if err := plugin.Inject(context.Background(), trace); err != nil {
			t.Fatalf("Inject() error = %v", err)
		}
	}

	if calls := uploader.calls.Load(); calls != breakerFailureThreshold {
		t.Fatalf("media upload calls = %d, want %d before breaker suppression", calls, breakerFailureThreshold)
	}
	if calls := client.calls.Load(); calls != 12 {
		t.Fatalf("OTLP emit calls = %d, want 12 despite media breaker", calls)
	}
}
