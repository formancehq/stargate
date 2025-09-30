package interceptors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestNewConfig(t *testing.T) {
	endpoint := "https://auth.example.com"
	refreshDuration := 5 * time.Minute
	clientID := "test-client-id"
	clientSecret := "test-client-secret"

	config := NewConfig(endpoint, refreshDuration, clientID, clientSecret)

	assert.Equal(t, endpoint, config.endpoint)
	assert.Equal(t, refreshDuration, config.refreshTokenDurationBeforeExpireTime)
	assert.Equal(t, clientID, config.clientID)
	assert.Equal(t, clientSecret, config.clientSecret)
}

func TestNewAuthInterceptor(t *testing.T) {
	config := NewConfig(
		"https://auth.example.com",
		5*time.Minute,
		"client-id",
		"client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)

	require.NoError(t, err)
	assert.NotNil(t, interceptor)
	assert.NotNil(t, interceptor.httpClient)
	assert.NotNil(t, interceptor.closeChan)
	assert.Equal(t, config, interceptor.config)
	assert.Empty(t, interceptor.accessToken)
}

func TestAuthInterceptor_Close(t *testing.T) {
	config := NewConfig(
		"https://auth.example.com",
		5*time.Minute,
		"client-id",
		"client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)
	require.NoError(t, err)

	// Close should not panic
	interceptor.Close()

	// Verify channel is closed
	select {
	case <-interceptor.closeChan:
		// Channel is closed as expected
	case <-time.After(time.Second):
		t.Fatal("closeChan should be closed")
	}
}

func TestAuthInterceptor_StreamClientInterceptor(t *testing.T) {
	config := NewConfig(
		"https://auth.example.com",
		5*time.Minute,
		"client-id",
		"client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)
	require.NoError(t, err)

	testToken := "test-access-token"
	interceptor.accessToken = testToken

	// Create a mock streamer
	mockStreamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// Verify that the authorization header was added
		md, ok := metadata.FromOutgoingContext(ctx)
		require.True(t, ok, "metadata should be present in context")

		authValues := md.Get("authorization")
		require.Len(t, authValues, 1, "should have exactly one authorization value")
		assert.Equal(t, testToken, authValues[0])

		return nil, nil
	}

	interceptorFunc := interceptor.StreamClientInterceptor()
	ctx := context.Background()

	_, err = interceptorFunc(ctx, nil, nil, "/test.method", mockStreamer)
	require.NoError(t, err)
}

func TestAuthInterceptor_RefreshToken_Success(t *testing.T) {
	// Create a mock OIDC discovery server
	discoveryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			// Include the full issuer URL to match validation
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			issuerURL := scheme + "://" + r.Host

			response := map[string]interface{}{
				"issuer":         issuerURL,
				"token_endpoint": issuerURL + "/oauth/token",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		if r.URL.Path == "/oauth/token" {
			// Verify it's a client credentials grant
			err := r.ParseForm()
			require.NoError(t, err)
			assert.Equal(t, "client_credentials", r.FormValue("grant_type"))

			response := map[string]interface{}{
				"access_token": "test-token-12345",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer discoveryServer.Close()

	config := NewConfig(
		discoveryServer.URL,
		5*time.Minute,
		"test-client-id",
		"test-client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)
	require.NoError(t, err)

	expiry, err := interceptor.refreshToken()
	require.NoError(t, err)

	assert.Equal(t, "test-token-12345", interceptor.accessToken)
	assert.False(t, expiry.IsZero(), "expiry time should be set")
	assert.True(t, expiry.After(time.Now()), "expiry should be in the future")
}

func TestAuthInterceptor_RefreshToken_InvalidEndpoint(t *testing.T) {
	config := NewConfig(
		"http://invalid-endpoint-that-does-not-exist-12345.local",
		5*time.Minute,
		"test-client-id",
		"test-client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)
	require.NoError(t, err)

	_, err = interceptor.refreshToken()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot discover endpoint")
}

func TestAuthInterceptor_RefreshToken_InvalidTokenResponse(t *testing.T) {
	// Create a mock server that returns invalid token response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			issuerURL := scheme + "://" + r.Host

			response := map[string]interface{}{
				"issuer":         issuerURL,
				"token_endpoint": issuerURL + "/oauth/token",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		if r.URL.Path == "/oauth/token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "invalid_client"}`))
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := NewConfig(
		server.URL,
		5*time.Minute,
		"invalid-client-id",
		"invalid-client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)
	require.NoError(t, err)

	_, err = interceptor.refreshToken()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot fetch token")
}

func TestAuthInterceptor_ScheduleRefreshToken(t *testing.T) {
	// Create a mock OIDC server with very short token expiry
	var tokenCallCount int32
	var tokensMu sync.Mutex
	var tokens []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			issuerURL := scheme + "://" + r.Host

			response := map[string]interface{}{
				"issuer":         issuerURL,
				"token_endpoint": issuerURL + "/oauth/token",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		if r.URL.Path == "/oauth/token" {
			count := atomic.AddInt32(&tokenCallCount, 1)
			token := "token-" + time.Now().Format("20060102150405.000000")

			tokensMu.Lock()
			tokens = append(tokens, token)
			tokensMu.Unlock()

			response := map[string]interface{}{
				"access_token": token,
				"token_type":   "Bearer",
				"expires_in":   1, // 1 second expiry
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)

			t.Logf("Token call %d: %s", count, token)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := NewConfig(
		server.URL,
		500*time.Millisecond, // Refresh 500ms before expiry
		"test-client-id",
		"test-client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)
	require.NoError(t, err)

	err = interceptor.ScheduleRefreshToken()
	require.NoError(t, err)

	initialCallCount := atomic.LoadInt32(&tokenCallCount)
	assert.Greater(t, initialCallCount, int32(0), "initial token should be fetched")

	// Wait for automatic refresh (should happen in ~500ms)
	time.Sleep(1200 * time.Millisecond)

	finalCallCount := atomic.LoadInt32(&tokenCallCount)
	assert.Greater(t, finalCallCount, initialCallCount, "token endpoint should have been called again")

	// Verify we got multiple different tokens
	tokensMu.Lock()
	uniqueTokens := len(tokens)
	tokensMu.Unlock()
	assert.GreaterOrEqual(t, uniqueTokens, 2, "should have received at least 2 tokens")

	// Clean up
	interceptor.Close()
}

func TestAuthInterceptor_ScheduleRefreshToken_CloseStopsRefresh(t *testing.T) {
	var tokenCallCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			issuerURL := scheme + "://" + r.Host

			response := map[string]interface{}{
				"issuer":         issuerURL,
				"token_endpoint": issuerURL + "/oauth/token",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		if r.URL.Path == "/oauth/token" {
			atomic.AddInt32(&tokenCallCount, 1)
			response := map[string]interface{}{
				"access_token": "token-test",
				"token_type":   "Bearer",
				"expires_in":   1,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := NewConfig(
		server.URL,
		500*time.Millisecond,
		"test-client-id",
		"test-client-secret",
	)

	interceptor, err := NewAuthInterceptor(config)
	require.NoError(t, err)

	err = interceptor.ScheduleRefreshToken()
	require.NoError(t, err)

	initialCallCount := atomic.LoadInt32(&tokenCallCount)

	// Close the interceptor immediately
	interceptor.Close()

	// Wait and verify no more token refreshes happen
	time.Sleep(800 * time.Millisecond)
	assert.Equal(t, initialCallCount, atomic.LoadInt32(&tokenCallCount), "no additional token calls should be made after close")
}

func TestConfig_Fields(t *testing.T) {
	config := Config{
		refreshTokenDurationBeforeExpireTime: 10 * time.Minute,
		clientID:                             "my-client-id",
		clientSecret:                         "my-client-secret",
		endpoint:                             "https://auth.example.com",
	}

	assert.Equal(t, 10*time.Minute, config.refreshTokenDurationBeforeExpireTime)
	assert.Equal(t, "my-client-id", config.clientID)
	assert.Equal(t, "my-client-secret", config.clientSecret)
	assert.Equal(t, "https://auth.example.com", config.endpoint)
}

func TestDefaultWaitingTime(t *testing.T) {
	assert.Equal(t, 10*time.Second, defaultWaitingTime)
}