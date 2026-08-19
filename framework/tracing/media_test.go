package tracing

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestTraceMediaStoreOwnsBytesAndReleasesWithTrace(t *testing.T) {
	store := NewTraceStore(time.Minute, nil)
	defer store.Stop()
	traceID := store.CreateTrace("")
	trace := store.GetTrace(traceID)
	original := []byte("immutable-image")
	if !trace.AddMedia(schemas.TraceMedia{ID: "image-1", MIMEType: "image/png", Data: original}) {
		t.Fatal("media was not stored")
	}
	original[0] = 'X'
	attachments := trace.MediaAttachments()
	if len(attachments) != 1 || string(attachments[0].Data) != "immutable-image" {
		t.Fatalf("stored media = %q, want owned immutable bytes", attachments[0].Data)
	}

	completed := store.CompleteTrace(traceID)
	snapshot := completed.SnapshotForExport()
	store.ReleaseTrace(completed)
	if got := len(snapshot.MediaAttachments()); got != 0 {
		t.Fatalf("released trace retained %d media attachments", got)
	}
}

func TestTraceMediaStoreEnforcesAttachmentLimit(t *testing.T) {
	store := newTraceMediaStore()
	for i := 0; i < maxTraceMediaAttachments; i++ {
		if !store.Store("trace-1", schemas.TraceMedia{ID: string(rune('a' + i)), Data: []byte("x")}) {
			t.Fatalf("attachment %d unexpectedly rejected", i)
		}
	}
	if store.Store("trace-1", schemas.TraceMedia{ID: "overflow", Data: []byte("x")}) {
		t.Fatal("attachment above per-trace limit was accepted")
	}
}

func TestTraceMediaStoreEnforcesByteBudgetsAndRecoversAfterRelease(t *testing.T) {
	store := newTraceMediaStore()
	halfGlobal := maxGlobalTraceMediaBytes / 2
	if status := store.StoreWithStatus("trace-1", schemas.TraceMedia{ID: "a", Data: make([]byte, halfGlobal)}, true); status != schemas.TraceMediaCaptureStatusCaptured {
		t.Fatalf("first trace status = %s", status)
	}
	if status := store.StoreWithStatus("trace-1", schemas.TraceMedia{ID: "trace-overflow", Data: []byte("x")}, true); status != schemas.TraceMediaCaptureStatusTraceByteLimit {
		t.Fatalf("per-trace overflow status = %s, want trace_byte_limit", status)
	}
	if status := store.StoreWithStatus("trace-2", schemas.TraceMedia{ID: "b", Data: make([]byte, halfGlobal)}, true); status != schemas.TraceMediaCaptureStatusCaptured {
		t.Fatalf("second trace status = %s", status)
	}
	if status := store.StoreWithStatus("trace-3", schemas.TraceMedia{ID: "global-overflow", Data: []byte("x")}, true); status != schemas.TraceMediaCaptureStatusGlobalByteLimit {
		t.Fatalf("global overflow status = %s, want global_byte_limit", status)
	}

	store.Delete("trace-1")
	if status := store.StoreWithStatus("trace-3", schemas.TraceMedia{ID: "after-release", Data: []byte("x")}, true); status != schemas.TraceMediaCaptureStatusCaptured {
		t.Fatalf("budget did not recover after release: %s", status)
	}
	stats := store.Stats()
	if stats.TraceByteLimitRejected != 1 || stats.GlobalByteLimitRejected != 1 || stats.CurrentBytes != halfGlobal+1 {
		t.Fatalf("media stats = %+v", stats)
	}
}

func TestTraceMediaStoreCountsDecodeSaturation(t *testing.T) {
	store := newTraceMediaStore()
	release, ok := store.TryAcquireDecode()
	if !ok {
		t.Fatal("first decode slot was unavailable")
	}
	defer release()
	if _, ok := store.TryAcquireDecode(); ok {
		t.Fatal("second concurrent decode unexpectedly acquired the single slot")
	}
	if stats := store.Stats(); stats.DecodeSaturated != 1 {
		t.Fatalf("decode saturation count = %d, want 1", stats.DecodeSaturated)
	}
}
