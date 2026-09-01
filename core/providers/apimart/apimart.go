package apimart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/network"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

const (
	defaultAPIMartBaseURL                  = "https://api.apimart.ai"
	defaultAPIMartInitialPollDelay         = 10 * time.Second
	defaultAPIMartPollInterval             = 4 * time.Second
	defaultAPIMartDownloadRetryDelay       = 500 * time.Millisecond
	defaultAPIMartDownloadAttempts         = 3
	defaultAPIMartMaxImageBytes      int64 = 25 * 1024 * 1024
	defaultAPIMartMaxTotalBytes      int64 = 64 * 1024 * 1024
)

type imageDownloader func(context.Context, string, int64) (string, int64, error)

type APIMartProvider struct {
	providerUtils.UnsupportedProvider
	logger              schemas.Logger
	client              *fasthttp.Client
	streamingClient     *fasthttp.Client
	networkConfig       schemas.NetworkConfig
	sendBackRawRequest  bool
	sendBackRawResponse bool
	initialPollDelay    time.Duration
	pollInterval        time.Duration
	downloadRetryDelay  time.Duration
	downloadAttempts    int
	maxImageBytes       int64
	maxTotalBytes       int64
	downloadImage       imageDownloader
}

func NewAPIMartProvider(config *schemas.ProviderConfig, logger schemas.Logger) (*APIMartProvider, error) {
	config.CheckAndSetDefaults()
	requestTimeout := time.Duration(config.NetworkConfig.DefaultRequestTimeoutInSeconds) * time.Second
	client := &fasthttp.Client{
		ReadTimeout:         requestTimeout,
		WriteTimeout:        requestTimeout,
		MaxConnsPerHost:     config.NetworkConfig.MaxConnsPerHost,
		MaxIdleConnDuration: time.Duration(config.NetworkConfig.KeepAliveTimeoutInSeconds) * time.Second,
		MaxConnWaitTimeout:  requestTimeout,
		MaxConnDuration:     time.Duration(schemas.DefaultMaxConnDurationInSeconds) * time.Second,
		ConnPoolStrategy:    fasthttp.FIFO,
	}
	client = providerUtils.ConfigureProxy(client, config.ProxyConfig, logger)
	client = providerUtils.ConfigureDialer(client, config.NetworkConfig.AllowPrivateNetwork)
	client = providerUtils.ConfigureTLS(client, config.NetworkConfig, logger)
	streamingClient := providerUtils.BuildStreamingClient(client)

	if config.NetworkConfig.BaseURL == "" {
		config.NetworkConfig.BaseURL = defaultAPIMartBaseURL
	}
	config.NetworkConfig.BaseURL = strings.TrimRight(config.NetworkConfig.BaseURL, "/")
	imageClient, err := providerUtils.NewImageDownloadClient(config.NetworkConfig)
	if err != nil {
		return nil, err
	}

	return &APIMartProvider{
		UnsupportedProvider: providerUtils.NewUnsupportedProvider(schemas.APIMart),
		logger:              logger,
		client:              client,
		streamingClient:     streamingClient,
		networkConfig:       config.NetworkConfig,
		sendBackRawRequest:  config.SendBackRawRequest,
		sendBackRawResponse: config.SendBackRawResponse,
		initialPollDelay:    defaultAPIMartInitialPollDelay,
		pollInterval:        defaultAPIMartPollInterval,
		downloadRetryDelay:  defaultAPIMartDownloadRetryDelay,
		downloadAttempts:    defaultAPIMartDownloadAttempts,
		maxImageBytes:       defaultAPIMartMaxImageBytes,
		maxTotalBytes:       defaultAPIMartMaxTotalBytes,
		downloadImage: func(ctx context.Context, imageURL string, maxBytes int64) (string, int64, error) {
			return providerUtils.FetchImageAndEncodeURLWithClient(ctx, imageURL, maxBytes, imageClient)
		},
	}, nil
}

func (provider *APIMartProvider) GetProviderKey() schemas.ModelProvider { return schemas.APIMart }

func (provider *APIMartProvider) ImageGeneration(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	converted, err := ToAPIMartImageGenerationRequest(request)
	if err != nil {
		return nil, newAPIMartError(http.StatusBadRequest, &APIMartErrorDetail{Code: "build_request_failed", Type: "invalid_request_error", Message: err.Error()}, err.Error())
	}
	responseFormat := imageGenerationResponseFormat(request.Params)
	return provider.executeImageTask(ctx, key, request.Model, responseFormat, converted)
}

