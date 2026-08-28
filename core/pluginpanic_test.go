package bifrost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type panicTestPlugin struct {
	name             string
	panicGetName     bool
	panicPreRequest  bool
	panicPre         bool
	panicPost        bool
	panicCleanup     bool
	mutatePreRequest func(*schemas.BifrostContext, *schemas.BifrostRequest)
	mutatePre        func(*schemas.BifrostRequest)
	post             func(*schemas.BifrostResponse, *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError)
	events           *[]string
}

type panicTestMCPPlugin struct {
	schemas.MCPPluginNoOpHooks
	*panicTestPlugin
}

func (p *panicTestPlugin) GetName() string {
	if p.panicGetName {
		panic("secret get-name panic")
	}
	return p.name
}

func (p *panicTestPlugin) PreRequestHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) error {
	if p.events != nil {
		*p.events = append(*p.events, p.name+".pre_request")
	}
	if p.mutatePreRequest != nil {
		p.mutatePreRequest(ctx, req)
	}
	if p.panicPreRequest {
		panic("secret pre-request panic")
	}
	return nil
}

func (p *panicTestPlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if p.events != nil {
		*p.events = append(*p.events, p.name+".pre")
	}
	if p.mutatePre != nil {
		p.mutatePre(req)
	}
	if p.panicPre {
		panic("secret pre panic")
	}
	return req, nil, nil
}

func (p *panicTestPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if p.events != nil {
		*p.events = append(*p.events, p.name+".post")
	}
	if p.panicPost {
		panic("secret post panic")
	}
	if p.post != nil {
		resp, bifrostErr = p.post(resp, bifrostErr)
	}
	return resp, bifrostErr, nil
}

func (p *panicTestPlugin) Cleanup() error {
	if p.events != nil {
		*p.events = append(*p.events, p.name+".cleanup")
	}
	if p.panicCleanup {
		panic("secret cleanup panic")
	}
	return nil
}

func newPanicPipeline(plugins ...schemas.LLMPlugin) *PluginPipeline {
	return &PluginPipeline{
		llmPlugins:     plugins,
		logger:         NewDefaultLogger(schemas.LogLevelError),
		tracer:         &schemas.NoOpTracer{},
		preHookErrors:  make([]error, 0),
		postHookErrors: make([]error, 0),
	}
}

func TestPreRequestHookPanicRollsBackRoutingState(t *testing.T) {
	originalFallbacks := []schemas.Fallback{{Provider: schemas.Anthropic, Model: "ordinary"}}
	originalErrorFallbacks := []schemas.ErrorFallbackRule{{Name: "original", Scenario: schemas.FailureCategoryRateLimit, Fallbacks: []schemas.Fallback{{Provider: schemas.Azure, Model: "dedicated"}}}}
	req := &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest, ChatRequest: &schemas.BifrostChatRequest{
		Provider: schemas.OpenAI, Model: "original", Fallbacks: originalFallbacks, ErrorFallbacks: originalErrorFallbacks,
	}}
	plugin := &panicTestPlugin{name: "panic-routing", panicPreRequest: true, mutatePreRequest: func(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) {
		req.SetProvider(schemas.Groq)
		req.SetModel("mutated")
		req.SetFallbacks([]schemas.Fallback{{Provider: schemas.XAI, Model: "mutated"}})
		req.SetErrorFallbacks([]schemas.ErrorFallbackRule{{Name: "mutated"}})
		ctx.SetValue(schemas.BifrostContextKeyRoutingPinnedAPIKeyID, "mutated-pin")
	}}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyRoutingPinnedAPIKeyID, "original-pin")

	panicErr := newPanicPipeline(plugin).RunPreRequestHooks(ctx, req)
	assertPluginPanicError(t, panicErr)
	provider, model, fallbacks := req.GetRequestFields()
	assert.Equal(t, schemas.OpenAI, provider)
	assert.Equal(t, "original", model)
	assert.Equal(t, originalFallbacks, fallbacks)
	assert.Equal(t, originalErrorFallbacks, req.GetErrorFallbacks())
	assert.Equal(t, "original-pin", GetStringFromContext(ctx, schemas.BifrostContextKeyRoutingPinnedAPIKeyID))
}

