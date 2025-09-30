package client

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/formancehq/go-libs/logging"
	"github.com/formancehq/stack/ee/stargate/internal/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

type mockMetricsRegistry struct{}

func (m *mockMetricsRegistry) ServerMessageReceivedByType() metric.Int64Counter {
	counter, _ := noop.NewMeterProvider().Meter("test").Int64Counter("test")
	return counter
}

func (m *mockMetricsRegistry) HTTPCallLatencies() metric.Int64Histogram {
	histogram, _ := noop.NewMeterProvider().Meter("test").Int64Histogram("test")
	return histogram
}

func (m *mockMetricsRegistry) HTTPCallStatusCodes() metric.Int64Counter {
	counter, _ := noop.NewMeterProvider().Meter("test").Int64Counter("test")
	return counter
}

func (m *mockMetricsRegistry) ConnectionStatus() metric.Int64UpDownCounter {
	counter, _ := noop.NewMeterProvider().Meter("test").Int64UpDownCounter("test")
	return counter
}

func (m *mockMetricsRegistry) ConnectionRetries() metric.Int64Counter {
	counter, _ := noop.NewMeterProvider().Meter("test").Int64Counter("test")
	return counter
}

func TestClient_Forward_Ping(t *testing.T) {
	client := &Client{
		metricsRegistry: &mockMetricsRegistry{},
	}

	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event: &generated.StargateServerMessage_Ping_{
			Ping: &generated.StargateServerMessage_Ping{},
		},
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.NotNil(t, result.msg)
	assert.Equal(t, "test-correlation-id", result.msg.CorrelationId)

	// Verify pong response
	pong, ok := result.msg.Event.(*generated.StargateClientMessage_Pong_)
	assert.True(t, ok, "response should be a Pong message")
	assert.NotNil(t, pong)
}

func TestClient_Forward_ApiCall_Success(t *testing.T) {
	// Create a test HTTP server to act as the gateway
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request details
		assert.Equal(t, "/api/test", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "value1", r.URL.Query().Get("param1"))
		assert.Equal(t, "value2", r.URL.Query().Get("param2"))

		// Verify request body
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, `{"test":"data"}`, string(body))

		// Send response
		w.Header().Set("X-Custom-Header", "test-value")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer testServer.Close()

	client := &Client{
		config: Config{
			GatewayUrl: testServer.URL,
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metricsRegistry: &mockMetricsRegistry{},
	}

	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event: &generated.StargateServerMessage_ApiCall{
			ApiCall: &generated.StargateServerMessage_APICall{
				Path:   "/api/test",
				Method: "POST",
				Body:   []byte(`{"test":"data"}`),
				Headers: map[string]*generated.Values{
					"Content-Type": {Values: []string{"application/json"}},
				},
				Query: map[string]*generated.Values{
					"param1": {Values: []string{"value1"}},
					"param2": {Values: []string{"value2"}},
				},
				OtlpContext: map[string]string{},
			},
		},
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.NotNil(t, result.msg)
	assert.Equal(t, "test-correlation-id", result.msg.CorrelationId)

	// Verify API call response
	apiCallResp, ok := result.msg.Event.(*generated.StargateClientMessage_ApiCallResponse)
	assert.True(t, ok, "response should be an ApiCallResponse")
	assert.NotNil(t, apiCallResp)
	assert.Equal(t, int32(http.StatusOK), apiCallResp.ApiCallResponse.StatusCode)
	assert.Equal(t, `{"result":"success"}`, string(apiCallResp.ApiCallResponse.Body))

	// Verify custom header is present
	assert.Contains(t, apiCallResp.ApiCallResponse.Headers, "X-Custom-Header")
	assert.Equal(t, []string{"test-value"}, apiCallResp.ApiCallResponse.Headers["X-Custom-Header"].Values)
}