func (provider *APIMartProvider) ImageEdit(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageEditRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	converted, err := ToAPIMartImageEditRequest(request)
	if err != nil {
		return nil, newAPIMartError(http.StatusBadRequest, &APIMartErrorDetail{Code: "build_request_failed", Type: "invalid_request_error", Message: err.Error()}, err.Error())
	}
	responseFormat := imageEditResponseFormat(request.Params)
	return provider.executeImageTask(ctx, key, request.Model, responseFormat, converted)
}

func imageGenerationResponseFormat(params *schemas.ImageGenerationParameters) string {
	if params != nil && params.ResponseFormat != nil && *params.ResponseFormat != "" {
		return *params.ResponseFormat
	}
	return "b64_json"
}

func imageEditResponseFormat(params *schemas.ImageEditParameters) string {
	if params != nil && params.ResponseFormat != nil && *params.ResponseFormat != "" {
		return *params.ResponseFormat
	}
	return "b64_json"
}

func (provider *APIMartProvider) executeImageTask(ctx *schemas.BifrostContext, key schemas.Key, model, responseFormat string, request *APIMartImageRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	operationCtx, cancel := schemas.NewBifrostContextWithTimeout(ctx, time.Duration(provider.networkConfig.DefaultRequestTimeoutInSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	if responseFormat != "url" && responseFormat != "b64_json" {
		return nil, newAPIMartError(http.StatusBadRequest, &APIMartErrorDetail{Code: "invalid_response_format", Type: "invalid_request_error", Message: "response_format must be url or b64_json"}, "invalid response format")
	}
	for _, imageURL := range request.ImageURLs {
		if strings.HasPrefix(imageURL, "data:") {
			continue
		}
		if err := network.ValidatePublicURL(operationCtx, imageURL); err != nil {
			return nil, newAPIMartError(http.StatusBadRequest, &APIMartErrorDetail{Code: "invalid_image_url", Type: "invalid_request_error", Message: fmt.Sprintf("input image URL is not public: %s", providerUtils.RedactURLForError(imageURL))}, err.Error())
		}
	}

	requestBody, err := providerUtils.MarshalSorted(request)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
	}
	taskID, bifrostErr := provider.submitTask(operationCtx, key, requestBody)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	task, polls, bifrostErr := provider.pollTask(operationCtx, key, taskID)
	if bifrostErr != nil {
		provider.logger.LogHTTPRequest(schemas.LogLevelDebug, "APIMart image task polling stopped").
			Str("task_id", taskID).
			Str("status", "error").
			Int("poll_count", polls).
			Int64("duration_ms", time.Since(started).Milliseconds()).
			Send()
		var sanitizedErrorBody []byte
		if providerUtils.ShouldSendBackRawResponse(operationCtx, provider.sendBackRawResponse) {
			includeTaskMetadata, _ := operationCtx.Value(schemas.BifrostContextKeyDropRawResponseFromClient).(bool)
			if task != nil {
				sanitizedErrorBody, _ = providerUtils.MarshalSorted(sanitizedAPIMartTaskResponse(task, includeTaskMetadata))
			} else {
				sanitizedErrorBody, _ = providerUtils.MarshalSorted(struct {
					Error *schemas.ErrorField `json:"error"`
				}{Error: bifrostErr.Error})
			}
		}
		return nil, providerUtils.EnrichError(operationCtx, bifrostErr, requestBody, sanitizedErrorBody, provider.sendBackRawRequest, provider.sendBackRawResponse, time.Since(started))
	}
	provider.logger.LogHTTPRequest(schemas.LogLevelDebug, "APIMart image task reached terminal state").
		Str("task_id", taskID).
		Str("status", task.Status).
		Int("poll_count", polls).
		Int64("duration_ms", time.Since(started).Milliseconds()).
		Send()

	urls, err := flattenAPIMartImageURLs(task)
	if err != nil {
		return nil, providerUtils.EnrichError(operationCtx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), requestBody, nil, provider.sendBackRawRequest, false, time.Since(started))
	}
	data, bifrostErr := provider.buildImageData(operationCtx, urls, responseFormat)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(operationCtx, bifrostErr, requestBody, nil, provider.sendBackRawRequest, false, time.Since(started))
	}
	created := task.Completed
	if created == 0 {
		created = time.Now().Unix()
	}
	response := &schemas.BifrostImageGenerationResponse{Created: created, Model: model, Data: data}
	response.ExtraFields.Latency = time.Since(started).Milliseconds()
	if providerUtils.ShouldSendBackRawRequest(operationCtx, provider.sendBackRawRequest) {
		response.ExtraFields.RawRequest = json.RawMessage(append([]byte(nil), requestBody...))
	}
	if providerUtils.ShouldSendBackRawResponse(operationCtx, provider.sendBackRawResponse) {
		includeTaskMetadata, _ := operationCtx.Value(schemas.BifrostContextKeyDropRawResponseFromClient).(bool)
		response.ExtraFields.RawResponse = sanitizedAPIMartTaskResponse(task, includeTaskMetadata)
	}
	return response, nil
}

