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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	coreNetwork "github.com/maximhq/bifrost/core/network"
	"github.com/maximhq/bifrost/core/schemas"
)

const maxLangfuseMediaResponseBytes = 1 << 20

type mediaUploader interface {
	Upload(ctx context.Context, traceID string, media schemas.TraceMedia) (string, error)
	Close()
}

type langfuseMediaClient struct {
	client       *http.Client // configured collector control plane; operator-private is allowed
	uploadClient *http.Client // untrusted presigned upload URL; always SSRF guarded
	endpoint     string
	headers      map[string]string
	allowed      *mediaUploadOriginPolicy
}

var errMediaUploadOriginNotAllowed = errors.New("media upload origin is not allowed")
var errMediaUploadCircuitOpen = errors.New("media upload circuit is open")

type mediaUploadOriginError struct{ origin string }

func (e *mediaUploadOriginError) Error() string {
	return fmt.Sprintf("media upload origin is not allowed origin=%s", e.origin)
}

func (e *mediaUploadOriginError) Unwrap() error { return errMediaUploadOriginNotAllowed }

type mediaUploadOriginPolicy struct {
	origins   map[string]struct{}
	hosts     map[string]struct{}
	allowlist *coreNetwork.Allowlist
}

func newMediaUploadOriginPolicy(values []string) (*mediaUploadOriginPolicy, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > 16 {
		return nil, fmt.Errorf("media upload allowed origins supports at most 16 entries")
	}
	policy := &mediaUploadOriginPolicy{origins: make(map[string]struct{}), hosts: make(map[string]struct{})}
	allowEntries := make([]string, 0, len(values))
	for _, value := range values {
		u, err := url.Parse(strings.TrimSpace(value))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("media upload allowed origin %q must be an exact HTTPS origin without path, credentials, query, or fragment", value)
		}
		origin, host, err := canonicalMediaUploadOrigin(u)
		if err != nil {
			return nil, fmt.Errorf("media upload allowed origin %q: %w", value, err)
		}
		if _, exists := policy.origins[origin]; exists {
			continue
		}
		policy.origins[origin] = struct{}{}
		policy.hosts[host] = struct{}{}
		allowEntries = append(allowEntries, host)
	}
	allowlist, err := coreNetwork.NewAllowlist(allowEntries)
	if err != nil {
		return nil, fmt.Errorf("media upload allowed origins: %w", err)
	}
	policy.allowlist = allowlist
	return policy, nil
}

