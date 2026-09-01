package apimart_test

import (
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/internal/llmtests"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestAPIMart(t *testing.T) {
	if strings.TrimSpace(os.Getenv("APIMART_API_KEY")) == "" {
		t.Skip("skipping APIMart live tests because APIMART_API_KEY is not set")
	}
	client, ctx, cancel, err := llmtests.SetupTest()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer client.Shutdown()

	llmtests.RunAllComprehensiveTests(t, client, ctx, llmtests.ComprehensiveTestConfig{
		Provider:             schemas.APIMart,
		ImageGenerationModel: "gpt-image-2",
		ImageEditModel:       "gpt-image-2",
		Scenarios: llmtests.TestScenarios{
			ImageGeneration: true,
			ImageEdit:       true,
		},
	})
}
