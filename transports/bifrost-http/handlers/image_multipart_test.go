package handlers

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

func imageMultipartContext(t *testing.T, fields map[string]string, files map[string][]byte) *fasthttp.RequestCtx {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	for field, data := range files {
		part, err := writer.CreateFormFile(field, field+".png")
		if err != nil {
			t.Fatalf("create file %s: %v", field, err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write file %s: %v", field, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.Header.SetContentType(writer.FormDataContentType())
	ctx.Request.SetBody(body.Bytes())
	return ctx
}

func TestPrepareImageEditRequestPreservesMultipartMedia(t *testing.T) {
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'i', 'm', 'a', 'g', 'e'}
	mask := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'm', 'a', 's', 'k'}
	ctx := imageMultipartContext(t, map[string]string{
		"model": "openai/gpt-image-1", "prompt": "replace the sky", "size": "1024x1024", "quality": "high", "n": "1",
	}, map[string][]byte{"image": image, "mask": mask})

	_, req, err := prepareImageEditRequest(ctx, &lib.Config{})
	if err != nil {
		t.Fatalf("prepare image edit: %v", err)
	}
	if req.Input == nil || len(req.Input.Images) != 1 || !bytes.Equal(req.Input.Images[0].Image, image) {
		t.Fatalf("edit input image was not preserved: %#v", req.Input)
	}
	if req.Params == nil || !bytes.Equal(req.Params.Mask, mask) || req.Input.Prompt != "replace the sky" {
		t.Fatalf("edit input fields were not preserved: input=%#v params=%#v", req.Input, req.Params)
	}
}

func TestPrepareImageVariationRequestPreservesMultipartMedia(t *testing.T) {
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'v', 'a', 'r'}
	ctx := imageMultipartContext(t, map[string]string{
		"model": "openai/dall-e-2", "size": "1024x1024", "n": "1",
	}, map[string][]byte{"image": image})

	req, err := prepareImageVariationRequest(ctx, &lib.Config{})
	if err != nil {
		t.Fatalf("prepare image variation: %v", err)
	}
	if req.Input == nil || !bytes.Equal(req.Input.Image.Image, image) {
		t.Fatalf("variation input image was not preserved: %#v", req.Input)
	}
}
