package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/circuitbreaker"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
)

type circuitTestAccount struct {
	config *schemas.ProviderConfig
	keys   []schemas.Key
}

func (a *circuitTestAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{schemas.OpenAI}, nil
}

func (a *circuitTestAccount) GetKeysForProvider(context.Context, schemas.ModelProvider) ([]schemas.Key, error) {
	return a.keys, nil
}

func (a *circuitTestAccount) GetConfigForProvider(schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	return a.config, nil
}

type circuitOrderSentinel struct{}

func (*circuitOrderSentinel) GetName() string { return "provider-selection-sentinel" }
func (*circuitOrderSentinel) Cleanup() error  { return nil }

func serverCircuitConfig() *circuitbreaker.Config {
	value := "true"
	return &circuitbreaker.Config{Policies: []circuitbreaker.Policy{{
		Name: "key-circuit", PrimaryProvider: schemas.OpenAI, PrimaryModel: "ptu", PrimaryKeyIDs: []string{"key-1", "key-2"},
		FallbackProvider: schemas.OpenAI, FallbackModel: "paygo",
		Condition: circuitbreaker.Condition{Signals: []circuitbreaker.Signal{{
			Source: "response_header", HeaderName: "x-spill", HeaderValue: &value,
		}}},
	}}}
}

func serverCircuitRequest() *schemas.BifrostRequest {
	return &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest, ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "ptu"}}
}

func serverCircuitContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
}

func TestLoadBuiltinCircuitBreakerPlugin(t *testing.T) {
	plugin, err := loadBuiltinPlugin(context.Background(), circuitbreaker.PluginName, serverCircuitConfig(), &lib.Config{})
	require.NoError(t, err)
	require.Equal(t, circuitbreaker.PluginName, plugin.GetName())
	_, ok := plugin.(schemas.LLMPlugin)
	require.True(t, ok)
}

func TestCircuitBreakerKeyPoolFilterTracksReloadAndRemoval(t *testing.T) {
	oldPlugin, err := circuitbreaker.Init(serverCircuitConfig(), nil)
	require.NoError(t, err)
	config := &lib.Config{}
	plugins := []schemas.BasePlugin{oldPlugin}
	config.BasePlugins.Store(&plugins)
	server := &BifrostHTTPServer{Config: config}
	filter := server.circuitBreakerKeyPoolFilter()
	keys := []schemas.Key{{ID: "key-1"}}

	tripCtx := serverCircuitContext()
	require.NoError(t, oldPlugin.PreRequestHook(tripCtx, serverCircuitRequest()))
	tripCtx.SetValue(schemas.BifrostContextKeySelectedKeyID, "key-1")
	_, _, err = oldPlugin.PostLLMHook(tripCtx, &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{
		ExtraFields: schemas.BifrostResponseExtraFields{
			RoutingInfo:             schemas.RoutingInfo{Provider: schemas.OpenAI, Model: "ptu"},
			ProviderResponseHeaders: map[string]string{"x-spill": "true"},
		},
	}}, nil)
	require.NoError(t, err)

	oldCtx := serverCircuitContext()
	require.NoError(t, oldPlugin.PreRequestHook(oldCtx, serverCircuitRequest()))
	filtered, err := filter(oldCtx, schemas.OpenAI, "ptu", keys)
	require.NoError(t, err)
	require.Empty(t, filtered)

	newPlugin, err := circuitbreaker.Init(serverCircuitConfig(), nil)
	require.NoError(t, err)
	plugins = []schemas.BasePlugin{newPlugin}
	config.BasePlugins.Store(&plugins)
	newCtx := serverCircuitContext()
	require.NoError(t, newPlugin.PreRequestHook(newCtx, serverCircuitRequest()))
	filtered, err = filter(newCtx, schemas.OpenAI, "ptu", keys)
	require.NoError(t, err)
	require.Equal(t, keys, filtered, "the stable closure must resolve the reloaded instance")

	plugins = nil
	config.BasePlugins.Store(&plugins)
	filtered, err = filter(newCtx, schemas.OpenAI, "ptu", keys)
	require.NoError(t, err)
	require.Equal(t, keys, filtered, "removed or disabled plugin must restore unfiltered keys")
}