func TestClient_Forward_ApiCall_HTTPError(t *testing.T) {
	// Create a test server that returns an error
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer testServer.Close()

	client := &Client{
		config: Config{
			GatewayUrl: testServer.URL,
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metricsRegistry: &mockMetricsRegistry{},
	}

	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event: &generated.StargateServerMessage_ApiCall{
			ApiCall: &generated.StargateServerMessage_APICall{
				Path:        "/api/test",
				Method:      "GET",
				Body:        []byte{},
				Headers:     map[string]*generated.Values{},
				Query:       map[string]*generated.Values{},
				OtlpContext: map[string]string{},
			},
		},
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.NotNil(t, result.msg)

	// Verify error response
	apiCallResp, ok := result.msg.Event.(*generated.StargateClientMessage_ApiCallResponse)
	assert.True(t, ok)
	assert.Equal(t, int32(http.StatusInternalServerError), apiCallResp.ApiCallResponse.StatusCode)
	assert.Equal(t, `{"error":"internal server error"}`, string(apiCallResp.ApiCallResponse.Body))
}

func TestClient_Forward_ApiCall_NetworkError(t *testing.T) {
	// Use a logger that implements the interface
	logger := logging.Testing()

	client := &Client{
		logger: logger,
		config: Config{
			GatewayUrl: "http://localhost:1", // Invalid URL that will fail
		},
		httpClient: &http.Client{
			Timeout: 1 * time.Millisecond, // Very short timeout to trigger error quickly
		},
		metricsRegistry: &mockMetricsRegistry{},
	}

	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event: &generated.StargateServerMessage_ApiCall{
			ApiCall: &generated.StargateServerMessage_APICall{
				Path:        "/api/test",
				Method:      "GET",
				Body:        []byte{},
				Headers:     map[string]*generated.Values{},
				Query:       map[string]*generated.Values{},
				OtlpContext: map[string]string{},
			},
		},
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	// Network error should return 500 response, not an error in the ResponseChanEvent
	assert.NoError(t, result.err)
	assert.NotNil(t, result.msg)

	apiCallResp, ok := result.msg.Event.(*generated.StargateClientMessage_ApiCallResponse)
	assert.True(t, ok)
	assert.Equal(t, int32(http.StatusInternalServerError), apiCallResp.ApiCallResponse.StatusCode)
	assert.Empty(t, apiCallResp.ApiCallResponse.Body)
}

func TestClient_Forward_ApiCall_PathNormalization(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify path is correctly normalized
		assert.Equal(t, "/api/test", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	client := &Client{
		config: Config{
			GatewayUrl: testServer.URL,
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metricsRegistry: &mockMetricsRegistry{},
	}

	tests := []struct {
		name string
		path string
	}{
		{
			name: "path with leading slash",
			path: "/api/test",
		},
		{
			name: "path without leading slash",
			path: "api/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &generated.StargateServerMessage{
				CorrelationId: "test-correlation-id",
				Event: &generated.StargateServerMessage_ApiCall{
					ApiCall: &generated.StargateServerMessage_APICall{
						Path:        tt.path,
						Method:      "GET",
						Body:        []byte{},
						Headers:     map[string]*generated.Values{},
						Query:       map[string]*generated.Values{},
						OtlpContext: map[string]string{},
					},
				},
			}

			result := client.Forward(context.Background(), msg)

			require.NotNil(t, result)
			assert.NoError(t, result.err)
			assert.NotNil(t, result.msg)
		})
	}
}

func TestClient_Forward_ApiCall_MultipleQueryValues(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify multiple values for the same query parameter
		values := r.URL.Query()["tags"]
		assert.Len(t, values, 2)
		assert.Contains(t, values, "tag1")
		assert.Contains(t, values, "tag2")

		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	client := &Client{
		config: Config{
			GatewayUrl: testServer.URL,
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metricsRegistry: &mockMetricsRegistry{},
	}

	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event: &generated.StargateServerMessage_ApiCall{
			ApiCall: &generated.StargateServerMessage_APICall{
				Path:   "/api/test",
				Method: "GET",
				Body:   []byte{},
				Headers: map[string]*generated.Values{},
				Query: map[string]*generated.Values{
					"tags": {Values: []string{"tag1", "tag2"}},
				},
				OtlpContext: map[string]string{},
			},
		},
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.NotNil(t, result.msg)
}

func TestClient_Forward_ApiCall_MultipleHeaders(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify multiple values for the same header
		values := r.Header.Values("X-Custom")
		assert.Len(t, values, 2)
		assert.Contains(t, values, "value1")
		assert.Contains(t, values, "value2")

		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	client := &Client{
		config: Config{
			GatewayUrl: testServer.URL,
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metricsRegistry: &mockMetricsRegistry{},
	}

	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event: &generated.StargateServerMessage_ApiCall{
			ApiCall: &generated.StargateServerMessage_APICall{
				Path:   "/api/test",
				Method: "GET",
				Body:   []byte{},
				Headers: map[string]*generated.Values{
					"X-Custom": {Values: []string{"value1", "value2"}},
				},
				Query:       map[string]*generated.Values{},
				OtlpContext: map[string]string{},
			},
		},
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.NotNil(t, result.msg)
}

func TestClient_Forward_UnknownMessageType(t *testing.T) {
	client := &Client{
		metricsRegistry: &mockMetricsRegistry{},
	}

	// Create a message with no event (unknown type)
	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event:         nil,
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.Nil(t, result.msg, "unknown message types should return nil message")
}

func TestClient_Forward_ApiCall_LargeBody(t *testing.T) {
	largeBody := bytes.Repeat([]byte("x"), 1024*1024) // 1MB

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, largeBody, body)

		// Echo the body back
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer testServer.Close()

	client := &Client{
		config: Config{
			GatewayUrl: testServer.URL,
		},
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metricsRegistry: &mockMetricsRegistry{},
	}

	msg := &generated.StargateServerMessage{
		CorrelationId: "test-correlation-id",
		Event: &generated.StargateServerMessage_ApiCall{
			ApiCall: &generated.StargateServerMessage_APICall{
				Path:        "/api/upload",
				Method:      "POST",
				Body:        largeBody,
				Headers:     map[string]*generated.Values{},
				Query:       map[string]*generated.Values{},
				OtlpContext: map[string]string{},
			},
		},
	}

	result := client.Forward(context.Background(), msg)

	require.NotNil(t, result)
	assert.NoError(t, result.err)
	assert.NotNil(t, result.msg)

	apiCallResp, ok := result.msg.Event.(*generated.StargateClientMessage_ApiCallResponse)
	assert.True(t, ok)
	assert.Equal(t, int32(http.StatusOK), apiCallResp.ApiCallResponse.StatusCode)
	assert.Equal(t, largeBody, apiCallResp.ApiCallResponse.Body)
}