func sanitizedAPIMartTaskResponse(task *APIMartTask, includeTaskMetadata bool) APIMartRawImageResponse {
	data := APIMartSanitizedTaskData{}
	if task == nil {
		return APIMartRawImageResponse{Code: http.StatusOK, Data: data}
	}
	data.Created = task.Created
	data.Completed = task.Completed
	data.ActualTime = task.ActualTime
	data.EstimatedTime = task.EstimatedTime
	data.Error = task.Error
	if task.Result != nil {
		images := make([]APIMartTaskImage, len(task.Result.Images))
		for i, image := range task.Result.Images {
			images[i].ExpiresAt = image.ExpiresAt
			for _, imageURL := range image.URLs {
				images[i].URLs = append(images[i].URLs, providerUtils.RedactURLForError(imageURL))
			}
		}
		data.Result = &APIMartTaskResult{Images: images}
	}
	if includeTaskMetadata {
		data.ID = task.ID
		data.Status = task.Status
		data.Progress = task.Progress
	}
	return APIMartRawImageResponse{Code: http.StatusOK, Data: data}
}

func (provider *APIMartProvider) buildImageData(ctx context.Context, urls []string, responseFormat string) ([]schemas.ImageData, *schemas.BifrostError) {
	data := make([]schemas.ImageData, 0, len(urls))
	if responseFormat == "url" {
		for index, imageURL := range urls {
			if apimartURLContainsCredentials(imageURL) {
				return nil, providerUtils.NewBifrostOperationError("APIMart returned a credential-bearing image URL that cannot be exposed in url response mode", nil)
			}
			data = append(data, schemas.ImageData{URL: imageURL, Index: index})
		}
		return data, nil
	}

	remainingEncoded := provider.maxTotalBytes
	for index, imageURL := range urls {
		// Base64 expands bytes by roughly 4/3. Convert the remaining encoded
		// response budget into the maximum decoded bytes this fetch may consume.
		limit := min(provider.maxImageBytes, (remainingEncoded/4)*3)
		if limit <= 0 {
			return nil, providerUtils.NewBifrostOperationError("APIMart images exceed total encoded response size limit", nil)
		}
		encoded, _, err := provider.downloadWithRetry(ctx, imageURL, limit)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, apimartContextError(err)
			}
			return nil, providerUtils.NewBifrostOperationError("failed to download APIMart result image", err)
		}
		if int64(len(encoded)) > remainingEncoded {
			return nil, providerUtils.NewBifrostOperationError("APIMart images exceed total encoded response size limit", nil)
		}
		remainingEncoded -= int64(len(encoded))
		data = append(data, schemas.ImageData{B64JSON: encoded, Index: index})
	}
	return data, nil
}

func (provider *APIMartProvider) downloadWithRetry(ctx context.Context, imageURL string, maxBytes int64) (string, int64, error) {
	var lastErr error
	for attempt := 1; attempt <= provider.downloadAttempts; attempt++ {
		encoded, size, err := provider.downloadImage(ctx, imageURL, maxBytes)
		if err == nil {
			return encoded, size, nil
		}
		lastErr = err
		if attempt == provider.downloadAttempts || !providerUtils.IsRetryableImageFetchError(err) {
			break
		}
		timer := time.NewTimer(provider.downloadRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return "", 0, ctx.Err()
		case <-timer.C:
		}
	}
	return "", 0, lastErr
}

func decodeAPIMartResponse(body []byte, target interface{}) error {
	if len(body) == 0 {
		return fmt.Errorf("empty APIMart response")
	}
	return sonic.Unmarshal(body, target)
}

var _ schemas.Provider = (*APIMartProvider)(nil)
