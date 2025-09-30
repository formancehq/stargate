package client

import (
	"context"
	"net/http"
	"time"

	"github.com/formancehq/go-libs/logging"
	"github.com/formancehq/stack/ee/stargate/internal/grpcmetrics"
	"github.com/sony/gobreaker"
)

// HTTPClient is an interface for making HTTP requests
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type CircuitBreakerHTTPClient struct {
	client          *http.Client
	breaker         *gobreaker.CircuitBreaker
	logger          logging.Logger
	metricsRegistry grpcmetrics.MetricsRegistry
}

type CircuitBreakerConfig struct {
	MaxRequests        uint32
	Interval           time.Duration
	Timeout            time.Duration
	ConsecutiveFailures uint32
}

func NewCircuitBreakerHTTPClient(
	client *http.Client,
	config CircuitBreakerConfig,
	logger logging.Logger,
	metricsRegistry grpcmetrics.MetricsRegistry,
) *CircuitBreakerHTTPClient {
	settings := gobreaker.Settings{
		Name:        "gateway-http",
		MaxRequests: config.MaxRequests,
		Interval:    config.Interval,
		Timeout:     config.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= config.ConsecutiveFailures
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.WithFields(map[string]any{
				"circuit_breaker": name,
				"from_state":      from.String(),
				"to_state":        to.String(),
			}).Info("circuit breaker state changed")

			// Update metrics based on state
			var stateValue float64
			switch to {
			case gobreaker.StateClosed:
				stateValue = 0
			case gobreaker.StateHalfOpen:
				stateValue = 1
			case gobreaker.StateOpen:
				stateValue = 2
			}
			metricsRegistry.CircuitBreakerState().Record(context.Background(), stateValue)
		},
	}

	return &CircuitBreakerHTTPClient{
		client:          client,
		breaker:         gobreaker.NewCircuitBreaker(settings),
		logger:          logger,
		metricsRegistry: metricsRegistry,
	}
}

func (c *CircuitBreakerHTTPClient) Do(req *http.Request) (*http.Response, error) {
	result, err := c.breaker.Execute(func() (interface{}, error) {
		return c.client.Do(req)
	})
	if err != nil {
		return nil, err
	}
	return result.(*http.Response), nil
}