func TestConfiguredCircuitBreakerKeyPoolFilterEnabledAndDisabled(t *testing.T) {
	server := &BifrostHTTPServer{Config: &lib.Config{}}
	require.Nil(t, server.configuredCircuitBreakerKeyPoolFilter())

	plugin, err := circuitbreaker.Init(serverCircuitConfig(), nil)
	require.NoError(t, err)
	plugins := []schemas.BasePlugin{plugin}
	server.Config.BasePlugins.Store(&plugins)
	require.NotNil(t, server.configuredCircuitBreakerKeyPoolFilter())

	plugins = nil
	server.Config.BasePlugins.Store(&plugins)
	require.Nil(t, server.configuredCircuitBreakerKeyPoolFilter())
}

func TestCircuitBreakerSyncReloadAndRemoveRefreshesCoreFilter(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"success","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	account := &circuitTestAccount{
		config: &schemas.ProviderConfig{
			NetworkConfig:            schemas.NetworkConfig{BaseURL: upstream.URL, DefaultRequestTimeoutInSeconds: 5, MaxRetries: 0},
			ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{Concurrency: 1, BufferSize: 1},
		},
		keys: []schemas.Key{{ID: "key-1", Value: *schemas.NewSecretVar("sk-test"), Models: schemas.WhiteList{"*"}, Weight: 100}},
	}
	client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account: account, Logger: bifrost.NewDefaultLogger(schemas.LogLevelError),
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)

	config := &lib.Config{}
	sentinel := &circuitOrderSentinel{}
	require.NoError(t, config.ReloadPlugin(sentinel))
	config.SetPluginOrderInfo(sentinel.GetName(), schemas.Ptr(schemas.PluginPlacementPostBuiltin), schemas.Ptr(math.MaxInt-1))
	server := &BifrostHTTPServer{Config: config, Client: client, Ctx: serverCircuitContext()}

	oldPlugin, err := circuitbreaker.Init(serverCircuitConfig(), nil)
	require.NoError(t, err)
	requestedPlacement := schemas.PluginPlacementPreBuiltin
	require.NoError(t, server.SyncLoadedPlugin(context.Background(), circuitbreaker.PluginName, oldPlugin, &requestedPlacement, schemas.Ptr(-100)))
	require.Equal(t, []string{sentinel.GetName(), circuitbreaker.PluginName}, config.GetPluginOrder(), "circuit ordering must be forced after provider selection")

	tripCtx := serverCircuitContext()
	require.NoError(t, oldPlugin.PreRequestHook(tripCtx, serverCircuitRequest()))
	tripCtx.SetValue(schemas.BifrostContextKeySelectedKeyID, "key-1")
	_, _, err = oldPlugin.PostLLMHook(tripCtx, &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{ExtraFields: schemas.BifrostResponseExtraFields{
		RoutingInfo: schemas.RoutingInfo{Provider: schemas.OpenAI, Model: "ptu"}, ProviderResponseHeaders: map[string]string{"x-spill": "true"},
	}}}, nil)
	require.NoError(t, err)

	request := func() (*schemas.BifrostChatResponse, *schemas.BifrostError) {
		ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(5*time.Second))
		return client.ChatCompletionRequest(ctx, &schemas.BifrostChatRequest{
			Provider: schemas.OpenAI, Model: "ptu",
			Input: []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr("hi")}}},
		})
	}
	response, bifrostErr := request()
	require.Nil(t, response)
	require.NotNil(t, bifrostErr)
	var errorType string
	if bifrostErr.Type != nil {
		errorType = *bifrostErr.Type
	} else if bifrostErr.Error != nil && bifrostErr.Error.Type != nil {
		errorType = *bifrostErr.Error.Type
	}
	require.Equal(t, "no_eligible_keys", errorType, "%+v", bifrostErr)
	require.Zero(t, upstreamHits.Load(), "suppressed key must not call the provider")

	newPlugin, err := circuitbreaker.Init(serverCircuitConfig(), nil)
	require.NoError(t, err)
	require.NoError(t, server.SyncLoadedPlugin(context.Background(), circuitbreaker.PluginName, newPlugin, nil, nil))
	response, bifrostErr = request()
	require.Nil(t, bifrostErr)
	require.NotNil(t, response)
	require.Equal(t, int32(1), upstreamHits.Load(), "reload must use the fresh circuit instance")

	require.NoError(t, server.RemovePlugin(context.Background(), circuitbreaker.PluginName))
	response, bifrostErr = request()
	require.Nil(t, bifrostErr)
	require.NotNil(t, response)
	require.Equal(t, int32(2), upstreamHits.Load(), "removal must clear the key filter")
}
