package tracing

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0, 'I', 'H', 'D', 'R'}

func mediaTraceForTest() (*schemas.Trace, *traceMediaStore) {
	store := newTraceMediaStore()
	trace := &schemas.Trace{InternalID: "media-test"}
	trace.SetMediaStore(store, trace.InternalID)
	return trace, store
}

func TestPopulateImageGenerationAndEditAttributes(t *testing.T) {
	trace, _ := mediaTraceForTest()
	generation := &schemas.BifrostRequest{RequestType: schemas.ImageGenerationRequest, ImageGenerationRequest: &schemas.BifrostImageGenerationRequest{
		Provider: schemas.OpenAI, Model: "gpt-image", Input: &schemas.ImageGenerationInput{Prompt: "draw a moon"},
		Params: &schemas.ImageGenerationParameters{Size: schemas.Ptr("1024x1024"), N: schemas.Ptr(1), InputImages: []string{
			base64.StdEncoding.EncodeToString(tinyPNG), "https://images.example.test/input.png?token=secret",
		}},
	}}
	attrs := PopulateRequestAttributesWithMedia(generation, trace, "generation-span")
	input := attrs[schemas.AttrBifrostImageInput].(string)
	assert.Contains(t, input, "draw a moon")
	assert.Contains(t, input, "1024x1024")
	assert.Contains(t, input, "bifrost-media://")
	assert.Contains(t, input, "https://images.example.test/input.png")
	assert.NotContains(t, input, "token=secret")
	assert.NotContains(t, input, "base64")

	edit := &schemas.BifrostRequest{RequestType: schemas.ImageEditRequest, ImageEditRequest: &schemas.BifrostImageEditRequest{
		Provider: schemas.OpenAI, Model: "gpt-image", Input: &schemas.ImageEditInput{Prompt: "edit", Images: []schemas.ImageInput{{Image: tinyPNG}}},
		Params: &schemas.ImageEditParameters{Mask: tinyPNG},
	}}
	attrs = PopulateRequestAttributesWithMedia(edit, trace, "edit-span")
	input = attrs[schemas.AttrBifrostImageInput].(string)
	assert.Contains(t, input, `"capture_status":"captured"`)
	assert.Contains(t, input, "bifrost-media://")
	assert.NotContains(t, input, base64.StdEncoding.EncodeToString(tinyPNG))
	require.Len(t, trace.MediaAttachments(), 2)
}

func TestPopulateImageResponseCapturesBase64AndUsage(t *testing.T) {
	trace, _ := mediaTraceForTest()
	resp := &schemas.BifrostResponse{ImageGenerationResponse: &schemas.BifrostImageGenerationResponse{
		Model: "gpt-image-wire",
		Data:  []schemas.ImageData{{Index: 0, B64JSON: base64.StdEncoding.EncodeToString(tinyPNG), RevisedPrompt: "safe prompt"}},
		Usage: &schemas.ImageUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30,
			InputTokensDetails:  &schemas.ImageTokenDetails{ImageTokens: 4, TextTokens: 6},
			OutputTokensDetails: &schemas.ImageTokenDetails{ImageTokens: 20}},
	}}
	attrs := PopulateResponseAttributesWithMedia(resp, trace, "response-span")
	output := attrs[schemas.AttrBifrostImageOutput].(string)
	assert.Contains(t, output, "bifrost-media://")
	assert.NotContains(t, output, base64.StdEncoding.EncodeToString(tinyPNG))
	assert.Equal(t, "gpt-image-wire", attrs[schemas.AttrResponseModel])
	assert.Equal(t, 10, attrs[schemas.AttrInputTokens])
	assert.Equal(t, 20, attrs[schemas.AttrOutputTokens])
	assert.Equal(t, 4, attrs[schemas.AttrInputTokenDetailsImage])
	require.Len(t, trace.MediaAttachments(), 1)
}

func TestImageMediaDeduplicatesFallbackInput(t *testing.T) {
	trace, _ := mediaTraceForTest()
	req := &schemas.BifrostRequest{RequestType: schemas.ImageEditRequest, ImageEditRequest: &schemas.BifrostImageEditRequest{
		Input: &schemas.ImageEditInput{Images: []schemas.ImageInput{{Image: tinyPNG}}},
	}}
	first := PopulateRequestAttributesWithMedia(req, trace, "primary-span")[schemas.AttrBifrostImageInput].(string)
	second := PopulateRequestAttributesWithMedia(req, trace, "fallback-span")[schemas.AttrBifrostImageInput].(string)
	assert.Contains(t, first, "bifrost-media://primary-span-input-image-0-")
	assert.Contains(t, second, "bifrost-media://primary-span-input-image-0-")
	assert.Len(t, trace.MediaAttachments(), 1)
}

func TestSafeTraceImageURLDropsCredentialsQueryAndFragment(t *testing.T) {
	raw := "https://user:pass@example.test/image.png?X-Amz-Signature=secret#fragment"
	assert.Empty(t, safeTraceImageURL(raw), "userinfo must reject the URL entirely")
	safe := safeTraceImageURL("https://example.test/image.png?X-Amz-Signature=secret#fragment")
	assert.Equal(t, "https://example.test/image.png", safe)
	assert.False(t, strings.Contains(safe, "secret"))
}
