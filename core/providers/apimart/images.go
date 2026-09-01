package apimart

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

const (
	maxAPIMartInputImages           = 16
	maxAPIMartInputImageBytes int64 = 20 * 1024 * 1024
	maxAPIMartTotalInputBytes int64 = 256 * 1024 * 1024
)

var supportedAPIMartImageMIMEs = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/webp": {},
	"image/gif":  {},
}

func ToAPIMartImageGenerationRequest(request *schemas.BifrostImageGenerationRequest) (*APIMartImageRequest, error) {
	if request == nil || request.Input == nil {
		return nil, fmt.Errorf("input is required")
	}
	converted := &APIMartImageRequest{Model: request.Model, Prompt: request.Input.Prompt}
	if request.Params == nil {
		return converted, nil
	}
	if err := copyAPIMartImageParams(converted, request.Params.N, request.Params.Size); err != nil {
		return nil, err
	}
	if len(request.Params.InputImages) > maxAPIMartInputImages {
		return nil, fmt.Errorf("input_images exceeds max %d", maxAPIMartInputImages)
	}
	var totalInputBytes int64
	for _, image := range request.Params.InputImages {
		validated, imageBytes, err := validateAPIMartImageReference(image)
		if err != nil {
			return nil, fmt.Errorf("invalid input image: %w", err)
		}
		totalInputBytes += imageBytes
		if totalInputBytes > maxAPIMartTotalInputBytes {
			return nil, fmt.Errorf("input images exceed %d-byte total limit", maxAPIMartTotalInputBytes)
		}
		converted.ImageURLs = append(converted.ImageURLs, validated)
	}
	return converted, nil
}

func ToAPIMartImageEditRequest(request *schemas.BifrostImageEditRequest) (*APIMartImageRequest, error) {
	if request == nil || request.Input == nil {
		return nil, fmt.Errorf("input is required")
	}
	if request.Params != nil && len(request.Params.Mask) > 0 {
		return nil, fmt.Errorf("mask image edits are not supported by APIMart")
	}
	converted := &APIMartImageRequest{Model: request.Model, Prompt: request.Input.Prompt}
	if len(request.Input.Images) > maxAPIMartInputImages {
		return nil, fmt.Errorf("images exceeds max %d", maxAPIMartInputImages)
	}
	if request.Params != nil {
		if err := copyAPIMartImageParams(converted, request.Params.N, request.Params.Size); err != nil {
			return nil, err
		}
	}
	var totalInputBytes int64
	for _, image := range request.Input.Images {
		var reference string
		var imageBytes int64
		var err error
		switch {
		case image.URL != "":
			reference, imageBytes, err = validateAPIMartImageReference(image.URL)
		case len(image.Image) > 0:
			reference, err = APIMartImageBytesToDataURI(image.Image)
			imageBytes = int64(len(image.Image))
		default:
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("invalid edit image: %w", err)
		}
		totalInputBytes += imageBytes
		if totalInputBytes > maxAPIMartTotalInputBytes {
			return nil, fmt.Errorf("images exceed %d-byte total limit", maxAPIMartTotalInputBytes)
		}
		converted.ImageURLs = append(converted.ImageURLs, reference)
	}
	if len(converted.ImageURLs) == 0 {
		return nil, fmt.Errorf("at least one image is required")
	}
	return converted, nil
}

func copyAPIMartImageParams(request *APIMartImageRequest, n *int, size *string) error {
	request.N = n
	if size == nil || *size == "" {
		return nil
	}
	if err := validateAPIMartPixelSize(*size); err != nil {
		return err
	}
	request.Size = size
	return nil
}

func validateAPIMartPixelSize(size string) error {
	widthText, heightText, found := strings.Cut(size, "x")
	if !found || widthText == "" || heightText == "" || strings.Contains(heightText, "x") {
		return fmt.Errorf("unsupported APIMart size %q: expected WIDTHxHEIGHT pixels", size)
	}
	width, widthErr := strconv.Atoi(widthText)
	height, heightErr := strconv.Atoi(heightText)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return fmt.Errorf("unsupported APIMart size %q: expected positive WIDTHxHEIGHT pixels", size)
	}
	return nil
}

