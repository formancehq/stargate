package client

import (
	"context"
	"fmt"
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
	organizationID  string
	stackID         string
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
	organizationID string,
	stackID string,
) *CircuitBreakerHTTPClient {
	// Validate configuration
	if config.ConsecutiveFailures == 0 {
		logger.WithFields(map[string]any{
			"default": 1,
		}).Info("consecutive failures is 0, using default")
		config.ConsecutiveFailures = 1
	}
	if config.MaxRequests == 0 {
		logger.WithFields(map[string]any{
			"default": 1,
		}).Info("max requests is 0, using default")
		config.MaxRequests = 1
	}
	if config.Timeout == 0 {
		logger.WithFields(map[string]any{
			"default": "30s",
		}).Info("timeout is 0, using default")
		config.Timeout = 30 * time.Second
	}

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
				"organization_id": organizationID,
				"stack_id":        stackID,
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
		organizationID:  organizationID,
		stackID:         stackID,
	}
}

func (c *CircuitBreakerHTTPClient) Do(req *http.Request) (*http.Response, error) {
	result, err := c.breaker.Execute(func() (interface{}, error) {
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		// Treat 5xx responses as failures for circuit breaker
		if resp.StatusCode >= 500 {
			// Close the body to avoid leaking resources
			_ = resp.Body.Close()
			return nil, &httpError{StatusCode: resp.StatusCode}
		}
		return resp, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*http.Response), nil
}

type httpError struct {
	StatusCode int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}