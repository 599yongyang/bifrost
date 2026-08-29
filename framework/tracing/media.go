package tracing

import (
	"sync"
	"sync/atomic"

	"github.com/maximhq/bifrost/core/schemas"
)

const (
	maxTraceMediaAttachments  = 16
	maxTraceMediaTotalBytes   = 32 << 20
	maxGlobalTraceMediaBytes  = 64 << 20
	maxConcurrentMediaDecodes = 1
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
	decodeSem  chan struct{}

	attachmentLimitRejected atomic.Int64
	traceByteLimitRejected  atomic.Int64
	globalByteLimitRejected atomic.Int64
	decodeSaturated         atomic.Int64
}

func newTraceMediaStore() *traceMediaStore {
	return &traceMediaStore{
		entries:   make(map[string]*traceMediaEntry),
		decodeSem: make(chan struct{}, maxConcurrentMediaDecodes),
	}
}

func (s *traceMediaStore) TryAcquireDecode() (func(), bool) {
	if s == nil || s.decodeSem == nil {
		return func() {}, true
	}
	select {
	case s.decodeSem <- struct{}{}:
		return func() { <-s.decodeSem }, true
	default:
		s.decodeSaturated.Add(1)
		return nil, false
	}
}

func (s *traceMediaStore) Store(key string, media schemas.TraceMedia) bool {
	return s.StoreWithStatus(key, media, false) == schemas.TraceMediaCaptureStatusCaptured
}

func (s *traceMediaStore) StoreOwned(key string, media schemas.TraceMedia) bool {
	return s.StoreWithStatus(key, media, true) == schemas.TraceMediaCaptureStatusCaptured
}

func (s *traceMediaStore) StoreWithStatus(key string, media schemas.TraceMedia, transferOwnership bool) string {
	if s == nil || key == "" || media.ID == "" || len(media.Data) == 0 {
		return schemas.TraceMediaCaptureStatusTraceByteLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[key]
	if entry == nil {
		entry = &traceMediaEntry{attachments: make([]schemas.TraceMedia, 0, 4)}
		s.entries[key] = entry
	}
	if len(entry.attachments) >= maxTraceMediaAttachments {
		s.attachmentLimitRejected.Add(1)
		return schemas.TraceMediaCaptureStatusAttachmentLimit
	}
	if entry.totalBytes+len(media.Data) > maxTraceMediaTotalBytes {
		s.traceByteLimitRejected.Add(1)
		return schemas.TraceMediaCaptureStatusTraceByteLimit
	}
	if s.totalBytes+len(media.Data) > maxGlobalTraceMediaBytes {
		s.globalByteLimitRejected.Add(1)
		return schemas.TraceMediaCaptureStatusGlobalByteLimit
	}
	if !transferOwnership {
		media.Data = append([]byte(nil), media.Data...)
	}
	entry.attachments = append(entry.attachments, media)
	entry.totalBytes += len(media.Data)
	s.totalBytes += len(media.Data)
	return schemas.TraceMediaCaptureStatusCaptured
}

type traceMediaStoreStats struct {
	CurrentBytes            int
	AttachmentLimitRejected int64
	TraceByteLimitRejected  int64
	GlobalByteLimitRejected int64
	DecodeSaturated         int64
}

func (s *traceMediaStore) Stats() traceMediaStoreStats {
	s.mu.RLock()
	currentBytes := s.totalBytes
	s.mu.RUnlock()
	return traceMediaStoreStats{
		CurrentBytes:            currentBytes,
		AttachmentLimitRejected: s.attachmentLimitRejected.Load(),
		TraceByteLimitRejected:  s.traceByteLimitRejected.Load(),
		GlobalByteLimitRejected: s.globalByteLimitRejected.Load(),
		DecodeSaturated:         s.decodeSaturated.Load(),
	}
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
