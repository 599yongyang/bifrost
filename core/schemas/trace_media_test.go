package schemas

import (
	"sync"
	"testing"
)

type testTraceMediaStore struct {
	mu    sync.RWMutex
	items map[string][]TraceMedia
}

func (s *testTraceMediaStore) Store(key string, media TraceMedia) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	media.Data = append([]byte(nil), media.Data...)
	s.items[key] = append(s.items[key], media)
	return true
}

func (s *testTraceMediaStore) List(key string) []TraceMedia {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]TraceMedia(nil), s.items[key]...)
}

func (s *testTraceMediaStore) Delete(key string) {
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
}

func TestTraceMediaSnapshotSharesBoundedSidecarWithoutCopyingIntoAttributes(t *testing.T) {
	store := &testTraceMediaStore{items: map[string][]TraceMedia{}}
	trace := &Trace{InternalID: "request-1", Attributes: map[string]any{"safe": "metadata"}}
	trace.SetMediaStore(store, trace.InternalID)
	original := []byte("image-bytes")
	if !trace.AddMedia(TraceMedia{ID: "image", MIMEType: "image/png", Data: original}) {
		t.Fatal("media was not stored")
	}
	original[0] = 'X'

	snapshot := trace.SnapshotForExport()
	attachments := snapshot.MediaAttachments()
	if len(attachments) != 1 || string(attachments[0].Data) != "image-bytes" {
		t.Fatalf("snapshot attachments = %#v", attachments)
	}
	if _, leaked := snapshot.Attributes["media"]; leaked {
		t.Fatal("binary media leaked into trace attributes")
	}

	trace.Reset()
	if got := trace.MediaAttachments(); got != nil {
		t.Fatalf("pooled trace retained media handle: %#v", got)
	}
	if len(snapshot.MediaAttachments()) != 1 {
		t.Fatal("export snapshot lost sidecar before exporter completed")
	}
}

func TestTraceMediaConcurrentStoreAndSnapshot(t *testing.T) {
	store := &testTraceMediaStore{items: map[string][]TraceMedia{}}
	trace := &Trace{InternalID: "concurrent"}
	trace.SetMediaStore(store, trace.InternalID)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			trace.AddMedia(TraceMedia{ID: "shared", Data: []byte("x")})
			_ = trace.SnapshotForExport().MediaAttachments()
		}()
	}
	wg.Wait()
}