func canonicalMediaUploadOrigin(u *url.URL) (string, string, error) {
	if u == nil || u.Hostname() == "" {
		return "", "", fmt.Errorf("host is required")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else if u.Scheme == "http" {
			port = "80"
		} else {
			return "", "", fmt.Errorf("unsupported scheme %q", u.Scheme)
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", fmt.Errorf("port must be between 1 and 65535")
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(host, port), host, nil
}

func (p *mediaUploadOriginPolicy) permits(u *url.URL) bool {
	if p == nil || u == nil {
		return false
	}
	origin, _, err := canonicalMediaUploadOrigin(u)
	if err != nil {
		return false
	}
	_, ok := p.origins[origin]
	return ok
}

func (p *mediaUploadOriginPolicy) trustsHost(host string) bool {
	if p == nil {
		return false
	}
	_, ok := p.hosts[strings.ToLower(host)]
	return ok
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

func newLangfuseMediaClient(collectorURL string, headers map[string]string, timeout time.Duration, tlsCACert string, insecureMode bool, allowedOrigins []string) (*langfuseMediaClient, error) {
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
	uploadTransport := http.DefaultTransport.(*http.Transport).Clone()
	uploadTransport.TLSClientConfig = tlsConfig.Clone()
	// Presigned URLs are untrusted. A process-wide HTTP proxy would move the
	// destination lookup outside this SSRF guard, so media uploads always dial
	// the validated target directly.
	uploadTransport.Proxy = nil
	allowed, err := newMediaUploadOriginPolicy(allowedOrigins)
	if err != nil {
		return nil, err
	}
	if allowed == nil {
		uploadTransport.DialContext = coreNetwork.SSRFSafeDialContext(timeout)
	} else {
		uploadTransport.DialContext = coreNetwork.SSRFSafeDialContextWithAllowlist(timeout, allowed.allowlist)
	}
	return &langfuseMediaClient{
		client: &http.Client{Timeout: timeout, Transport: transport},
		uploadClient: &http.Client{
			Timeout: timeout, Transport: uploadTransport,
			CheckRedirect: rejectMediaUploadRedirect,
		},
		endpoint: endpoint,
		headers:  headers,
		allowed:  allowed,
	}, nil
}

func rejectMediaUploadRedirect(_ *http.Request, _ []*http.Request) error {
	return fmt.Errorf("media upload redirects are not allowed")
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
		return "", &mediaHTTPError{operation: "create", status: response.StatusCode}
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
	if c != nil && c.allowed != nil && c.allowed.trustsHost(u.Hostname()) && !c.allowed.permits(u) {
		origin, _, _ := canonicalMediaUploadOrigin(u)
		return 0, &mediaUploadOriginError{origin: origin}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("create media upload: %w", err)
	}
	request.Header.Set("Content-Type", mimeType)
	request.Header.Set("x-amz-checksum-sha256", checksum)
	startedAt := time.Now()
	uploadClient := c.uploadClient
	if uploadClient == nil {
		uploadTransport := http.DefaultTransport.(*http.Transport).Clone()
		uploadTransport.Proxy = nil
		uploadTransport.DialContext = coreNetwork.SSRFSafeDialContext(0)
		uploadClient = &http.Client{Transport: uploadTransport, CheckRedirect: rejectMediaUploadRedirect}
	}
	response, err := uploadClient.Do(request)
	if err != nil {
		return 0, mediaRequestError("content_upload", u.String(), startedAt, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxLangfuseMediaResponseBytes))
	response.Body.Close()
	if response.StatusCode/100 != 2 {
		return response.StatusCode, &mediaHTTPError{operation: "content_upload", status: response.StatusCode}
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
		return &mediaHTTPError{operation: "status_update", status: response.StatusCode}
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
		return &mediaHTTPError{operation: "status_verify", status: response.StatusCode}
	}
	var verified langfuseGetMediaResponse
	if err := sonic.Unmarshal(responseBody, &verified); err != nil || verified.MediaID != mediaID || verified.URL == "" || verified.UploadedAt == "" {
		return fmt.Errorf("verify media upload returned incomplete status")
	}
	return nil
}

type mediaNetworkError struct {
	operation string
	source    string
	host      string
	elapsedMS int64
}

func (e *mediaNetworkError) Error() string {
	return fmt.Sprintf("media request failed operation=%s source=%s host=%s elapsed_ms=%d", e.operation, e.source, e.host, e.elapsedMS)
}

type mediaHTTPError struct {
	operation string
	status    int
}

func (e *mediaHTTPError) Error() string {
	return fmt.Sprintf("media request failed operation=%s source=http_error status=%d", e.operation, e.status)
}

func mediaRequestError(operation, rawURL string, startedAt time.Time, err error) error {
	host := "unknown"
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	return &mediaNetworkError{operation: operation, source: classifyMediaNetworkError(err), host: host, elapsedMS: time.Since(startedAt).Milliseconds()}
}

func classifyMediaNetworkError(err error) string {
	switch {
	case errors.Is(err, coreNetwork.ErrBlockedNonPublicAddress):
		return "ssrf_blocked"
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

func mediaUploadFailureReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errMediaUploadOriginNotAllowed) {
		return "media_upload_origin_not_allowed"
	}
	if errors.Is(err, errMediaUploadCircuitOpen) {
		return "media_circuit_open"
	}
	var networkErr *mediaNetworkError
	if errors.As(err, &networkErr) {
		switch networkErr.source {
		case "ssrf_blocked":
			return "media_upload_ssrf_blocked"
		case "dns_error":
			return "media_upload_dns_error"
		case "tls_error":
			return "media_upload_tls_error"
		case "connection_refused":
			return "media_upload_connection_refused"
		case "connection_reset":
			return "media_upload_connection_reset"
		case "deadline_exceeded", "context_canceled", "network_timeout":
			return "media_upload_timeout"
		default:
			return "media_upload_network_error"
		}
	}
	var httpErr *mediaHTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.status == http.StatusForbidden:
			return "media_upload_http_403"
		case httpErr.status >= 500:
			return "media_upload_http_5xx"
		default:
			return "media_upload_http_error"
		}
	}
	return "media_upload_failed"
}

func (c *langfuseMediaClient) Close() {
	if c != nil && c.client != nil {
		c.client.CloseIdleConnections()
	}
	if c != nil && c.uploadClient != nil {
		c.uploadClient.CloseIdleConnections()
	}
}

// uploadTraceMedia uploads a target's attachments before the OTLP trace. A
// Langfuse record is never emitted with unresolved local references after an
// upload failure. Generic OTLP targets have no media endpoint and receive the
// compact summaries with local references stripped by the converter.
func uploadTraceMedia(ctx context.Context, target *otelTarget, trace *schemas.Trace) (map[string]string, error) {
	if target == nil || trace == nil || target.disableContentLogging {
		return nil, nil
	}
	attachments := trace.MediaAttachments()
	if len(attachments) == 0 || target.mediaUploader == nil {
		return nil, nil
	}
	if target.mediaBreakerOpen() {
		return nil, errMediaUploadCircuitOpen
	}
	target.ensureMediaRuntime()
	mediaSem := target.mediaSem
	refs := make(map[string]string, len(attachments))
	mediaCtx, cancel := context.WithTimeout(ctx, target.exportTimeout)
	defer cancel()
	for _, media := range attachments {
		select {
		case mediaSem <- struct{}{}:
		case <-mediaCtx.Done():
			target.tripMediaBreaker()
			return nil, mediaCtx.Err()
		}
		token, err := func() (string, error) {
			defer func() { <-mediaSem }()
			return target.mediaUploader.Upload(mediaCtx, trace.TraceID, media)
		}()
		if err != nil {
			target.tripMediaBreaker()
			if logger != nil {
				logger.Error("failed to upload trace media trace=%s field=%s role=%s: %v", trace.TraceID, media.Field, media.Role, err)
			}
			return nil, err
		}
		refs["bifrost-media://"+media.ID] = token
	}
	target.resetMediaBreaker()
	return refs, nil
}