func TestHandleRequestPreRequestPanicNeverCallsProvider(t *testing.T) {
	var providerHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"unexpected","choices":[]}`))
	}))
	defer server.Close()

	account := NewMockAccount()
	configureFallbackTestProvider(account, schemas.OpenAI, server.URL)
	plugin := &panicTestPlugin{name: "panic-routing", panicPreRequest: true, mutatePreRequest: func(_ *schemas.BifrostContext, req *schemas.BifrostRequest) {
		req.SetProvider(schemas.Groq)
		req.SetModel("mutated")
		req.SetFallbacks([]schemas.Fallback{{Provider: schemas.XAI, Model: "fallback"}})
		req.SetErrorFallbacks([]schemas.ErrorFallbackRule{{Name: "error-fallback"}})
	}}
	client, err := Init(context.Background(), schemas.BifrostConfig{
		Account: account, Logger: NewDefaultLogger(schemas.LogLevelError), LLMPlugins: []schemas.LLMPlugin{plugin},
	})
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)

	response, bifrostErr := client.ChatCompletionRequest(
		schemas.NewBifrostContext(context.Background(), time.Now().Add(10*time.Second)),
		&schemas.BifrostChatRequest{
			Provider: schemas.OpenAI, Model: "gpt-4o-mini",
			Input: []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: Ptr("hi")}}},
		},
	)
	assert.Nil(t, response)
	assertPluginPanicError(t, bifrostErr)
	assert.Zero(t, providerHits.Load())
}

func TestPreLLMHookPanicRunsOnlyPreviouslyEnteredPostHooks(t *testing.T) {
	var events []string
	first := &panicTestPlugin{name: "first", events: &events, post: func(_ *schemas.BifrostResponse, _ *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
		return &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{}}, nil
	}}
	panicking := &panicTestPlugin{name: "panicking", panicPre: true, events: &events, mutatePre: func(req *schemas.BifrostRequest) {
		req.SetProvider(schemas.Groq)
		req.SetModel("mutated")
		req.SetFallbacks([]schemas.Fallback{{Provider: schemas.XAI, Model: "mutated"}})
		req.SetErrorFallbacks([]schemas.ErrorFallbackRule{{Name: "mutated"}})
	}}
	pipeline := newPanicPipeline(first, panicking)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	req := &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest, ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "gpt"}}

	_, shortCircuit, count := pipeline.RunLLMPreHooks(ctx, req)
	require.NotNil(t, shortCircuit)
	assertPluginPanicError(t, shortCircuit.Error)
	assert.Equal(t, 1, count)
	provider, model, fallbacks := req.GetRequestFields()
	assert.Equal(t, schemas.OpenAI, provider)
	assert.Equal(t, "gpt", model)
	assert.Nil(t, fallbacks)
	assert.Nil(t, req.GetErrorFallbacks())
	resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, nil, shortCircuit.Error, count)
	assert.Nil(t, resp, "an earlier plugin must not recover a plugin_panic")
	assertPluginPanicError(t, bifrostErr)
	assert.Equal(t, []string{"first.pre", "panicking.pre", "first.post"}, events)
}

func TestPostLLMHookPanicFailsClosedAndStopsReverseChain(t *testing.T) {
	var events []string
	first := &panicTestPlugin{name: "first", events: &events}
	panicking := &panicTestPlugin{name: "panicking", panicPost: true, events: &events}
	pipeline := newPanicPipeline(first, panicking)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	input := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{}}

	resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, input, nil, 2)
	assert.Nil(t, resp)
	assertPluginPanicError(t, bifrostErr)
	assert.Equal(t, []string{"panicking.post"}, events)
	assert.NotContains(t, bifrostErr.GetErrorString(), "secret post panic")
}

func TestStreamingPostLLMHookPanicFailsClosed(t *testing.T) {
	panicking := &panicTestPlugin{name: "stream-panicking", panicPost: true}
	pipeline := newPanicPipeline(panicking)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeyStreamStartTime, time.Now())
	input := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{}}

	resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, input, nil, 1)
	assert.Nil(t, resp)
	assertPluginPanicError(t, bifrostErr)
	assert.Equal(t, 1, pipeline.GetChunkCount())
}

func TestPluginGetNamePanicIsContained(t *testing.T) {
	pipeline := newPanicPipeline(&panicTestPlugin{panicGetName: true})
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	req := &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest, ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "gpt"}}

	_, shortCircuit, count := pipeline.RunLLMPreHooks(ctx, req)
	require.NotNil(t, shortCircuit)
	assertPluginPanicError(t, shortCircuit.Error)
	assert.Zero(t, count)
}

func TestInitWithPanickingPluginNameDoesNotCrash(t *testing.T) {
	plugin := &panicTestPlugin{panicGetName: true}
	var client *Bifrost
	var initErr error
	require.NotPanics(t, func() {
		client, initErr = Init(context.Background(), schemas.BifrostConfig{
			Account: NewMockAccount(), Logger: NewDefaultLogger(schemas.LogLevelError), LLMPlugins: []schemas.LLMPlugin{plugin},
		})
	})
	require.NoError(t, initErr)
	t.Cleanup(client.Shutdown)

	req := &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest, ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "gpt"}}
	panicErr := client.RunPreRequestHooksWithError(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), req)
	assertPluginPanicError(t, panicErr)
}

func TestPluginCleanupPanicDoesNotStopShutdown(t *testing.T) {
	var events []string
	panicking := &panicTestPlugin{name: "panic-cleanup", panicCleanup: true, events: &events}
	after := &panicTestPlugin{name: "after", events: &events}
	client, err := Init(context.Background(), schemas.BifrostConfig{
		Account: NewMockAccount(), Logger: NewDefaultLogger(schemas.LogLevelError), LLMPlugins: []schemas.LLMPlugin{panicking, after},
	})
	require.NoError(t, err)
	require.NotPanics(t, client.Shutdown)
	assert.Equal(t, []string{"panic-cleanup.cleanup", "after.cleanup"}, events)
}

func TestReloadAndRemoveContainLifecyclePanics(t *testing.T) {
	client := &Bifrost{logger: NewDefaultLogger(schemas.LogLevelError)}
	plugins := []schemas.LLMPlugin{&panicTestPlugin{name: "cleanup", panicCleanup: true}}
	client.llmPlugins.Store(&plugins)

	require.NotPanics(t, func() {
		require.NoError(t, client.RemovePlugin("cleanup", []schemas.PluginType{schemas.PluginTypeLLM}))
	})
	err := client.ReloadPlugin(&panicTestPlugin{panicGetName: true}, []schemas.PluginType{schemas.PluginTypeLLM})
	require.Error(t, err)
}

func TestRemovePurgesAlreadyLoadedCorruptedPlugins(t *testing.T) {
	var events []string
	corrupted := &panicTestPlugin{name: "corrupted", panicGetName: true, events: &events}
	normal := &panicTestPlugin{name: "normal"}
	client := &Bifrost{logger: NewDefaultLogger(schemas.LogLevelError)}
	llmPlugins := []schemas.LLMPlugin{corrupted, normal}
	client.llmPlugins.Store(&llmPlugins)

	require.NotPanics(t, func() {
		require.NoError(t, client.RemovePlugin("missing", []schemas.PluginType{schemas.PluginTypeLLM}))
	})
	got := *client.llmPlugins.Load()
	require.Len(t, got, 1)
	assert.Same(t, normal, got[0])
	assert.Equal(t, []string{"corrupted.cleanup"}, events)

	mcpCorrupted := &panicTestMCPPlugin{panicTestPlugin: &panicTestPlugin{name: "mcp-corrupted", panicGetName: true, events: &events}}
	mcpNormal := &panicTestMCPPlugin{panicTestPlugin: &panicTestPlugin{name: "mcp-normal"}}
	mcpPlugins := []schemas.MCPPlugin{mcpCorrupted, mcpNormal}
	client.mcpPlugins.Store(&mcpPlugins)
	require.NotPanics(t, func() {
		require.NoError(t, client.RemovePlugin("missing", []schemas.PluginType{schemas.PluginTypeMCP}))
	})
	gotMCP := *client.mcpPlugins.Load()
	require.Len(t, gotMCP, 1)
	assert.Same(t, mcpNormal, gotMCP[0])
	assert.Contains(t, events, "mcp-corrupted.cleanup")
}

func TestReloadPurgesCorruptedAndDuplicateExistingPlugins(t *testing.T) {
	var events []string
	corrupted := &panicTestPlugin{name: "corrupted", panicGetName: true, events: &events}
	oldOne := &panicTestPlugin{name: "target", events: &events}
	oldDuplicate := &panicTestPlugin{name: "target", events: &events}
	normal := &panicTestPlugin{name: "normal"}
	replacement := &panicTestPlugin{name: "target"}
	client := &Bifrost{logger: NewDefaultLogger(schemas.LogLevelError)}
	llmPlugins := []schemas.LLMPlugin{corrupted, oldOne, oldDuplicate, normal}
	client.llmPlugins.Store(&llmPlugins)

	require.NoError(t, client.ReloadPlugin(replacement, []schemas.PluginType{schemas.PluginTypeLLM}))
	got := *client.llmPlugins.Load()
	require.Len(t, got, 2)
	assert.Same(t, replacement, got[0])
	assert.Same(t, normal, got[1])
	assert.Equal(t, []string{"corrupted.cleanup", "target.cleanup", "target.cleanup"}, events)

	mcpCorrupted := &panicTestMCPPlugin{panicTestPlugin: &panicTestPlugin{name: "mcp-corrupted", panicGetName: true}}
	mcpOld := &panicTestMCPPlugin{panicTestPlugin: &panicTestPlugin{name: "mcp-target"}}
	mcpReplacement := &panicTestMCPPlugin{panicTestPlugin: &panicTestPlugin{name: "mcp-target"}}
	mcpPlugins := []schemas.MCPPlugin{mcpCorrupted, mcpOld}
	client.mcpPlugins.Store(&mcpPlugins)
	require.NoError(t, client.ReloadPlugin(mcpReplacement, []schemas.PluginType{schemas.PluginTypeMCP}))
	gotMCP := *client.mcpPlugins.Load()
	require.Len(t, gotMCP, 1)
	assert.Same(t, mcpReplacement, gotMCP[0])
}

func TestReorderPluginsKeepsCorruptedPluginsAtStableTail(t *testing.T) {
	b := &panicTestPlugin{name: "b"}
	corruptedOne := &panicTestPlugin{name: "corrupted-one", panicGetName: true}
	a := &panicTestPlugin{name: "a"}
	corruptedTwo := &panicTestPlugin{name: "corrupted-two", panicGetName: true}
	client := &Bifrost{logger: NewDefaultLogger(schemas.LogLevelError)}
	plugins := []schemas.LLMPlugin{b, corruptedOne, a, corruptedTwo}
	client.llmPlugins.Store(&plugins)

	require.NotPanics(t, func() { client.ReorderPlugins([]string{"a", "b"}) })
	got := *client.llmPlugins.Load()
	require.Len(t, got, 4)
	assert.Same(t, a, got[0])
	assert.Same(t, b, got[1])
	assert.Same(t, corruptedOne, got[2])
	assert.Same(t, corruptedTwo, got[3])
}

func assertPluginPanicError(t *testing.T, err *schemas.BifrostError) {
	t.Helper()
	require.NotNil(t, err)
	require.NotNil(t, err.Error)
	require.NotNil(t, err.Error.Type)
	assert.Equal(t, "plugin_panic", *err.Error.Type)
	assert.Equal(t, "plugin execution failed unexpectedly", err.Error.Message)
	require.NotNil(t, err.AllowFallbacks)
	assert.False(t, *err.AllowFallbacks)
	assert.False(t, strings.Contains(err.GetErrorString(), "secret"))
}
