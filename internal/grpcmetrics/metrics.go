package grpcmetrics

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

type MetricsRegistry interface {
	HTTPCallLatencies() metric.Int64Histogram
	HTTPCallStatusCodes() metric.Int64Counter
	ServerMessageReceivedByType() metric.Int64Counter
	ConnectionRetries() metric.Int64Counter
	ConnectionStatus() metric.Int64UpDownCounter
	AuthTokenRefreshErrors() metric.Int64Counter
	AuthTokenRefreshDuration() metric.Int64Histogram
	AuthTokenExpiry() metric.Float64Gauge
}

type metricsRegistry struct {
	httpCallLatencies           metric.Int64Histogram
	httpCallStatusCodes         metric.Int64Counter
	serverMessageReceivedByType metric.Int64Counter
	connectionRetries           metric.Int64Counter
	connectionStatus            metric.Int64UpDownCounter
	authTokenRefreshErrors      metric.Int64Counter
	authTokenRefreshDuration    metric.Int64Histogram
	authTokenExpiry             metric.Float64Gauge
}

func RegisterMetricsRegistry(meterProvider metric.MeterProvider) (MetricsRegistry, error) {
	meter := meterProvider.Meter("client")

	httpCallLatencies, err := meter.Int64Histogram(
		"http_call_latencies",
		metric.WithUnit("ms"),
		metric.WithDescription("Latency of HTTP calls"),
	)
	if err != nil {
		return nil, err
	}

	httpCallStatusCodes, err := meter.Int64Counter(
		"http_call_status_codes",
		metric.WithUnit("1"),
		metric.WithDescription("HTTP status codes of HTTP calls"),
	)
	if err != nil {
		return nil, err
	}

	serverMessageReceivedByType, err := meter.Int64Counter(
		"server_message_received_by_type",
		metric.WithUnit("1"),
		metric.WithDescription("Server message received by type"),
	)
	if err != nil {
		return nil, err
	}

	connectionRetries, err := meter.Int64Counter(
		"connection_retries",
		metric.WithUnit("1"),
		metric.WithDescription("Number of connection retry attempts"),
	)
	if err != nil {
		return nil, err
	}

	connectionStatus, err := meter.Int64UpDownCounter(
		"connection_status",
		metric.WithUnit("1"),
		metric.WithDescription("Current connection status (1=connected, 0=disconnected)"),
	)
	if err != nil {
		return nil, err
	}

	authTokenRefreshErrors, err := meter.Int64Counter(
		"auth_token_refresh_errors_total",
		metric.WithUnit("1"),
		metric.WithDescription("Total number of authentication token refresh errors"),
	)
	if err != nil {
		return nil, err
	}

	authTokenRefreshDuration, err := meter.Int64Histogram(
		"auth_token_refresh_duration_milliseconds",
		metric.WithUnit("ms"),
		metric.WithDescription("Duration of authentication token refresh operations"),
	)
	if err != nil {
		return nil, err
	}

	authTokenExpiry, err := meter.Float64Gauge(
		"auth_token_expiry_timestamp",
		metric.WithUnit("s"),
		metric.WithDescription("Unix timestamp when the authentication token expires"),
	)
	if err != nil {
		return nil, err
	}

	return &metricsRegistry{
		httpCallLatencies:           httpCallLatencies,
		httpCallStatusCodes:         httpCallStatusCodes,
		serverMessageReceivedByType: serverMessageReceivedByType,
		connectionRetries:           connectionRetries,
		connectionStatus:            connectionStatus,
		authTokenRefreshErrors:      authTokenRefreshErrors,
		authTokenRefreshDuration:    authTokenRefreshDuration,
		authTokenExpiry:             authTokenExpiry,
	}, nil
}

func (m *metricsRegistry) HTTPCallLatencies() metric.Int64Histogram {
	return m.httpCallLatencies
}

func (m *metricsRegistry) HTTPCallStatusCodes() metric.Int64Counter {
	return m.httpCallStatusCodes
}

func (m *metricsRegistry) ServerMessageReceivedByType() metric.Int64Counter {
	return m.serverMessageReceivedByType
}

func (m *metricsRegistry) ConnectionRetries() metric.Int64Counter {
	return m.connectionRetries
}

func (m *metricsRegistry) ConnectionStatus() metric.Int64UpDownCounter {
	return m.connectionStatus
}

func (m *metricsRegistry) AuthTokenRefreshErrors() metric.Int64Counter {
	return m.authTokenRefreshErrors
}

func (m *metricsRegistry) AuthTokenRefreshDuration() metric.Int64Histogram {
	return m.authTokenRefreshDuration
}

func (m *metricsRegistry) AuthTokenExpiry() metric.Float64Gauge {
	return m.authTokenExpiry
}

type NoOpMetricsRegistry struct{}

func NewNoOpMetricsRegistry() *NoOpMetricsRegistry {
	return &NoOpMetricsRegistry{}
}

func (m *NoOpMetricsRegistry) HTTPCallLatencies() metric.Int64Histogram {
	histogram, _ := otel.GetMeterProvider().Meter("client").Int64Histogram("http_call_latencies")
	return histogram
}

func (m *NoOpMetricsRegistry) HTTPCallStatusCodes() metric.Int64Counter {
	counter, _ := otel.GetMeterProvider().Meter("client").Int64Counter("http_call_status_codes")
	return counter
}

func (m *NoOpMetricsRegistry) ServerMessageReceivedByType() metric.Int64Counter {
	counter, _ := otel.GetMeterProvider().Meter("client").Int64Counter("server_message_received_by_type")
	return counter
}

func (m *NoOpMetricsRegistry) ConnectionRetries() metric.Int64Counter {
	counter, _ := otel.GetMeterProvider().Meter("client").Int64Counter("connection_retries")
	return counter
}

func (m *NoOpMetricsRegistry) ConnectionStatus() metric.Int64UpDownCounter {
	counter, _ := otel.GetMeterProvider().Meter("client").Int64UpDownCounter("connection_status")
	return counter
}

func (m *NoOpMetricsRegistry) AuthTokenRefreshErrors() metric.Int64Counter {
	counter, _ := otel.GetMeterProvider().Meter("client").Int64Counter("auth_token_refresh_errors_total")
	return counter
}

func (m *NoOpMetricsRegistry) AuthTokenRefreshDuration() metric.Int64Histogram {
	histogram, _ := otel.GetMeterProvider().Meter("client").Int64Histogram("auth_token_refresh_duration_milliseconds")
	return histogram
}

func (m *NoOpMetricsRegistry) AuthTokenExpiry() metric.Float64Gauge {
	gauge, _ := otel.GetMeterProvider().Meter("client").Float64Gauge("auth_token_expiry_timestamp")
	return gauge
}
