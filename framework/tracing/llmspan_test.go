package tracing

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func ptr[T any](value T) *T { return &value }

func traceWithMediaStore() *schemas.Trace {
	trace := &schemas.Trace{InternalID: "test-trace"}
	trace.SetMediaStore(newTraceMediaStore(), trace.InternalID)
	return trace
}

func TestPopulateImageGenerationAttributes(t *testing.T) {
	requestAttrs := PopulateRequestAttributes(&schemas.BifrostRequest{
		RequestType: schemas.ImageGenerationRequest,
		ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{
			Provider: schemas.OpenAI,
			Model:    "gpt-image-2",
			Input:    &schemas.ImageGenerationInput{Prompt: "a moonlit harbor"},
			Params: &schemas.ImageGenerationParameters{
				Size:    ptr("2048x1152"),
				Quality: ptr("high"),
				N:       ptr(2),
			},
		},
	})

	input := assertJSONAttr(t, requestAttrs, "bifrost.image.input")
	if input["prompt"] != "a moonlit harbor" || input["size"] != "2048x1152" || input["quality"] != "high" || input["n"] != float64(2) {
		t.Fatalf("image input = %#v, want prompt/size/quality/n", input)
	}

	responseAttrs := PopulateResponseAttributes(&schemas.BifrostResponse{
		ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{
			Model: "gpt-image-2-2026-08-01",
			Data: []schemas.ImageData{{
				URL:           "https://images.example.test/result.png",
				RevisedPrompt: "A moonlit harbor in watercolor",
			}},
		},
	})
	output := assertJSONAttr(t, responseAttrs, "bifrost.image.output")
	if output["image_count"] != float64(1) {
		t.Fatalf("image output count = %v, want 1", output["image_count"])
	}
	images, ok := output["images"].([]any)
	if !ok || len(images) != 1 {
		t.Fatalf("image output images = %T(%v), want one image", output["images"], output["images"])
	}
	image, ok := images[0].(map[string]any)
	if !ok || image["url"] != "https://images.example.test/result.png" || image["revised_prompt"] != "A moonlit harbor in watercolor" {
		t.Fatalf("image output = %#v, want URL and revised prompt", images[0])
	}
}

func TestPopulateImageResponseAttributesRejectsUnsafeURL(t *testing.T) {
	attrs := map[string]any{}
	PopulateImageResponseAttributes(&schemas.BifrostImageGenerationResponse{Data: []schemas.ImageData{{
		URL: "https://user:password@example.com/generated.png",
	}}}, attrs)

	output, _ := attrs[schemas.AttrBifrostImageOutput].(string)
	if strings.Contains(output, "password") || strings.Contains(output, "user:") {
		t.Fatalf("image output leaked URL credentials: %s", output)
	}
	if !strings.Contains(output, `"capture_status":"invalid_url"`) {
		t.Fatalf("image output missing invalid URL status: %s", output)
	}
}

func TestImageMediaReferencesAreDistinctAcrossInputAndOutput(t *testing.T) {
	trace := traceWithMediaStore()
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 's', 'a', 'm', 'e'}
	input := summarizeImageBytes(trace, "span-1", "input", "image", 0, image)
	output := summarizeImageBytes(trace, "span-1", "output", "image", 0, image)
	if input["media_ref"] == output["media_ref"] {
		t.Fatalf("input and output reused media reference %v", input["media_ref"])
	}
}

func TestImageMediaCaptureHasPerTraceAttachmentLimit(t *testing.T) {
	trace := traceWithMediaStore()
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'i'}
	for i := 0; i < 16; i++ {
		summary := summarizeImageBytes(trace, "span-1", "input", "image", i, image)
		if summary["capture_status"] != "captured" {
			t.Fatalf("attachment %d status = %v, want captured", i, summary["capture_status"])
		}
	}
	overflow := summarizeImageBytes(trace, "span-1", "input", "image", 16, image)
	if overflow["capture_status"] != "attachment_limit" || len(trace.MediaAttachments()) != 16 {
		t.Fatalf("overflow status/media = %v/%d, want attachment_limit/16", overflow["capture_status"], len(trace.MediaAttachments()))
	}
}

func TestPopulateImageResponseRejectsOversizedBase64BeforeDecode(t *testing.T) {
	encodedLength := base64.StdEncoding.EncodedLen(maxTraceMediaBytes + 1)
	oversizedInvalid := strings.Repeat("A", encodedLength-1) + "!"
	attrs := map[string]any{}
	populateImageResponseAttributes(&schemas.BifrostImageGenerationResponse{
		Data: []schemas.ImageData{{B64JSON: oversizedInvalid}},
	}, attrs, traceWithMediaStore(), "span-1")

	output := assertJSONAttr(t, attrs, schemas.AttrBifrostImageOutput)
	images, _ := output["images"].([]any)
	image, _ := images[0].(map[string]any)
	if status := image["capture_status"]; status != "too_large" {
		t.Fatalf("oversized base64 status = %v, want too_large preflight", status)
	}
}

func assertJSONAttr(t *testing.T, attrs map[string]any, key string) map[string]any {
	t.Helper()

	raw, ok := attrs[key].(string)
	if !ok {
		t.Fatalf("attribute %s = %T(%v), want JSON string", key, attrs[key], attrs[key])
	}
	if strings.Contains(raw, "map[") || strings.Contains(raw, "&map") {
		t.Fatalf("attribute %s used Go map formatting: %q", key, raw)
	}

	var parsed map[string]any
	if err := schemas.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("attribute %s = %q, want valid JSON object: %v", key, raw, err)
	}
	return parsed
}

