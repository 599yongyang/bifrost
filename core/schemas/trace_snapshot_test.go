package schemas

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type traceMediaTestStore struct {
	items map[string][]TraceMedia
}

func (s *traceMediaTestStore) Store(key string, media TraceMedia) bool {
	media.Data = append([]byte(nil), media.Data...)
	s.items[key] = append(s.items[key], media)
	return true
}

func (s *traceMediaTestStore) List(key string) []TraceMedia {
	return append([]TraceMedia(nil), s.items[key]...)
}

func (s *traceMediaTestStore) Delete(key string) { delete(s.items, key) }

func TestTraceMediaIsExcludedFromGenericSerialization(t *testing.T) {
	store := &traceMediaTestStore{items: make(map[string][]TraceMedia)}
	trace := &Trace{TraceID: "trace-1", InternalID: "internal-1"}
	trace.SetMediaStore(store, trace.InternalID)
	trace.AddMedia(TraceMedia{
		ID: "media-1", SpanID: "span-1", MIMEType: "image/png", Data: []byte("secret-image-bytes"),
	})

	payload, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	if strings.Contains(string(payload), "Media") || strings.Contains(string(payload), "c2VjcmV0LWltYWdlLWJ5dGVz") {
		t.Fatalf("generic trace serialization included media sidecar: %s", payload)
	}
	snapshot := trace.SnapshotForExport()
	if len(snapshot.MediaAttachments()) != 1 || &snapshot.MediaAttachments()[0].Data[0] != &trace.MediaAttachments()[0].Data[0] {
		t.Fatal("snapshot should retain a manager handle without copying media bytes")
	}
}

// TestSnapshotForExport_ConcurrentWriter reproduces the crash observed by the
// Datadog/OTEL exporters: an exporter iterating a span's live Attributes map
// while a late writer (streaming finalization, redaction) mutates it via the
// span lock triggers a fatal "concurrent map iteration and map write".
//
// SnapshotForExport must clone the maps under the span/trace locks so the
// returned trace can be read freely while the original keeps being written.
// Run with -race; without the snapshot, iterating trace.Spans[i].Attributes in
// the reader goroutine below would fatal.
func TestSnapshotForExport_ConcurrentWriter(t *testing.T) {
	trace := &Trace{
		TraceID:    "t1",
		Attributes: map[string]any{"trace.k": "v"},
	}
	root := &Span{SpanID: "root", TraceID: "t1", Kind: SpanKindHTTPRequest}
	child := &Span{SpanID: "llm", ParentID: "root", TraceID: "t1", Kind: SpanKindLLMCall}
	root.SetAttribute("seed", "x")
	child.SetAttribute("seed", "x")
	trace.RootSpan = root
	trace.Spans = []*Span{root, child}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: mimics completeDeferredSpan / redaction still mutating the span
	// attributes (under the span lock) after the trace was handed to exporters.
	wg.Go(func() {
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				child.SetAttribute("gen_ai.usage.output_tokens", i)
				root.SetAttribute("http.status_code", i)
				trace.SetAttribute("trace.k", i)
				i++
			}
		}
	})

	// Reader: exporters take a snapshot and iterate its maps freely.
	for range 4 {
		wg.Go(func() {
			for range 2000 {
				snap := trace.SnapshotForExport()
				for _, s := range snap.Spans {
					for k, v := range s.Attributes { // must not race the writer
						_, _ = k, v
					}
				}
				for k, v := range snap.Attributes {
					_, _ = k, v
				}
			}
		})
	}

	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestSnapshotForExport_IsolatedCopy(t *testing.T) {
	trace := &Trace{TraceID: "t1", Attributes: map[string]any{"a": 1}}
	root := &Span{SpanID: "root", TraceID: "t1"}
	root.SetAttribute("k", "v")
	trace.RootSpan = root
	trace.Spans = []*Span{root}

	snap := trace.SnapshotForExport()

	// Mutating the original after snapshot must not affect the snapshot.
	root.SetAttribute("k", "changed")
	trace.SetAttribute("a", 999)

	if got := snap.Spans[0].Attributes["k"]; got != "v" {
		t.Fatalf("snapshot span attr leaked mutation: got %v, want v", got)
	}
	if got := snap.Attributes["a"]; got != 1 {
		t.Fatalf("snapshot trace attr leaked mutation: got %v, want 1", got)
	}
	// RootSpan identity must be preserved within the snapshot.
	if snap.RootSpan != snap.Spans[0] {
		t.Fatalf("snapshot RootSpan must alias its entry in Spans")
	}
	// The snapshot must not alias the original span pointers.
	if snap.Spans[0] == root {
		t.Fatalf("snapshot must copy spans, not alias the originals")
	}
}
