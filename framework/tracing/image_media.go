package tracing

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const maxTraceMediaBytes = 20 << 20

// PopulateRequestAttributesWithMedia enriches the regular v2 request attributes
// with compact image summaries and bounded external attachments.
func PopulateRequestAttributesWithMedia(req *schemas.BifrostRequest, trace *schemas.Trace, spanID string) map[string]any {
	attrs := PopulateRequestAttributes(req)
	if req == nil {
		return attrs
	}
	switch req.RequestType {
	case schemas.ImageGenerationRequest, schemas.ImageGenerationStreamRequest:
		if request := req.ImageGenerationRequest; request != nil && request.Input != nil {
			input := map[string]any{"prompt": request.Input.Prompt}
			copyImageParameters(input, request.Params)
			if request.Params != nil && len(request.Params.InputImages) > 0 {
				images := make([]map[string]any, 0, len(request.Params.InputImages))
				for i, image := range request.Params.InputImages {
					images = append(images, summarizeImageInputString(trace, spanID, i, image))
				}
				input["input_images"] = images
				input["image_count"] = len(images)
			}
			attrs[schemas.AttrBifrostImageInput] = formatTraceValue(input)
		}
	case schemas.ImageEditRequest, schemas.ImageEditStreamRequest:
		if request := req.ImageEditRequest; request != nil && request.Input != nil {
			attrs[schemas.AttrBifrostImageInput] = formatTraceValue(imageEditInputSummary(request, trace, spanID))
		}
	case schemas.ImageVariationRequest:
		if request := req.ImageVariationRequest; request != nil && request.Input != nil {
			attrs[schemas.AttrBifrostImageInput] = formatTraceValue(imageVariationInputSummary(request, trace, spanID))
		}
	}
	return attrs
}

// PopulateResponseAttributesWithMedia enriches the regular v2 response attributes.
func PopulateResponseAttributesWithMedia(resp *schemas.BifrostResponse, trace *schemas.Trace, spanID string) map[string]any {
	attrs := PopulateResponseAttributes(resp)
	if resp == nil || resp.ImageGenerationResponse == nil {
		return attrs
	}
	imageResp := resp.ImageGenerationResponse
	attrs[schemas.AttrBifrostImageOutput] = formatTraceValue(imageResponseSummary(imageResp, trace, spanID))
	if imageResp.Model != "" {
		attrs[schemas.AttrResponseModel] = imageResp.Model
	}
	if usage := imageResp.Usage; usage != nil {
		attrs[schemas.AttrInputTokens] = usage.InputTokens
		attrs[schemas.AttrOutputTokens] = usage.OutputTokens
		attrs[schemas.AttrTotalTokens] = usage.TotalTokens
		if usage.InputTokensDetails != nil {
			attrs[schemas.AttrInputTokenDetailsImage] = usage.InputTokensDetails.ImageTokens
			attrs[schemas.AttrInputTokenDetailsText] = usage.InputTokensDetails.TextTokens
		}
		if usage.OutputTokensDetails != nil {
			attrs[schemas.AttrOutputTokenDetailsImage] = usage.OutputTokensDetails.ImageTokens
			attrs[schemas.AttrOutputTokenDetailsText] = usage.OutputTokensDetails.TextTokens
		}
	}
	return attrs
}

func copyImageParameters(input map[string]any, params *schemas.ImageGenerationParameters) {
	if params == nil {
		return
	}
	if params.Size != nil {
		input["size"] = *params.Size
	}
	if params.Quality != nil {
		input["quality"] = *params.Quality
	}
	if params.N != nil {
		input["n"] = *params.N
	}
}

func imageEditInputSummary(req *schemas.BifrostImageEditRequest, trace *schemas.Trace, spanID string) map[string]any {
	input := map[string]any{"prompt": req.Input.Prompt}
	images := make([]map[string]any, 0, len(req.Input.Images))
	for i, image := range req.Input.Images {
		if len(image.Image) > 0 {
			images = append(images, summarizeImageBytes(trace, spanID, "input", "image", i, image.Image))
		} else if safe := safeTraceImageURL(image.URL); safe != "" {
			images = append(images, map[string]any{"url": safe})
		} else {
			images = append(images, map[string]any{"capture_status": "invalid_url"})
		}
	}
	input["images"], input["image_count"] = images, len(images)
	if params := req.Params; params != nil {
		if len(params.Mask) > 0 {
			input["mask"] = summarizeImageBytes(trace, spanID, "input", "mask", 0, params.Mask)
		}
		if params.Size != nil {
			input["size"] = *params.Size
		}
		if params.Quality != nil {
			input["quality"] = *params.Quality
		}
		if params.N != nil {
			input["n"] = *params.N
		}
	}
	return input
}

func imageVariationInputSummary(req *schemas.BifrostImageVariationRequest, trace *schemas.Trace, spanID string) map[string]any {
	var image map[string]any
	if len(req.Input.Image.Image) > 0 {
		image = summarizeImageBytes(trace, spanID, "input", "image", 0, req.Input.Image.Image)
	} else if safe := safeTraceImageURL(req.Input.Image.URL); safe != "" {
		image = map[string]any{"url": safe}
	} else {
		image = map[string]any{"capture_status": "invalid_url"}
	}
	input := map[string]any{"images": []map[string]any{image}, "image_count": 1}
	if params := req.Params; params != nil {
		if params.Size != nil {
			input["size"] = *params.Size
		}
		if params.N != nil {
			input["n"] = *params.N
		}
	}
	return input
}

func summarizeImageInputString(trace *schemas.Trace, spanID string, index int, raw string) map[string]any {
	if safe := safeTraceImageURL(raw); safe != "" {
		return map[string]any{"url": safe}
	}
	return summarizeBase64Image(trace, spanID, "input", "image", index, raw)
}

