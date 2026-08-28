package schema_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDynamicDebianDockerfileBuildsLocalGovernanceModule(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file path")
	}
	dockerfilePath := filepath.Join(filepath.Dir(filename), "..", "Dockerfile.dynamic-debian")
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read %s: %v", dockerfilePath, err)
	}
	contents := string(data)
	required := []string{
		"COPY plugins/governance/go.mod plugins/governance/go.sum ./plugins/governance/",
		"go work init ./core ./framework ./plugins/governance",
		"COPY plugins/governance/ ./plugins/governance/",
		`test "$(go list -m -f '{{.Dir}}' github.com/maximhq/bifrost/plugins/governance)" = "/app/plugins/governance"`,
	}
	for _, fragment := range required {
		if !strings.Contains(contents, fragment) {
			t.Errorf("Dockerfile.dynamic-debian must include local governance module fragment %q", fragment)
		}
	}
}
