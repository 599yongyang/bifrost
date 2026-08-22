package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

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
