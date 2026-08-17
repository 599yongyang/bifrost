package otel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
)

const maxLangfuseMediaResponseBytes = 1 << 20

type mediaUploader interface {
	Upload(ctx context.Context, traceID string, media schemas.TraceMedia) (string, error)
	Close()
}

type langfuseMediaClient struct {
	client   *http.Client
	endpoint string
	headers  map[string]string
}

type langfuseCreateMediaRequest struct {
	TraceID       string `json:"traceId"`
	ObservationID string `json:"observationId,omitempty"`
	ContentType   string `json:"contentType"`
	ContentLength int    `json:"contentLength"`
	SHA256Hash    string `json:"sha256Hash"`
	Field         string `json:"field"`
}

type langfuseCreateMediaResponse struct {
	MediaID   string  `json:"mediaId"`
	UploadURL *string `json:"uploadUrl"`
}

type langfuseGetMediaResponse struct {
	MediaID    string `json:"mediaId"`
	URL        string `json:"url"`
	UploadedAt string `json:"uploadedAt"`
}

func newLangfuseMediaClient(collectorURL string, headers map[string]string, timeout time.Duration, tlsCACert string, insecureMode bool) (*langfuseMediaClient, error) {
	endpoint := inferLangfuseMediaEndpoint(collectorURL)
	if endpoint == "" {
		return nil, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig, err := buildTLSConfig(tlsCACert, insecureMode)
	if err != nil {
		return nil, err
	}
	transport.TLSClientConfig = tlsConfig
	return &langfuseMediaClient{
		client:   &http.Client{Timeout: timeout, Transport: transport},
		endpoint: endpoint,
		headers:  headers,
	}, nil
}

func inferLangfuseMediaEndpoint(collectorURL string) string {
	u, err := url.Parse(collectorURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	marker := "/api/public/otel"
	index := strings.Index(u.Path, marker)
	if index < 0 {
		return ""
	}
	u.Path = strings.TrimSuffix(u.Path[:index], "/") + "/api/public/media"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func (c *langfuseMediaClient) Upload(ctx context.Context, traceID string, media schemas.TraceMedia) (string, error) {
	if c == nil || c.client == nil || len(media.Data) == 0 {
		return "", fmt.Errorf("media uploader is not configured")
	}
	digest := sha256.Sum256(media.Data)
	checksum := base64.StdEncoding.EncodeToString(digest[:])
	body, err := sonic.Marshal(langfuseCreateMediaRequest{
		TraceID: traceID, ObservationID: media.SpanID, ContentType: media.MIMEType,
		ContentLength: len(media.Data), SHA256Hash: checksum, Field: media.Field,
	})
	if err != nil {
		return "", fmt.Errorf("marshal media request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create media request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range c.headers {
		if !strings.EqualFold(key, "content-type") {
			request.Header.Set(key, value)
		}
	}
	startedAt := time.Now()
	response, err := c.client.Do(request)
	if err != nil {
		return "", mediaRequestError("create", c.endpoint, startedAt, err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxLangfuseMediaResponseBytes))
	response.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read media response: %w", readErr)
	}
	if response.StatusCode/100 != 2 {
		return "", fmt.Errorf("initialize media upload returned status %d", response.StatusCode)
	}
	var created langfuseCreateMediaResponse
	if err := sonic.Unmarshal(responseBody, &created); err != nil || created.MediaID == "" {
		return "", fmt.Errorf("decode media response")
	}
	if created.UploadURL != nil && *created.UploadURL != "" {
		status, uploadErr := c.putMedia(ctx, *created.UploadURL, media.MIMEType, checksum, media.Data)
		var patchErr error
		if status > 0 {
			patchErr = c.patchMedia(ctx, created.MediaID, status, uploadErr)
		}
		if uploadErr != nil {
			return "", uploadErr
		}
		if patchErr != nil {
			return "", patchErr
		}
	}
	if err := c.verifyMedia(ctx, created.MediaID); err != nil {
		return "", err
	}
	return fmt.Sprintf("@@@langfuseMedia:type=%s|id=%s|source=bytes@@@", media.MIMEType, created.MediaID), nil
}

func (c *langfuseMediaClient) putMedia(ctx context.Context, uploadURL, mimeType, checksum string, data []byte) (int, error) {
	u, err := url.Parse(uploadURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return 0, fmt.Errorf("invalid media upload URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("create media upload: %w", err)
	}
	request.Header.Set("Content-Type", mimeType)
	request.Header.Set("x-amz-checksum-sha256", checksum)
	startedAt := time.Now()
	response, err := c.client.Do(request)
	if err != nil {
		return 0, mediaRequestError("content_upload", u.String(), startedAt, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxLangfuseMediaResponseBytes))
	response.Body.Close()
	if response.StatusCode/100 != 2 {
		return response.StatusCode, fmt.Errorf("upload media returned status %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

func (c *langfuseMediaClient) patchMedia(ctx context.Context, mediaID string, uploadStatus int, uploadErr error) error {
	statusBody := map[string]any{
		"uploadedAt":       time.Now().UTC().Format(time.RFC3339Nano),
		"uploadHttpStatus": uploadStatus,
	}
	if uploadErr != nil {
		statusBody["uploadHttpError"] = "media upload failed"
	}
	body, err := sonic.Marshal(statusBody)
	if err != nil {
		return fmt.Errorf("marshal media status: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.endpoint+"/"+url.PathEscape(mediaID), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create media status request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range c.headers {
		if !strings.EqualFold(key, "content-type") {
			request.Header.Set(key, value)
		}
	}
	startedAt := time.Now()
	response, err := c.client.Do(request)
	if err != nil {
		return mediaRequestError("status_update", request.URL.String(), startedAt, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxLangfuseMediaResponseBytes))
	response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("report media status returned status %d", response.StatusCode)
	}
	return nil
}

func (c *langfuseMediaClient) verifyMedia(ctx context.Context, mediaID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/"+url.PathEscape(mediaID), nil)
	if err != nil {
		return fmt.Errorf("create media verification request: %w", err)
	}
	for key, value := range c.headers {
		if !strings.EqualFold(key, "content-type") {
			request.Header.Set(key, value)
		}
	}
	startedAt := time.Now()
	response, err := c.client.Do(request)
	if err != nil {
		return mediaRequestError("status_verify", request.URL.String(), startedAt, err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxLangfuseMediaResponseBytes))
	response.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read media verification response: %w", readErr)
	}
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("verify media upload returned status %d", response.StatusCode)
	}
	var verified langfuseGetMediaResponse
	if err := sonic.Unmarshal(responseBody, &verified); err != nil || verified.MediaID != mediaID || verified.URL == "" || verified.UploadedAt == "" {
		return fmt.Errorf("verify media upload returned incomplete status")
	}
	return nil
}

func mediaRequestError(operation, rawURL string, startedAt time.Time, err error) error {
	host := "unknown"
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	return fmt.Errorf("media request failed operation=%s source=%s host=%s elapsed_ms=%d",
		operation, classifyMediaNetworkError(err), host, time.Since(startedAt).Milliseconds())
}

func classifyMediaNetworkError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "connection_reset"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_error"
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return "tls_error"
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return "tls_error"
	}
	var recordHeaderErr tls.RecordHeaderError
	if errors.As(err, &recordHeaderErr) {
		return "tls_error"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "network_timeout"
	}
	return "network_error"
}

func (c *langfuseMediaClient) Close() {
	if c != nil && c.client != nil {
		c.client.CloseIdleConnections()
	}
}
