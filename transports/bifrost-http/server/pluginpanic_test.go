package server

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	dynamicplugins "github.com/maximhq/bifrost/framework/plugins"
	"github.com/maximhq/bifrost/plugins/telemetry"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
)

type panickingPluginLoader struct{}

func (*panickingPluginLoader) LoadPlugin(string, any) (schemas.BasePlugin, error) {
	panic("secret custom init panic")
}
func (*panickingPluginLoader) VerifyBasePlugin(string) (string, error) { return "", nil }

var _ dynamicplugins.PluginLoader = (*panickingPluginLoader)(nil)

type failingPluginLoader struct{}

func (*failingPluginLoader) LoadPlugin(string, any) (schemas.BasePlugin, error) {
	return nil, errors.New("incompatible plugin toolchain")
}
func (*failingPluginLoader) VerifyBasePlugin(string) (string, error) { return "", nil }

type panickingNamePlugin struct{}

func (*panickingNamePlugin) GetName() string { panic("secret name panic") }
func (*panickingNamePlugin) Cleanup() error  { return nil }

func TestInstantiatePluginContainsBuiltinAndCustomPanics(t *testing.T) {
	_, err := InstantiatePlugin(context.Background(), telemetry.PluginName, nil, nil, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")

	path := "/tmp/panic-plugin.so"
	config := &lib.Config{PluginLoader: &panickingPluginLoader{}}
	_, err = InstantiatePlugin(context.Background(), "custom-panic", &path, nil, config)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}

func TestLoadCustomPluginsContinuesAfterPluginLoadFailure(t *testing.T) {
	previousLogger := logger
	logger = noopTestLogger{}
	t.Cleanup(func() { logger = previousLogger })

	path := "/tmp/incompatible-plugin.so"
	config := &lib.Config{
		PluginLoader: &failingPluginLoader{},
		PluginConfigs: []*schemas.PluginConfig{{
			Name:    "incompatible-custom-plugin",
			Enabled: true,
			Path:    &path,
		}},
	}
	server := &BifrostHTTPServer{Config: config}

	require.NoError(t, server.loadCustomPlugins(context.Background()))
	status, ok := config.GetPluginStatusByName("incompatible-custom-plugin")
	require.True(t, ok)
	require.Equal(t, schemas.PluginStatusError, status.Status)
	require.Empty(t, config.GetLoadedLLMPlugins())
}

func TestSyncLoadedPluginContainsGetNamePanic(t *testing.T) {
	server := &BifrostHTTPServer{Config: &lib.Config{}}
	err := server.SyncLoadedPlugin(context.Background(), "display-name", &panickingNamePlugin{}, nil, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}

func TestRemovePluginPrunesCorruptedInstance(t *testing.T) {
	config := &lib.Config{}
	bad := &panickingNamePlugin{}
	plugins := []schemas.BasePlugin{bad}
	config.BasePlugins.Store(&plugins)
	config.UpdatePluginOverallStatus("corrupted", "display-name", schemas.PluginStatusActive, nil, nil)
	server := &BifrostHTTPServer{Config: config}

	require.NoError(t, server.RemovePlugin(context.Background(), "display-name"))
	remaining := config.BasePlugins.Load()
	require.NotNil(t, remaining)
	require.Empty(t, *remaining)
}