func TestPopulateResponsesResponseAttributesSerializesMetadataAsJSON(t *testing.T) {
	emptyMetadata := map[string]any{}
	attrs := map[string]any{}

	PopulateResponsesResponseAttributes(&schemas.BifrostResponsesResponse{
		Metadata: &emptyMetadata,
	}, attrs)

	if got := attrs[schemas.AttrRespMetadata]; got != "{}" {
		t.Fatalf("empty metadata = %v, want {}", got)
	}

	metadata := map[string]any{
		"tenant": "acme",
		"flags":  []any{"beta", "trace"},
		"nested": map[string]any{"enabled": true},
	}
	attrs = map[string]any{}

	PopulateResponsesResponseAttributes(&schemas.BifrostResponsesResponse{
		Metadata: &metadata,
	}, attrs)

	parsed := assertJSONAttr(t, attrs, schemas.AttrRespMetadata)
	if parsed["tenant"] != "acme" {
		t.Fatalf("metadata tenant = %v, want acme", parsed["tenant"])
	}
	if _, ok := parsed["nested"].(map[string]any); !ok {
		t.Fatalf("metadata nested = %T(%v), want object", parsed["nested"], parsed["nested"])
	}
}

func TestPopulateTextCompletionRequestAttributesSerializesLogitBiasAsJSON(t *testing.T) {
	logitBias := map[string]float64{"50256": -100}
	attrs := map[string]any{}

	PopulateTextCompletionRequestAttributes(&schemas.BifrostTextCompletionRequest{
		Params: &schemas.TextCompletionParameters{
			LogitBias: &logitBias,
		},
	}, attrs)

	parsed := assertJSONAttr(t, attrs, schemas.AttrLogitBias)
	if parsed["50256"] != float64(-100) {
		t.Fatalf("logit bias = %v, want -100", parsed["50256"])
	}
}

func TestPopulateBatchCreateRequestAttributesSerializesMetadataAsJSON(t *testing.T) {
	attrs := map[string]any{}

	PopulateBatchCreateRequestAttributes(&schemas.BifrostBatchCreateRequest{
		Metadata: map[string]string{"job": "nightly"},
	}, attrs)

	parsed := assertJSONAttr(t, attrs, schemas.AttrBatchMetadata)
	if parsed["job"] != "nightly" {
		t.Fatalf("batch metadata job = %v, want nightly", parsed["job"])
	}
}

func TestPopulateRequestExtraParamsSerializesStructuredValues(t *testing.T) {
	tests := []struct {
		name     string
		populate func(map[string]any)
	}{
		{
			name: "chat",
			populate: func(attrs map[string]any) {
				PopulateChatRequestAttributes(&schemas.BifrostChatRequest{
					Params: &schemas.ChatParameters{
						ExtraParams: map[string]any{
							"structured": map[string]any{"mode": "json"},
							"scalar":     7,
						},
					},
				}, attrs)
			},
		},
		{
			name: "text",
			populate: func(attrs map[string]any) {
				PopulateTextCompletionRequestAttributes(&schemas.BifrostTextCompletionRequest{
					Params: &schemas.TextCompletionParameters{
						ExtraParams: map[string]any{
							"structured": []any{"a", "b"},
							"scalar":     true,
						},
					},
				}, attrs)
			},
		},
		{
			name: "embedding",
			populate: func(attrs map[string]any) {
				PopulateEmbeddingRequestAttributes(&schemas.BifrostEmbeddingRequest{
					Params: &schemas.EmbeddingParameters{
						ExtraParams: map[string]any{
							"structured": map[string]any{"dimensions": 1536},
							"scalar":     "text",
						},
					},
				}, attrs)
			},
		},
		{
			name: "batch",
			populate: func(attrs map[string]any) {
				PopulateBatchListRequestAttributes(&schemas.BifrostBatchListRequest{
					ExtraParams: map[string]any{
						"structured": map[string]any{"cursor": "next"},
						"scalar":     3,
					},
				}, attrs)
			},
		},
		{
			name: "file",
			populate: func(attrs map[string]any) {
				PopulateFileListRequestAttributes(&schemas.BifrostFileListRequest{
					ExtraParams: map[string]any{
						"structured": map[string]any{"storage": "s3"},
						"scalar":     "raw",
					},
				}, attrs)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attrs := map[string]any{}
			tc.populate(attrs)

			raw, ok := attrs["structured"].(string)
			if !ok {
				t.Fatalf("structured extra param = %T(%v), want string", attrs["structured"], attrs["structured"])
			}
			if strings.Contains(raw, "map[") || strings.Contains(raw, "&map") {
				t.Fatalf("structured extra param used Go formatting: %q", raw)
			}
			var parsed any
			if err := schemas.Unmarshal([]byte(raw), &parsed); err != nil {
				t.Fatalf("structured extra param = %q, want valid JSON: %v", raw, err)
			}
			if attrs["scalar"] == "" || attrs["scalar"] == nil {
				t.Fatalf("scalar extra param was not preserved: %v", attrs["scalar"])
			}
		})
	}
}
