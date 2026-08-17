package bifrost

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestFinalizeAttemptRoutingInfoPublishesUnaryFallbackIdentity(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), time.Time{})
	primary := schemas.RoutingInfo{Provider: schemas.OpenAI, Model: "public-image-model"}
	ctx.SetRoutingInfoSnapshot(primary)
	ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, 1)

	fallback := finalizeAttemptRoutingInfo(ctx, schemas.RoutingInfo{Provider: schemas.Azure, Model: "fallback-deployment"})
	if !fallback.IsFallback || fallback.PrimaryProvider == nil || *fallback.PrimaryProvider != schemas.OpenAI ||
		fallback.PrimaryModel == nil || *fallback.PrimaryModel != "public-image-model" {
		t.Fatalf("fallback routing info = %+v, want primary identity preserved", fallback)
	}
	published, ok := ctx.Value(schemas.BifrostContextKeyRoutingInfo).(schemas.RoutingInfo)
	if !ok || published.Provider != schemas.Azure || published.Model != "fallback-deployment" || !published.IsFallback {
		t.Fatalf("published routing info = %+v, want current fallback attempt", published)
	}
}
