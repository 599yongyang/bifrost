package tracing

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestPopulateErrorAttributesIncludesSafeTimeoutMetadata(t *testing.T) {
	errorType := "timeout"
	status := 502
	upstreamResponded := false
	err := &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "safe timeout", Type: &errorType},
		ExtraFields: schemas.BifrostErrorExtraFields{
			TimeoutSource:            schemas.TimeoutSourceUpstreamConnection,
			ConfiguredTimeoutSeconds: 600,
			ElapsedMS:                27_000,
			UpstreamResponseReceived: &upstreamResponded,
		},
	}
	attrs := PopulateErrorAttributes(err)
	if attrs[schemas.AttrBifrostTimeoutSource] != string(schemas.TimeoutSourceUpstreamConnection) {
		t.Fatalf("timeout source = %v", attrs[schemas.AttrBifrostTimeoutSource])
	}
	if attrs[schemas.AttrBifrostConfiguredTimeout] != 600 || attrs[schemas.AttrBifrostTimeoutElapsedMs] != int64(27_000) {
		t.Fatalf("timeout metadata = %#v", attrs)
	}
	if attrs[schemas.AttrBifrostUpstreamResponded] != false {
		t.Fatalf("upstream response flag = %v", attrs[schemas.AttrBifrostUpstreamResponded])
	}
}