func validateAPIMartImageReference(raw string) (string, int64, error) {
	sanitized, err := schemas.SanitizeImageURL(raw)
	if err != nil {
		return "", 0, err
	}
	if !strings.HasPrefix(sanitized, "data:") {
		return sanitized, 0, nil
	}
	mediaType, isBase64, payload, ok := schemas.ParseDataURL(sanitized)
	if !ok || !isBase64 {
		return "", 0, fmt.Errorf("image data URI must use base64 encoding")
	}
	if _, ok := supportedAPIMartImageMIMEs[mediaType]; !ok {
		return "", 0, fmt.Errorf("unsupported image MIME type %q", mediaType)
	}
	maxEncodedBytes := base64.StdEncoding.EncodedLen(int(maxAPIMartInputImageBytes))
	if len(payload) > maxEncodedBytes+1024 {
		return "", 0, fmt.Errorf("input image exceeds %d-byte limit", maxAPIMartInputImageBytes)
	}
	encodedBytes := 0
	for _, r := range payload {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		encodedBytes++
		if encodedBytes > maxEncodedBytes {
			return "", 0, fmt.Errorf("input image exceeds %d-byte limit", maxAPIMartInputImageBytes)
		}
	}
	cleanPayload := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, payload)
	estimatedBytes := base64.StdEncoding.DecodedLen(len(cleanPayload))
	if strings.HasSuffix(cleanPayload, "=") {
		estimatedBytes--
	}
	if strings.HasSuffix(cleanPayload, "==") {
		estimatedBytes--
	}
	if int64(estimatedBytes) > maxAPIMartInputImageBytes {
		return "", 0, fmt.Errorf("input image exceeds %d-byte limit", maxAPIMartInputImageBytes)
	}
	decoded, err := base64.StdEncoding.DecodeString(cleanPayload)
	if err != nil {
		return "", 0, fmt.Errorf("invalid base64 image data: %w", err)
	}
	if int64(len(decoded)) > maxAPIMartInputImageBytes {
		return "", 0, fmt.Errorf("input image exceeds %d-byte limit", maxAPIMartInputImageBytes)
	}
	if detected := http.DetectContentType(decoded); detected != mediaType {
		return "", 0, fmt.Errorf("image MIME type %q does not match detected content %q", mediaType, detected)
	}
	return "data:" + mediaType + ";base64," + cleanPayload, int64(len(decoded)), nil
}

func APIMartImageBytesToDataURI(image []byte) (string, error) {
	if int64(len(image)) > maxAPIMartInputImageBytes {
		return "", fmt.Errorf("input image exceeds %d-byte limit", maxAPIMartInputImageBytes)
	}
	mediaType := http.DetectContentType(image)
	if _, ok := supportedAPIMartImageMIMEs[mediaType]; !ok {
		return "", fmt.Errorf("unsupported image MIME type %q", mediaType)
	}
	return providerUtils.FileBytesToBase64DataURL(image), nil
}

func flattenAPIMartImageURLs(task *APIMartTask) ([]string, error) {
	if task == nil || task.Result == nil {
		return nil, fmt.Errorf("completed APIMart task returned no result")
	}
	urls := make([]string, 0, len(task.Result.Images))
	for _, image := range task.Result.Images {
		for _, rawURL := range image.URLs {
			if rawURL != "" {
				urls = append(urls, rawURL)
			}
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("completed APIMart task returned no image URLs")
	}
	return urls, nil
}

func apimartURLContainsCredentials(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	if parsed.User != nil {
		return true
	}
	for key := range parsed.Query() {
		normalized := strings.ToLower(key)
		if normalized == "sig" || normalized == "sas" || normalized == "policy" || normalized == "key-pair-id" || normalized == "auth" || normalized == "auth_key" || normalized == "authkey" ||
			strings.Contains(normalized, "signature") || strings.Contains(normalized, "credential") || strings.Contains(normalized, "token") {
			return true
		}
	}
	return false
}
