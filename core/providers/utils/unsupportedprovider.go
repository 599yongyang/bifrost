package utils

import (
	"context"

	"github.com/maximhq/bifrost/core/schemas"
)

// UnsupportedProvider supplies standard unsupported-operation implementations for
// narrowly scoped native providers. Embed it and override the operations the provider
// actually supports.
type UnsupportedProvider struct {
	providerKey schemas.ModelProvider
}

func NewUnsupportedProvider(providerKey schemas.ModelProvider) UnsupportedProvider {
	return UnsupportedProvider{providerKey: providerKey}
}

func (p UnsupportedProvider) GetProviderKey() schemas.ModelProvider { return p.providerKey }

func (p UnsupportedProvider) unsupported(requestType schemas.RequestType) *schemas.BifrostError {
	return NewUnsupportedOperationError(requestType, p.providerKey)
}

func (p UnsupportedProvider) ListModels(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ListModelsRequest)
}
func (p UnsupportedProvider) TextCompletion(*schemas.BifrostContext, schemas.Key, *schemas.BifrostTextCompletionRequest) (*schemas.BifrostTextCompletionResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.TextCompletionRequest)
}
func (p UnsupportedProvider) TextCompletionStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostTextCompletionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.TextCompletionStreamRequest)
}
func (p UnsupportedProvider) ChatCompletion(*schemas.BifrostContext, schemas.Key, *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ChatCompletionRequest)
}
func (p UnsupportedProvider) ChatCompletionStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostChatRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ChatCompletionStreamRequest)
}
func (p UnsupportedProvider) Responses(*schemas.BifrostContext, schemas.Key, *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ResponsesRequest)
}
func (p UnsupportedProvider) ResponsesStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostResponsesRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ResponsesStreamRequest)
}
func (p UnsupportedProvider) CountTokens(*schemas.BifrostContext, schemas.Key, *schemas.BifrostResponsesRequest) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.CountTokensRequest)
}
func (p UnsupportedProvider) Compaction(*schemas.BifrostContext, schemas.Key, *schemas.BifrostCompactionRequest) (*schemas.BifrostCompactionResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.CompactionRequest)
}
func (p UnsupportedProvider) Embedding(*schemas.BifrostContext, schemas.Key, *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.EmbeddingRequest)
}
func (p UnsupportedProvider) Rerank(*schemas.BifrostContext, schemas.Key, *schemas.BifrostRerankRequest) (*schemas.BifrostRerankResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.RerankRequest)
}
func (p UnsupportedProvider) OCR(*schemas.BifrostContext, schemas.Key, *schemas.BifrostOCRRequest) (*schemas.BifrostOCRResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.OCRRequest)
}
func (p UnsupportedProvider) Speech(*schemas.BifrostContext, schemas.Key, *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.SpeechRequest)
}
func (p UnsupportedProvider) SpeechStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostSpeechRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.SpeechStreamRequest)
}
func (p UnsupportedProvider) Transcription(*schemas.BifrostContext, schemas.Key, *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.TranscriptionRequest)
}
func (p UnsupportedProvider) TranscriptionStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostTranscriptionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.TranscriptionStreamRequest)
}
func (p UnsupportedProvider) ImageGeneration(*schemas.BifrostContext, schemas.Key, *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ImageGenerationRequest)
}
func (p UnsupportedProvider) ImageGenerationStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostImageGenerationRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ImageGenerationStreamRequest)
}
func (p UnsupportedProvider) ImageEdit(*schemas.BifrostContext, schemas.Key, *schemas.BifrostImageEditRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ImageEditRequest)
}
func (p UnsupportedProvider) ImageEditStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostImageEditRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ImageEditStreamRequest)
}
func (p UnsupportedProvider) ImageVariation(*schemas.BifrostContext, schemas.Key, *schemas.BifrostImageVariationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ImageVariationRequest)
}
func (p UnsupportedProvider) VideoGeneration(*schemas.BifrostContext, schemas.Key, *schemas.BifrostVideoGenerationRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.VideoGenerationRequest)
}
func (p UnsupportedProvider) VideoEdit(*schemas.BifrostContext, schemas.Key, *schemas.BifrostVideoEditRequest) (*schemas.BifrostVideoEditResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.VideoEditRequest)
}
func (p UnsupportedProvider) VideoRetrieve(*schemas.BifrostContext, schemas.Key, *schemas.BifrostVideoRetrieveRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.VideoRetrieveRequest)
}
func (p UnsupportedProvider) VideoDownload(*schemas.BifrostContext, schemas.Key, *schemas.BifrostVideoDownloadRequest) (*schemas.BifrostVideoDownloadResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.VideoDownloadRequest)
}
func (p UnsupportedProvider) VideoDelete(*schemas.BifrostContext, schemas.Key, *schemas.BifrostVideoDeleteRequest) (*schemas.BifrostVideoDeleteResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.VideoDeleteRequest)
}
func (p UnsupportedProvider) VideoList(*schemas.BifrostContext, schemas.Key, *schemas.BifrostVideoListRequest) (*schemas.BifrostVideoListResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.VideoListRequest)
}
func (p UnsupportedProvider) VideoRemix(*schemas.BifrostContext, schemas.Key, *schemas.BifrostVideoRemixRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.VideoRemixRequest)
}
func (p UnsupportedProvider) BatchCreate(*schemas.BifrostContext, schemas.Key, *schemas.BifrostBatchCreateRequest) (*schemas.BifrostBatchCreateResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.BatchCreateRequest)
}
func (p UnsupportedProvider) BatchList(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostBatchListRequest) (*schemas.BifrostBatchListResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.BatchListRequest)
}
func (p UnsupportedProvider) BatchRetrieve(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostBatchRetrieveRequest) (*schemas.BifrostBatchRetrieveResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.BatchRetrieveRequest)
}
func (p UnsupportedProvider) BatchCancel(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostBatchCancelRequest) (*schemas.BifrostBatchCancelResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.BatchCancelRequest)
}
func (p UnsupportedProvider) BatchDelete(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostBatchDeleteRequest) (*schemas.BifrostBatchDeleteResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.BatchDeleteRequest)
}
func (p UnsupportedProvider) BatchResults(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostBatchResultsRequest) (*schemas.BifrostBatchResultsResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.BatchResultsRequest)
}
func (p UnsupportedProvider) FileUpload(*schemas.BifrostContext, schemas.Key, *schemas.BifrostFileUploadRequest) (*schemas.BifrostFileUploadResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.FileUploadRequest)
}
func (p UnsupportedProvider) FileList(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostFileListRequest) (*schemas.BifrostFileListResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.FileListRequest)
}
func (p UnsupportedProvider) FileRetrieve(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostFileRetrieveRequest) (*schemas.BifrostFileRetrieveResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.FileRetrieveRequest)
}
func (p UnsupportedProvider) FileDelete(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostFileDeleteRequest) (*schemas.BifrostFileDeleteResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.FileDeleteRequest)
}
func (p UnsupportedProvider) FileContent(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostFileContentRequest) (*schemas.BifrostFileContentResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.FileContentRequest)
}
func (p UnsupportedProvider) CachedContentCreate(*schemas.BifrostContext, schemas.Key, *schemas.BifrostCachedContentCreateRequest) (*schemas.BifrostCachedContentCreateResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.CachedContentCreateRequest)
}
func (p UnsupportedProvider) CachedContentList(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostCachedContentListRequest) (*schemas.BifrostCachedContentListResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.CachedContentListRequest)
}
func (p UnsupportedProvider) CachedContentRetrieve(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostCachedContentRetrieveRequest) (*schemas.BifrostCachedContentRetrieveResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.CachedContentRetrieveRequest)
}
func (p UnsupportedProvider) CachedContentUpdate(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostCachedContentUpdateRequest) (*schemas.BifrostCachedContentUpdateResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.CachedContentUpdateRequest)
}
func (p UnsupportedProvider) CachedContentDelete(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostCachedContentDeleteRequest) (*schemas.BifrostCachedContentDeleteResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.CachedContentDeleteRequest)
}
func (p UnsupportedProvider) ContainerCreate(*schemas.BifrostContext, schemas.Key, *schemas.BifrostContainerCreateRequest) (*schemas.BifrostContainerCreateResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerCreateRequest)
}
func (p UnsupportedProvider) ContainerList(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostContainerListRequest) (*schemas.BifrostContainerListResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerListRequest)
}
func (p UnsupportedProvider) ContainerRetrieve(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostContainerRetrieveRequest) (*schemas.BifrostContainerRetrieveResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerRetrieveRequest)
}
func (p UnsupportedProvider) ContainerDelete(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostContainerDeleteRequest) (*schemas.BifrostContainerDeleteResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerDeleteRequest)
}
func (p UnsupportedProvider) ContainerFileCreate(*schemas.BifrostContext, schemas.Key, *schemas.BifrostContainerFileCreateRequest) (*schemas.BifrostContainerFileCreateResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerFileCreateRequest)
}
func (p UnsupportedProvider) ContainerFileList(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostContainerFileListRequest) (*schemas.BifrostContainerFileListResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerFileListRequest)
}
func (p UnsupportedProvider) ContainerFileRetrieve(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostContainerFileRetrieveRequest) (*schemas.BifrostContainerFileRetrieveResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerFileRetrieveRequest)
}
func (p UnsupportedProvider) ContainerFileContent(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostContainerFileContentRequest) (*schemas.BifrostContainerFileContentResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerFileContentRequest)
}
func (p UnsupportedProvider) ContainerFileDelete(*schemas.BifrostContext, []schemas.Key, *schemas.BifrostContainerFileDeleteRequest) (*schemas.BifrostContainerFileDeleteResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.ContainerFileDeleteRequest)
}
func (p UnsupportedProvider) Passthrough(*schemas.BifrostContext, schemas.Key, *schemas.BifrostPassthroughRequest) (*schemas.BifrostPassthroughResponse, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.PassthroughRequest)
}
func (p UnsupportedProvider) PassthroughStream(*schemas.BifrostContext, schemas.PostHookRunner, func(context.Context), schemas.Key, *schemas.BifrostPassthroughRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, p.unsupported(schemas.PassthroughStreamRequest)
}

var _ schemas.Provider = UnsupportedProvider{}
