package bifrost

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mcpPanicPlugin struct {
	name               string
	panicPre           bool
	panicPost          bool
	panicConnectPre    bool
	panicConnectPost   bool
	preErr             error
	postErr            error
	connectPreErr      error
	connectPostErr     error
	recoverPost        bool
	recoverConnectPost bool
	events             *[]string
	lastCtx            *schemas.BifrostContext
}

func (p *mcpPanicPlugin) GetName() string { return p.name }
func (p *mcpPanicPlugin) Cleanup() error  { return nil }

func (p *mcpPanicPlugin) PreMCPHook(ctx *schemas.BifrostContext, req *schemas.BifrostMCPRequest) (*schemas.BifrostMCPRequest, *schemas.MCPPluginShortCircuit, error) {
	p.record(ctx, "pre")
	if p.panicPre {
		panic("secret MCP pre panic")
	}
	return req, nil, p.preErr
}

func (p *mcpPanicPlugin) PostMCPHook(ctx *schemas.BifrostContext, resp *schemas.BifrostMCPResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostMCPResponse, *schemas.BifrostError, error) {
	p.record(ctx, "post")
	if p.panicPost {
		panic("secret MCP post panic")
	}
	if p.recoverPost {
		return &schemas.BifrostMCPResponse{}, nil, p.postErr
	}
	return resp, bifrostErr, p.postErr
}

func (p *mcpPanicPlugin) PreMCPConnectionHook(ctx *schemas.BifrostContext, req *schemas.BifrostMCPConnectRequest) (*schemas.BifrostMCPConnectRequest, *schemas.MCPConnectionShortCircuit, error) {
	p.record(ctx, "connect_pre")
	if p.panicConnectPre {
		panic("secret MCP connect pre panic")
	}
	return req, nil, p.connectPreErr
}

func (p *mcpPanicPlugin) PostMCPConnectionHook(ctx *schemas.BifrostContext, resp *schemas.BifrostMCPConnectResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostMCPConnectResponse, *schemas.BifrostError, error) {
	p.record(ctx, "connect_post")
	if p.panicConnectPost {
		panic("secret MCP connect post panic")
	}
	if p.recoverConnectPost {
		return &schemas.BifrostMCPConnectResponse{}, nil, p.connectPostErr
	}
	return resp, bifrostErr, p.connectPostErr
}

func (p *mcpPanicPlugin) record(ctx *schemas.BifrostContext, hook string) {
	p.lastCtx = ctx
	if p.events != nil {
		*p.events = append(*p.events, p.name+"."+hook)
	}
}

func newMCPPanicPipeline(plugins ...schemas.MCPPlugin) *PluginPipeline {
	return &PluginPipeline{
		mcpPlugins:     plugins,
		logger:         NewDefaultLogger(schemas.LogLevelError),
		tracer:         &mcpChildSpanTracer{},
		preHookErrors:  make([]error, 0),
		postHookErrors: make([]error, 0),
	}
}

type mcpChildSpanTracer struct{ schemas.NoOpTracer }

func (*mcpChildSpanTracer) StartSpanID(_ context.Context, _ string, _ schemas.SpanKind) (string, schemas.SpanHandle) {
	return "child-span", "child-handle"
}

func TestMCPEnvelopePrePanicUnwindsOnlySuccessfulHooks(t *testing.T) {
	var events []string
	first := &mcpPanicPlugin{name: "first", recoverPost: true, events: &events}
	panicking := &mcpPanicPlugin{name: "panicking", panicPre: true, events: &events}
	pipeline := newMCPPanicPipeline(first, panicking, &mcpPanicPlugin{name: "after", events: &events})
	ctx := mcpPanicContext()
	req := &schemas.BifrostMCPRequest{RequestType: schemas.MCPRequestTypePing, BifrostMCPPingRequest: &schemas.BifrostMCPPingRequest{}}

	_, shortCircuit, count := pipeline.RunMCPPreHooks(ctx, req)
	require.NotNil(t, shortCircuit)
	assertPluginPanicError(t, shortCircuit.Error)
	assert.Equal(t, 1, count)
	assert.Equal(t, "parent-span", GetStringFromContext(ctx, schemas.BifrostContextKeySpanID))
	assertScopedContextReleased(t, panicking.lastCtx)

	resp, bifrostErr := pipeline.RunMCPPostHooks(ctx, nil, shortCircuit.Error, count)
	assert.Nil(t, resp)
	assertPluginPanicError(t, bifrostErr)
	assert.Equal(t, []string{"first.pre", "panicking.pre", "first.post"}, events)
	assert.Equal(t, "parent-span", GetStringFromContext(ctx, schemas.BifrostContextKeySpanID))
}

