package plugins

import (
	"os"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func pluginBuildInfo(goVersion, buildMode, goos, goarch string) *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: goVersion,
		Settings: []debug.BuildSetting{
			{Key: "-buildmode", Value: buildMode},
			{Key: "GOOS", Value: goos},
			{Key: "GOARCH", Value: goarch},
		},
	}
}

func TestValidatePluginBuildInfoRejectsIncompatibleToolchain(t *testing.T) {
	err := validatePluginBuildInfo(pluginBuildInfo("go1.26.5", "plugin", "linux", "amd64"), "go1.27.0", "linux", "amd64")
	require.ErrorContains(t, err, "built with Go go1.26.5")
	require.ErrorContains(t, err, "host uses go1.27.0")
}

func TestValidatePluginBuildInfoRejectsWrongArtifactTypeAndTarget(t *testing.T) {
	t.Run("executable", func(t *testing.T) {
		err := validatePluginBuildInfo(pluginBuildInfo("go1.27.0", "exe", "linux", "amd64"), "go1.27.0", "linux", "amd64")
		require.ErrorContains(t, err, "build mode")
	})

	t.Run("architecture", func(t *testing.T) {
		err := validatePluginBuildInfo(pluginBuildInfo("go1.27.0", "plugin", "linux", "arm64"), "go1.27.0", "linux", "amd64")
		require.ErrorContains(t, err, "linux/arm64")
		require.ErrorContains(t, err, "linux/amd64")
	})
}

func TestValidatePluginBuildInfoAcceptsMatchingPlugin(t *testing.T) {
	require.NoError(t, validatePluginBuildInfo(
		pluginBuildInfo("go1.27.0", "plugin", "linux", "amd64"),
		"go1.27.0",
		"linux",
		"amd64",
	))
}

func TestValidatePluginBinaryRejectsExecutableBeforePluginOpen(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	err = validatePluginBinary(executable, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	require.ErrorContains(t, err, "build mode")
}
