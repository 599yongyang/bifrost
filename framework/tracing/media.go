package tracing

import (
	"sync"

	"github.com/maximhq/bifrost/core/schemas"
)

const (
	maxTraceMediaAttachments = 16
	maxTraceMediaTotalBytes  = 64 << 20
	maxGlobalTraceMediaBytes = 256 << 20
)

type traceMediaEntry struct {
	attachments []schemas.TraceMedia
	totalBytes  int
}

// traceMediaStore owns binary observability attachments outside Trace. Entries
// are keyed by Trace.InternalID, bounded per request, and removed when the trace
// is released or expires from TraceStore.
type traceMediaStore struct {
	mu         sync.RWMutex
	entries    map[string]*traceMediaEntry
	totalBytes int
}

func newTraceMediaStore() *traceMediaStore {
	return &traceMediaStore{entries: make(map[string]*traceMediaEntry)}
}

func (s *traceMediaStore) Store(key string, media schemas.TraceMedia) bool {
	if s == nil || key == "" || media.ID == "" || len(media.Data) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[key]
	if entry == nil {
		entry = &traceMediaEntry{attachments: make([]schemas.TraceMedia, 0, 4)}
		s.entries[key] = entry
	}
	if len(entry.attachments) >= maxTraceMediaAttachments ||
		entry.totalBytes+len(media.Data) > maxTraceMediaTotalBytes ||
		s.totalBytes+len(media.Data) > maxGlobalTraceMediaBytes {
		return false
	}
	media.Data = append([]byte(nil), media.Data...)
	entry.attachments = append(entry.attachments, media)
	entry.totalBytes += len(media.Data)
	s.totalBytes += len(media.Data)
	return true
}

func (s *traceMediaStore) List(key string) []schemas.TraceMedia {
	if s == nil || key == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.entries[key]
	if entry == nil {
		return nil
	}
	// The slice is copied so callers cannot alter manager bookkeeping. Data is
	// intentionally shared read-only to avoid duplicating request-sized bytes.
	return append([]schemas.TraceMedia(nil), entry.attachments...)
}

func (s *traceMediaStore) Delete(key string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	entry := s.entries[key]
	delete(s.entries, key)
	if entry != nil {
		s.totalBytes -= entry.totalBytes
	}
	s.mu.Unlock()
	if entry == nil {
		return
	}
	for i := range entry.attachments {
		entry.attachments[i] = schemas.TraceMedia{}
	}
}
