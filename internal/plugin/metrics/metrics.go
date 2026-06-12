package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func NewProvider(registerer prometheus.Registerer) (*sdkmetric.MeterProvider, error) {
	exporter, err := otelprometheus.New(
		otelprometheus.WithRegisterer(registerer),
		otelprometheus.WithoutScopeInfo(),
	)
	if err != nil {
		return nil, fmt.Errorf("create Prometheus exporter: %w", err)
	}

	return sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter)), nil
}