func TestMCPEnvelopePostPanicFailsClosedAndStopsChain(t *testing.T) {
	var events []string
	first := &mcpPanicPlugin{name: "first", events: &events}
	panicking := &mcpPanicPlugin{name: "panicking", panicPost: true, events: &events}
	pipeline := newMCPPanicPipeline(first, panicking)
	ctx := mcpPanicContext()

	resp, bifrostErr := pipeline.RunMCPPostHooks(ctx, &schemas.BifrostMCPResponse{}, nil, 2)
	assert.Nil(t, resp)
	assertPluginPanicError(t, bifrostErr)
	assert.Equal(t, []string{"panicking.post"}, events)
	assertScopedContextReleased(t, panicking.lastCtx)
	assert.Equal(t, "parent-span", GetStringFromContext(ctx, schemas.BifrostContextKeySpanID))
}

func TestMCPConnectionPrePanicUnwindsOnlySuccessfulHooks(t *testing.T) {
	var events []string
	first := &mcpPanicPlugin{name: "first", recoverConnectPost: true, events: &events}
	panicking := &mcpPanicPlugin{name: "panicking", panicConnectPre: true, events: &events}
	pipeline := newMCPPanicPipeline(first, panicking, &mcpPanicPlugin{name: "after", events: &events})
	ctx := mcpPanicContext()
	req := &schemas.BifrostMCPConnectRequest{ClientName: "client"}

	_, shortCircuit, count := pipeline.RunMCPPreConnectionHooks(ctx, req)
	require.NotNil(t, shortCircuit)
	assertPluginPanicError(t, shortCircuit.Error)
	assert.Equal(t, 1, count)
	assertScopedContextReleased(t, panicking.lastCtx)
	assert.Equal(t, "parent-span", GetStringFromContext(ctx, schemas.BifrostContextKeySpanID))

	resp, bifrostErr := pipeline.RunMCPPostConnectionHooks(ctx, nil, shortCircuit.Error, count)
	assert.Nil(t, resp)
	assertPluginPanicError(t, bifrostErr)
	assert.Equal(t, []string{"first.connect_pre", "panicking.connect_pre", "first.connect_post"}, events)
}

func TestMCPConnectionPostPanicFailsClosedAndStopsChain(t *testing.T) {
	var events []string
	first := &mcpPanicPlugin{name: "first", events: &events}
	panicking := &mcpPanicPlugin{name: "panicking", panicConnectPost: true, events: &events}
	pipeline := newMCPPanicPipeline(first, panicking)
	ctx := mcpPanicContext()

	resp, bifrostErr := pipeline.RunMCPPostConnectionHooks(ctx, &schemas.BifrostMCPConnectResponse{}, nil, 2)
	assert.Nil(t, resp)
	assertPluginPanicError(t, bifrostErr)
	assert.Equal(t, []string{"panicking.connect_post"}, events)
	assertScopedContextReleased(t, panicking.lastCtx)
	assert.Equal(t, "parent-span", GetStringFromContext(ctx, schemas.BifrostContextKeySpanID))
}

func TestMCPOrdinaryHookErrorsRemainNonBlocking(t *testing.T) {
	ordinaryErr := errors.New("ordinary hook error")
	var events []string
	first := &mcpPanicPlugin{name: "first", preErr: ordinaryErr, postErr: ordinaryErr, connectPreErr: ordinaryErr, connectPostErr: ordinaryErr, events: &events}
	second := &mcpPanicPlugin{name: "second", events: &events}
	pipeline := newMCPPanicPipeline(first, second)
	ctx := mcpPanicContext()

	_, shortCircuit, count := pipeline.RunMCPPreHooks(ctx, &schemas.BifrostMCPRequest{})
	assert.Nil(t, shortCircuit)
	assert.Equal(t, 2, count)
	resp, bifrostErr := pipeline.RunMCPPostHooks(ctx, &schemas.BifrostMCPResponse{}, nil, count)
	assert.NotNil(t, resp)
	assert.Nil(t, bifrostErr)

	_, connectionShortCircuit, connectionCount := pipeline.RunMCPPreConnectionHooks(ctx, &schemas.BifrostMCPConnectRequest{})
	assert.Nil(t, connectionShortCircuit)
	assert.Equal(t, 2, connectionCount)
	connectionResp, connectionErr := pipeline.RunMCPPostConnectionHooks(ctx, &schemas.BifrostMCPConnectResponse{}, nil, connectionCount)
	assert.NotNil(t, connectionResp)
	assert.Nil(t, connectionErr)
	assert.Contains(t, events, "second.pre")
	assert.Contains(t, events, "second.connect_pre")
}

func mcpPanicContext() *schemas.BifrostContext {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ctx.SetValue(schemas.BifrostContextKeySpanID, "parent-span")
	return ctx
}

func assertScopedContextReleased(t *testing.T, ctx *schemas.BifrostContext) {
	t.Helper()
	require.NotNil(t, ctx)
	assert.Nil(t, ctx.GetPluginLogs())
}