func summarizeImageBytes(trace *schemas.Trace, spanID, field, role string, index int, data []byte) map[string]any {
	mimeType := strings.Split(http.DetectContentType(data), ";")[0]
	summary := map[string]any{"mime_type": mimeType, "bytes": len(data)}
	if !supportedTraceImageMIME(mimeType) {
		summary["capture_status"] = "unsupported_mime"
		return summary
	}
	if len(data) > maxTraceMediaBytes {
		summary["capture_status"] = "too_large"
		return summary
	}
	if trace == nil || spanID == "" {
		summary["capture_status"] = "metadata_only"
		return summary
	}
	digest := sha256.Sum256(data)
	sha := hex.EncodeToString(digest[:])
	summary["sha256"] = sha
	if ref, ok := trace.FindMediaRef(field, role, index, sha); ok {
		summary["media_ref"], summary["capture_status"] = ref, "captured"
		return summary
	}
	id := fmt.Sprintf("%s-%s-%s-%d-%s", spanID, field, role, index, sha[:16])
	status := trace.AddMediaWithStatus(schemas.TraceMedia{ID: id, SpanID: spanID, Field: field, Role: role, Index: index, MIMEType: mimeType, Bytes: len(data), SHA256: sha, Data: data})
	summary["capture_status"] = status
	if status == schemas.TraceMediaCaptureStatusCaptured {
		summary["media_ref"] = "bifrost-media://" + id
	}
	return summary
}

func imageResponseSummary(resp *schemas.BifrostImageGenerationResponse, trace *schemas.Trace, spanID string) map[string]any {
	images := make([]map[string]any, 0, len(resp.Data))
	for i, data := range resp.Data {
		image := map[string]any{"index": data.Index}
		if data.URL != "" {
			if safe := safeTraceImageURL(data.URL); safe != "" {
				image["url"] = safe
			} else {
				image["capture_status"] = "invalid_url"
			}
		}
		if data.B64JSON != "" {
			for key, value := range summarizeBase64Image(trace, spanID, "output", "image", i, data.B64JSON) {
				image[key] = value
			}
		}
		if data.RevisedPrompt != "" {
			image["revised_prompt"] = data.RevisedPrompt
		}
		images = append(images, image)
	}
	return map[string]any{"images": images, "image_count": len(images)}
}

func safeTraceImageURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	// Fragments and embedded credentials are never useful to observability.
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func summarizeBase64Image(trace *schemas.Trace, spanID, field, role string, index int, encoded string) map[string]any {
	raw := strings.TrimSpace(encoded)
	if strings.HasPrefix(raw, "data:") {
		comma := strings.IndexByte(raw, ',')
		if comma < 0 || !strings.Contains(raw[:comma], ";base64") {
			return map[string]any{"capture_status": "invalid_base64"}
		}
		raw = raw[comma+1:]
	}
	if trace == nil || spanID == "" {
		return map[string]any{"capture_status": "metadata_only"}
	}
	if estimate := decodedBase64SizeUpperBound(raw); estimate > maxTraceMediaBytes {
		return map[string]any{"bytes_estimate": estimate, "capture_status": "too_large"}
	}
	release, ok := trace.TryAcquireMediaDecode()
	if !ok {
		return map[string]any{"capture_status": "decode_saturated"}
	}
	defer release()
	data, total, digest, prefix, err := decodeTraceBase64(raw, base64.StdEncoding)
	if err != nil {
		data, total, digest, prefix, err = decodeTraceBase64(raw, base64.RawStdEncoding)
	}
	if err != nil || total == 0 {
		return map[string]any{"capture_status": "invalid_base64"}
	}
	mimeType := strings.Split(http.DetectContentType(prefix), ";")[0]
	summary := map[string]any{"mime_type": mimeType, "bytes": total, "sha256": digest}
	if !supportedTraceImageMIME(mimeType) {
		summary["capture_status"] = "unsupported_mime"
		return summary
	}
	id := fmt.Sprintf("%s-%s-%s-%d-%s", spanID, field, role, index, digest[:16])
	status := trace.AddOwnedMediaWithStatus(schemas.TraceMedia{ID: id, SpanID: spanID, Field: field, Role: role, Index: index, MIMEType: mimeType, Bytes: total, SHA256: digest, Data: data})
	summary["capture_status"] = status
	if status == schemas.TraceMediaCaptureStatusCaptured {
		summary["media_ref"] = "bifrost-media://" + id
	}
	return summary
}

func decodeTraceBase64(raw string, encoding *base64.Encoding) ([]byte, int, string, []byte, error) {
	decoder := base64.NewDecoder(encoding, strings.NewReader(raw))
	hasher := sha256.New()
	var captured bytes.Buffer
	prefix := make([]byte, 0, 512)
	buf := make([]byte, 32<<10)
	total := 0
	for {
		n, readErr := decoder.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = hasher.Write(chunk)
			if len(prefix) < 512 {
				prefix = append(prefix, chunk[:min(512-len(prefix), len(chunk))]...)
			}
			total += n
			if total <= maxTraceMediaBytes {
				_, _ = captured.Write(chunk)
			} else {
				captured.Reset()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, 0, "", nil, readErr
		}
	}
	return captured.Bytes(), total, hex.EncodeToString(hasher.Sum(nil)), prefix, nil
}

func decodedBase64SizeUpperBound(raw string) int {
	if raw == "" {
		return 0
	}
	padding := 0
	if raw[len(raw)-1] == '=' {
		padding++
		if len(raw) > 1 && raw[len(raw)-2] == '=' {
			padding++
		}
	}
	return (len(raw)*6+7)/8 - padding
}

func supportedTraceImageMIME(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
