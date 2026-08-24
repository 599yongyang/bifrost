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
		t.Fatalf("filtered headers = %#v, want only retry/rate-limit headers", headers)
	}
}

func TestValidateImageResponseRejectsMetadataOnlyImage(t *testing.T) {
	err := validateImageResponse(schemas.ImageGenerationRequest, &schemas.BifrostResponse{
		ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{
			Data: []schemas.ImageData{{
				RevisedPrompt: "a ceramic coffee cup",
			}},
		},
	})

	if err == nil {
		t.Fatal("expected metadata-only image response to be rejected")
	}
	if err.StatusCode == nil || *err.StatusCode != 502 {
		t.Fatalf("status = %v, want 502", err.StatusCode)
	}
	if err.AllowFallbacks == nil || !*err.AllowFallbacks {
		t.Fatal("invalid image response must allow configured fallbacks")
	}
}

func TestValidateImageResponseAcceptsURLAndBase64Images(t *testing.T) {
	for _, image := range []schemas.ImageData{{URL: "https://images.example.test/output.png"}, {B64JSON: "iVBORw0KGgo="}} {
		if err := validateImageResponse(schemas.ImageGenerationRequest, &schemas.BifrostResponse{
			ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{Data: []schemas.ImageData{image}},
		}); err != nil {
			t.Fatalf("valid image response rejected: %v", err)
		}
	}
}
