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
