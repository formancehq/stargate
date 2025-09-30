package client

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestShouldRetry(t *testing.T) {
	c := &Client{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error should not retry",
			err:      nil,
			expected: false,
		},
		{
			name:     "canceled error should not retry",
			err:      status.Error(codes.Canceled, "canceled"),
			expected: false,
		},
		{
			name:     "invalid argument error should not retry",
			err:      status.Error(codes.InvalidArgument, "invalid"),
			expected: false,
		},
		{
			name:     "not found error should not retry",
			err:      status.Error(codes.NotFound, "not found"),
			expected: false,
		},
		{
			name:     "already exists error should not retry",
			err:      status.Error(codes.AlreadyExists, "already exists"),
			expected: false,
		},
		{
			name:     "permission denied error should not retry",
			err:      status.Error(codes.PermissionDenied, "permission denied"),
			expected: false,
		},
		{
			name:     "unauthenticated error should not retry",
			err:      status.Error(codes.Unauthenticated, "unauthenticated"),
			expected: false,
		},
		{
			name:     "unavailable error should retry",
			err:      status.Error(codes.Unavailable, "unavailable"),
			expected: true,
		},
		{
			name:     "deadline exceeded should retry",
			err:      status.Error(codes.DeadlineExceeded, "deadline exceeded"),
			expected: true,
		},
		{
			name:     "unknown error should retry",
			err:      status.Error(codes.Unknown, "unknown"),
			expected: true,
		},
		{
			name:     "non-gRPC error should retry",
			err:      errors.New("some error"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.shouldRetry(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateBackoff(t *testing.T) {
	config := Config{
		InitialRetryDelay: time.Second,
		MaxRetryDelay:     30 * time.Second,
		RetryMultiplier:   2.0,
	}
	c := &Client{config: config}

	tests := []struct {
		name        string
		retryCount  int
		minExpected time.Duration
		maxExpected time.Duration
	}{
		{
			name:        "first retry",
			retryCount:  1,
			minExpected: 800 * time.Millisecond,  // 1s - 20% jitter
			maxExpected: 1200 * time.Millisecond, // 1s + 20% jitter
		},
		{
			name:        "second retry",
			retryCount:  2,
			minExpected: 1600 * time.Millisecond, // 2s - 20% jitter
			maxExpected: 2400 * time.Millisecond, // 2s + 20% jitter
		},
		{
			name:        "third retry",
			retryCount:  3,
			minExpected: 3200 * time.Millisecond, // 4s - 20% jitter
			maxExpected: 4800 * time.Millisecond, // 4s + 20% jitter
		},
		{
			name:        "should cap at max delay",
			retryCount:  10,
			minExpected: 24 * time.Second,  // 30s - 20% jitter
			maxExpected: 36 * time.Second,  // 30s + 20% jitter (capped)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run multiple times to account for randomness
			for i := 0; i < 10; i++ {
				delay := c.calculateBackoff(tt.retryCount)
				assert.GreaterOrEqual(t, delay, tt.minExpected, "delay should be >= minimum expected")
				assert.LessOrEqual(t, delay, tt.maxExpected, "delay should be <= maximum expected")
			}
		})
	}
}

func TestCalculateBackoff_NonNegative(t *testing.T) {
	config := Config{
		InitialRetryDelay: time.Millisecond,
		MaxRetryDelay:     time.Second,
		RetryMultiplier:   2.0,
	}
	c := &Client{config: config}

	// Test that delay is never negative even with jitter
	for i := 1; i <= 20; i++ {
		delay := c.calculateBackoff(i)
		assert.GreaterOrEqual(t, delay, time.Duration(0), "delay should never be negative")
	}
}

func TestNewClientConfig(t *testing.T) {
	config := NewClientConfig(
		"org-123",
		"stack-456",
		100,
		"http://localhost:8080",
		30*time.Second,
		100,
		10,
	)

	assert.Equal(t, "org-123", config.OrganizationID)
	assert.Equal(t, "stack-456", config.StackID)
	assert.Equal(t, 100, config.ChanSize)
	assert.Equal(t, "http://localhost:8080", config.GatewayUrl)
	assert.Equal(t, 30*time.Second, config.HTTPClientTimeout)
	assert.Equal(t, 100, config.HTTPMaxIdleConns)
	assert.Equal(t, 10, config.HTTPMaxIdleConnsPerHost)

	// Test default retry configuration
	assert.Equal(t, 5, config.MaxRetries)
	assert.Equal(t, time.Second, config.InitialRetryDelay)
	assert.Equal(t, 30*time.Second, config.MaxRetryDelay)
	assert.Equal(t, 2.0, config.RetryMultiplier)
}

func TestNewWorkerPoolConfig(t *testing.T) {
	config := NewWorkerPoolConfig(10, 100)

	assert.Equal(t, 10, config.MaxWorkers)
	assert.Equal(t, 100, config.MaxTasks)
}

func TestNewClient_TrimGatewayURL(t *testing.T) {
	tests := []struct {
		name        string
		gatewayUrl  string
		expectedUrl string
	}{
		{
			name:        "URL with trailing slash",
			gatewayUrl:  "http://localhost:8080/",
			expectedUrl: "http://localhost:8080",
		},
		{
			name:        "URL without trailing slash",
			gatewayUrl:  "http://localhost:8080",
			expectedUrl: "http://localhost:8080",
		},
		{
			name:        "URL with multiple trailing slashes",
			gatewayUrl:  "http://localhost:8080///",
			expectedUrl: "http://localhost:8080//",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				GatewayUrl:              tt.gatewayUrl,
				HTTPClientTimeout:       time.Second,
				HTTPMaxIdleConns:        10,
				HTTPMaxIdleConnsPerHost: 2,
			}

			client := NewClient(
				nil,
				config,
				NewWorkerPoolConfig(1, 10),
				nil,
				"localhost:9000",
				false,
				"",
				false,
				nil,
			)

			assert.Equal(t, tt.expectedUrl, client.config.GatewayUrl)
		})
	}
}

func TestNewClient_HTTPClientConfiguration(t *testing.T) {
	config := Config{
		GatewayUrl:              "http://localhost:8080",
		HTTPClientTimeout:       5 * time.Second,
		HTTPMaxIdleConns:        100,
		HTTPMaxIdleConnsPerHost: 20,
	}

	client := NewClient(
		nil,
		config,
		NewWorkerPoolConfig(10, 100),
		nil,
		"localhost:9000",
		false,
		"",
		false,
		nil,
	)

	assert.NotNil(t, client.httpClient)
	assert.Equal(t, 5*time.Second, client.httpClient.Timeout)
}

func TestResponseChanEvent(t *testing.T) {
	// Test struct creation
	event := &ResponseChanEvent{
		msg: nil,
		err: errors.New("test error"),
	}

	assert.Nil(t, event.msg)
	assert.NotNil(t, event.err)
	assert.Equal(t, "test error", event.err.Error())
}

func TestClient_Close_NilConnection(t *testing.T) {
	client := NewClient(
		nil,
		Config{
			GatewayUrl:              "http://localhost:8080",
			HTTPClientTimeout:       time.Second,
			HTTPMaxIdleConns:        10,
			HTTPMaxIdleConnsPerHost: 2,
		},
		NewWorkerPoolConfig(1, 10),
		nil,
		"localhost:9000",
		false,
		"",
		false,
		nil,
	)

	err := client.Close()
	assert.NoError(t, err)
}

func TestConfig(t *testing.T) {
	config := Config{
		OrganizationID:          "test-org",
		StackID:                 "test-stack",
		ChanSize:                50,
		GatewayUrl:              "http://gateway.local",
		HTTPClientTimeout:       10 * time.Second,
		HTTPMaxIdleConns:        200,
		HTTPMaxIdleConnsPerHost: 50,
		MaxRetries:              3,
		InitialRetryDelay:       500 * time.Millisecond,
		MaxRetryDelay:           10 * time.Second,
		RetryMultiplier:         1.5,
	}

	assert.Equal(t, "test-org", config.OrganizationID)
	assert.Equal(t, "test-stack", config.StackID)
	assert.Equal(t, 50, config.ChanSize)
	assert.Equal(t, "http://gateway.local", config.GatewayUrl)
	assert.Equal(t, 10*time.Second, config.HTTPClientTimeout)
	assert.Equal(t, 200, config.HTTPMaxIdleConns)
	assert.Equal(t, 50, config.HTTPMaxIdleConnsPerHost)
	assert.Equal(t, 3, config.MaxRetries)
	assert.Equal(t, 500*time.Millisecond, config.InitialRetryDelay)
	assert.Equal(t, 10*time.Second, config.MaxRetryDelay)
	assert.Equal(t, 1.5, config.RetryMultiplier)
}

func TestWorkerPoolConfig(t *testing.T) {
	config := WorkerPoolConfig{
		MaxWorkers: 5,
		MaxTasks:   50,
	}

	assert.Equal(t, 5, config.MaxWorkers)
	assert.Equal(t, 50, config.MaxTasks)
}