package tracing

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type panickingObservabilityPlugin struct {
	blockingObsPlugin
	panicName   bool
	panicInject bool
}

func (p *panickingObservabilityPlugin) GetName() string {
	if p.panicName {
		panic("secret observability name panic")
	}
	return p.blockingObsPlugin.GetName()
}

func (p *panickingObservabilityPlugin) Inject(_ context.Context, _ *schemas.Trace) error {
	p.started.Add(1)
	if p.panicInject {
		panic("secret observability inject panic")
	}
	return nil
}

func TestSetObservabilityPluginsContainsGetNamePanic(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	panicking := &panickingObservabilityPlugin{panicName: true}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("SetObservabilityPlugins leaked panic: %v", recovered)
			}
		}()
		tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{panicking}, nil)
	}()

	loaded := tracer.obsPlugins.Load()
	if loaded == nil || len(*loaded) != 0 {
		t.Fatalf("panicking plugin should be skipped, got %v", loaded)
	}
}

func TestObservabilityInjectPanicDoesNotBlockHealthyPlugin(t *testing.T) {
	store := NewTraceStore(5*time.Minute, nil)
	defer store.Stop()
	tracer := NewTracer(store, nil, nil)
	defer tracer.Stop()

	panicking := &panickingObservabilityPlugin{
		blockingObsPlugin: blockingObsPlugin{name: "panicking"},
		panicInject:       true,
	}
	healthy := &blockingObsPlugin{name: "healthy"}
	tracer.SetObservabilityPlugins([]schemas.ObservabilityPlugin{panicking, healthy}, nil)

	tracer.CompleteAndFlushTrace(tracer.CreateTrace(""))
	if completed := tracer.waitForFlushes(5 * time.Second); !completed {
		t.Fatal("trace flush did not complete after contained plugin panic")
	}
	if got := panicking.started.Load(); got != 1 {
		t.Fatalf("panicking plugin Inject calls = %d, want 1", got)
	}
	if got := healthy.started.Load(); got != 1 {
		t.Fatalf("healthy plugin Inject calls = %d, want 1", got)
	}
}
