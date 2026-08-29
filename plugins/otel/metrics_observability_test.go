package otel

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetricsExporterRecordsObservabilityEvents(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	exporter := &MetricsExporter{provider: provider, meter: provider.Meter("test")}
	exporter.initMetrics()
	exporter.RecordObservabilityEvent(context.Background(), "selection", "quota")

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "bifrost_observability_events_total" {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
				t.Fatalf("observability metric = %#v", metric.Data)
			}
			return
		}
	}
	t.Fatal("bifrost_observability_events_total was not collected")
}
