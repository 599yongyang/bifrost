package server

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/plugins/logging"
	"github.com/maximhq/bifrost/plugins/otel"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/require"
)

func manualExportReason(t *testing.T, plugin *otel.OtelPlugin) string {
	t.Helper()
	_, reason, _ := plugin.EnqueueManualExport(context.Background(), "log-1")
	return reason
}

func TestWireObservationExportStoreBootstrapAndLoggingRemoval(t *testing.T) {
	config := &lib.Config{}
	loggerPlugin := &logging.LoggerPlugin{}
	otelPlugin := &otel.OtelPlugin{}
	require.NoError(t, config.ReloadPlugin(loggerPlugin))
	require.NoError(t, config.ReloadPlugin(otelPlugin))
	server := &BifrostHTTPServer{Config: config}

	server.wireObservationExportStore()
	require.Equal(t, "target_unavailable", manualExportReason(t, otelPlugin), "bootstrap wiring should install the logging repository")

	config.UpdatePluginOverallStatus(logging.PluginName, "logging-display", "active", nil, nil)
	require.NoError(t, server.RemovePlugin(context.Background(), "logging-display"))
	status, reason, err := otelPlugin.EnqueueManualExport(context.Background(), "log-1")
	require.Error(t, err)
	require.Equal(t, logstore.ObservationExportStatusUnavailable, status)
	require.Equal(t, "manual_export_unavailable", reason, "removing or disabling logging must clear the live store")
}

func TestWireObservationExportStoreRefreshesReloadedInstances(t *testing.T) {
	config := &lib.Config{}
	loggerPlugin := &logging.LoggerPlugin{}
	oldOTEL := &otel.OtelPlugin{}
	require.NoError(t, config.ReloadPlugin(loggerPlugin))
	require.NoError(t, config.ReloadPlugin(oldOTEL))
	server := &BifrostHTTPServer{Config: config}
	server.wireObservationExportStore()

	newOTEL := &otel.OtelPlugin{}
	require.NoError(t, config.ReloadPlugin(newOTEL))
	server.wireObservationExportStore()
	require.Equal(t, "target_unavailable", manualExportReason(t, newOTEL), "reload must wire the new OTEL instance")

	require.NoError(t, config.UnregisterPlugin(logging.PluginName))
	server.wireObservationExportStore()
	require.Equal(t, "manual_export_unavailable", manualExportReason(t, newOTEL))
	require.Equal(t, "target_unavailable", manualExportReason(t, oldOTEL), "retired OTEL instance must not be mutated during live rewiring")
}
