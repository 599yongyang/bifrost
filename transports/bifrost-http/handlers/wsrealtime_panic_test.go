package handlers

import (
	"context"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type realtimePanicAccount struct{}

func (realtimePanicAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return nil, nil
}
func (realtimePanicAccount) GetKeysForProvider(context.Context, schemas.ModelProvider) ([]schemas.Key, error) {
	return nil, nil
}
func (realtimePanicAccount) GetConfigForProvider(schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	return nil, configstore.ErrNotFound
}

type realtimePanicPlugin struct{}

func (*realtimePanicPlugin) GetName() string { return "realtime-panic" }
func (*realtimePanicPlugin) Cleanup() error  { return nil }
func (*realtimePanicPlugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	panic("secret realtime routing panic")
}
func (*realtimePanicPlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (*realtimePanicPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}

func TestWSRealtimeRejectsPreRequestPluginPanicBeforeUpgrade(t *testing.T) {
	SetLogger(&mockLogger{})
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: realtimePanicAccount{}, Logger: bifrost.NewDefaultLogger(schemas.LogLevelError),
		LLMPlugins: []schemas.LLMPlugin{&realtimePanicPlugin{}},
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)

	config := &lib.Config{ClientConfig: &configstore.ClientConfig{AllowedOrigins: []string{"*"}}}
	handler := &WSRealtimeHandler{client: client, config: config, handlerStore: testWSHandlerStore{}}
	var request fasthttp.Request
	request.Header.SetMethod(fasthttp.MethodGet)
	request.SetRequestURI("/openai/v1/realtime?model=gpt-realtime")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&request, nil, nil)

	handler.handleUpgrade(ctx)
	require.Equal(t, fasthttp.StatusInternalServerError, ctx.Response.StatusCode())
	require.NotContains(t, string(ctx.Response.Body()), "secret")
	require.NotEqual(t, "websocket", string(ctx.Response.Header.Peek("Upgrade")))
}
