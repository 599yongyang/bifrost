package bifrost

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestAttachUpstreamRequestIDPrefersClientRequestID(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, map[string]string{
		"X-Request-Id":        "gateway-a, gateway-b",
		"X-Client-Request-Id": "provider-request-123",
		"X-Vendor-Request-Id": "vendor-request-456",
	})
	bifrostErr := invalidImageResponseError("image 0 has neither url nor b64_json")

	attachUpstreamRequestID(ctx, bifrostErr)

	if got := bifrostErr.ExtraFields.UpstreamRequestID; got != "provider-request-123" {
		t.Fatalf("upstream request ID = %q, want provider-request-123", got)
	}
	if got := bifrostErr.ExtraFields.UpstreamResponseHeaders; len(got) != 3 || got["x-client-request-id"] != "provider-request-123" || got["x-request-id"] != "gateway-a, gateway-b" || got["x-vendor-request-id"] != "vendor-request-456" {
		t.Fatalf("upstream response headers = %#v, want allowlisted request IDs", got)
	}
}

func TestFilterUpstreamResponseHeadersKeepsOnlySafeOperationalHeaders(t *testing.T) {
	headers := filterUpstreamResponseHeaders(map[string]string{
		"Retry-After":           "30",
		"X-RateLimit-Remaining": "9",
		"X-Amzn-RequestId":      "aws-request-123",
		"X-Vendor-Request-Id":   "vendor-request-456",
		"Set-Cookie":            "session=secret",
		"Authorization":         "Bearer secret",
		"X-Internal-Debug":      "do-not-store",
	})

	if len(headers) != 4 || headers["retry-after"] != "30" || headers["x-ratelimit-remaining"] != "9" || headers["x-amzn-requestid"] != "aws-request-123" || headers["x-vendor-request-id"] != "vendor-request-456" {
		t.Fatalf("filtered headers = %#v, want only retry/rate-limit/request-id headers", headers)
	}
}

func TestValidateImageResponseRejectsInvalidResponsesForAllImageRequestTypes(t *testing.T) {
	requestTypes := []schemas.RequestType{
		schemas.ImageGenerationRequest,
		schemas.ImageEditRequest,
		schemas.ImageVariationRequest,
	}
	invalidResponses := []struct {
		name     string
		response *schemas.BifrostResponse
	}{
		{name: "nil response"},
		{name: "nil image response", response: &schemas.BifrostResponse{}},
		{name: "empty data", response: imageResponse()},
		{name: "metadata only", response: imageResponse(schemas.ImageData{RevisedPrompt: "a ceramic coffee cup"})},
	}

	for _, requestType := range requestTypes {
		for _, testCase := range invalidResponses {
			t.Run(string(requestType)+"/"+testCase.name, func(t *testing.T) {
				err := validateImageResponse(requestType, testCase.response)
				assertInvalidImageResponseError(t, err)
			})
		}
	}
}

func TestValidateImageResponseAcceptsURLAndBase64Images(t *testing.T) {
	validImages := []struct {
		name  string
		image schemas.ImageData
	}{
		{name: "url", image: schemas.ImageData{URL: "https://images.example.test/output.png"}},
		{name: "base64", image: schemas.ImageData{B64JSON: "iVBORw0KGgo="}},
	}

	for _, requestType := range []schemas.RequestType{
		schemas.ImageGenerationRequest,
		schemas.ImageEditRequest,
		schemas.ImageVariationRequest,
	} {
		for _, testCase := range validImages {
			t.Run(string(requestType)+"/"+testCase.name, func(t *testing.T) {
				if err := validateImageResponse(requestType, imageResponse(testCase.image)); err != nil {
					t.Fatalf("valid image response rejected: %v", err)
				}
			})
		}
	}
}

func TestValidateImageResponseRequiresEveryImageToContainData(t *testing.T) {
	err := validateImageResponse(schemas.ImageGenerationRequest, imageResponse(
		schemas.ImageData{URL: "https://images.example.test/output.png"},
		schemas.ImageData{RevisedPrompt: "metadata without image data"},
	))
	assertInvalidImageResponseError(t, err)
}

func TestValidateImageResponseIgnoresNonImageRequests(t *testing.T) {
	if err := validateImageResponse(schemas.ChatCompletionRequest, nil); err != nil {
		t.Fatalf("non-image response rejected: %v", err)
	}
}

func TestInvalidImageResponseCanBeRecoveredByPostLLMHook(t *testing.T) {
	recovered := imageResponse(schemas.ImageData{URL: "https://images.example.test/recovered.png"})
	plugin := &imageRecoveryPlugin{response: recovered}
	pipeline := &PluginPipeline{
		logger:     NewNoOpLogger(),
		tracer:     &schemas.NoOpTracer{},
		llmPlugins: []schemas.LLMPlugin{plugin},
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	invalidErr := validateImageResponse(schemas.ImageGenerationRequest, imageResponse())
	invalidErr.PopulateExtraFields(schemas.ImageGenerationRequest, schemas.OpenAI, "gpt-image-1", "gpt-image-1")

	resp, err := pipeline.RunPostLLMHooks(ctx, nil, invalidErr, 1)
	if err != nil {
		t.Fatalf("post-hook did not recover invalid image response: %v", err)
	}
	if resp != recovered {
		t.Fatalf("response = %p, want recovered response %p", resp, recovered)
	}
	if !plugin.sawInvalidImageError {
		t.Fatal("post-hook did not receive invalid_image_response")
	}
}

func imageResponse(images ...schemas.ImageData) *schemas.BifrostResponse {
	return &schemas.BifrostResponse{
		ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{Data: images},
	}
}

func assertInvalidImageResponseError(t *testing.T, err *schemas.BifrostError) {
	t.Helper()
	if err == nil {
		t.Fatal("expected image response to be rejected")
	}
	if err.StatusCode == nil || *err.StatusCode != 502 {
		t.Fatalf("status = %v, want 502", err.StatusCode)
	}
	if err.Type == nil || *err.Type != "invalid_image_response" {
		t.Fatalf("type = %v, want invalid_image_response", err.Type)
	}
	if err.Error == nil || err.Error.Type == nil || *err.Error.Type != "invalid_image_response" {
		t.Fatalf("nested error = %v, want invalid_image_response", err.Error)
	}
	if err.AllowFallbacks == nil || !*err.AllowFallbacks {
		t.Fatal("invalid image response must allow configured fallbacks")
	}
}

type imageRecoveryPlugin struct {
	response             *schemas.BifrostResponse
	sawInvalidImageError bool
}

func (p *imageRecoveryPlugin) GetName() string { return "image-recovery" }
func (p *imageRecoveryPlugin) Cleanup() error  { return nil }
func (p *imageRecoveryPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (p *imageRecoveryPlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (p *imageRecoveryPlugin) PostLLMHook(_ *schemas.BifrostContext, _ *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	p.sawInvalidImageError = err != nil && err.Type != nil && *err.Type == "invalid_image_response"
	return p.response, nil, nil
}
