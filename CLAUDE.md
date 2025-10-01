# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Stargate is a gRPC-based reverse proxy client that connects internal stacks to a central gateway server. It receives API call requests via bidirectional gRPC streaming, forwards them to a local gateway, and returns responses. Part of the Formance stack enterprise edition.

## Rules
1. ❌ **Ne JAMAIS utiliser `context.Background()`** - toujours utiliser un contexte approprié (utiliser `context.TODO()` pour les opérations de long terme)
2. 📝 **Utiliser `require` au lieu de `assert`** dans les tests - pour arrêter l'exécution immédiatement en cas d'échec critique
3. 🚫 **Ne JAMAIS utiliser `panic`** - toujours utiliser un error handling propre avec des logs structurés et des réponses HTTP appropriées
4. 🔍 **Utiliser des correlation IDs** - les messages `ResponseChanEvent` ont un champ `correlationID` pour le tracking et le debugging

## Build and Development Commands

### Build
```bash
# Compile the binary
earthly +compile

# Build Docker image
earthly +build-image

# Run all tests
earthly +tests
```

### Code Quality
```bash
# Run linter
earthly +lint

# Run pre-commit checks (includes tidy + lint)
earthly +pre-commit

# Tidy go modules
earthly +tidy
```

### Generate Code
```bash
# Regenerate gRPC code from stargate.proto
earthly +grpc-generate
```

### Local Development
```bash
# Run the client (requires configuration flags)
go run main.go client \
  --organization-id=<org-id> \
  --stack-id=<stack-id> \
  --stargate-server-url=<server-url> \
  --gateway-url=<gateway-url> \
  --stargate-auth-issuer-url=<issuer-url> \
  --stargate-auth-client-id=<client-id> \
  --stargate-auth-client-secret=<client-secret>
```

## Architecture

### Core Components

**gRPC Bidirectional Streaming**: The heart of Stargate is a bidirectional gRPC stream ([internal/client/client.go:195](internal/client/client.go#L195)) that maintains a persistent connection to the stargate server. Messages flow in both directions:
- Server → Client: `StargateServerMessage` containing API call requests or pings
- Client → Server: `StargateClientMessage` containing API call responses or pongs

**Message Forwarding Flow** ([internal/client/client.go:426](internal/client/client.go#L426)):
1. Client receives `StargateServerMessage` from gRPC stream
2. Worker pool processes message asynchronously
3. For API calls: HTTP request forwarded to local gateway URL
4. Response converted to `StargateClientMessage`
5. Message sent back through gRPC stream

**Connection Resilience** ([internal/client/client.go:195](internal/client/client.go#L195)): The `Run()` method implements exponential backoff retry logic with configurable parameters:
- Retries on transient errors (network, unavailable, etc.)
- Non-retryable errors: Canceled, InvalidArgument, NotFound, PermissionDenied, Unauthenticated
- Jittered exponential backoff to prevent thundering herd
- Automatic reconnection on connection loss

**OAuth2 Authentication** ([internal/client/interceptors/auth_interceptor.go](internal/client/interceptors/auth_interceptor.go)): The auth interceptor handles OAuth2 client credentials flow:
- Discovers OIDC endpoints from issuer URL
- Automatically refreshes access tokens before expiration
- Injects bearer token into gRPC metadata

**Worker Pool** ([internal/client/client.go:348](internal/client/client.go#L348)): Uses `alitto/pond` for concurrent message processing with configurable limits on workers and queued tasks. This prevents overwhelming the system under high load.

**Circuit Breaker** ([internal/client/circuit_breaker_client.go](internal/client/circuit_breaker_client.go)): Wraps the HTTP client with a circuit breaker pattern using `sony/gobreaker` to protect the gateway from cascading failures:
- Configurable failure threshold and timeout
- Transitions between closed (normal), open (failing), and half-open (recovery) states
- Tracks both network errors and HTTP 5xx responses as failures
- Exposes circuit breaker state metrics for monitoring

### Key Files

- [stargate.proto](stargate.proto): Protocol buffer definitions for bidirectional streaming
- [internal/generated/](internal/generated/): Generated gRPC code (do not edit manually)
- [internal/client/client.go](internal/client/client.go): Core client logic, retry handling, message forwarding
- [internal/client/circuit_breaker_client.go](internal/client/circuit_breaker_client.go): Circuit breaker HTTP client wrapper
- [internal/client/module.go](internal/client/module.go): Uber FX dependency injection setup
- [internal/client/interceptors/auth_interceptor.go](internal/client/interceptors/auth_interceptor.go): OAuth2 token management
- [cmd/client.go](cmd/client.go): CLI flag definitions and configuration resolution

### Configuration

All configuration is passed via CLI flags (see [cmd/root.go](cmd/root.go#L33-L58)):
- **Identity**: `--organization-id`, `--stack-id`
- **Endpoints**: `--stargate-server-url`, `--gateway-url`
- **Auth**: `--stargate-auth-issuer-url`, `--stargate-auth-client-id`, `--stargate-auth-client-secret`
- **TLS**: `--tls-enabled`, `--tls-ca-cert`, `--tls-insecure-skip-verify`
- **Performance**: `--worker-pool-max-worker`, `--worker-pool-max-tasks`, `--client-chan-size`
- **Retry**: `--max-retries`, `--initial-retry-delay`, `--max-retry-delay`, `--retry-multiplier`
- **Circuit Breaker**: `--circuit-breaker-max-requests`, `--circuit-breaker-interval`, `--circuit-breaker-timeout`, `--circuit-breaker-consecutive-failures`

### Dependencies

- **Uber FX**: Dependency injection framework
- **gRPC**: Core communication protocol
- **go-chi**: HTTP router for health/info endpoints
- **alitto/pond**: Worker pool for concurrent processing
- **sony/gobreaker**: Circuit breaker pattern implementation
- **zitadel/oidc**: OAuth2/OIDC client
- **OpenTelemetry**: Observability (traces and metrics)
- **formancehq/go-libs**: Internal shared libraries

## Development Notes

- The application uses Uber FX lifecycle hooks for graceful startup/shutdown
- Health endpoint exposed via go-chi router (defined in [internal/client/routes/](internal/client/routes/))
- OpenTelemetry context propagation between gRPC and HTTP requests ([internal/opentelemetry/context.go](internal/opentelemetry/context.go))
- gRPC metrics tracked in [internal/grpcmetrics/](internal/grpcmetrics/)
- The client automatically shuts down on non-retryable errors ([internal/client/module.go:115](internal/client/module.go#L115))

### Graceful Shutdown

The application has a configurable shutdown timeout (default: 30s, flag: `--shutdown-timeout`):
- During shutdown, the worker pool is gracefully stopped first
- If tasks don't complete within the timeout, metrics are recorded and forced cleanup occurs
- Metric `shutdown_timeouts_total` tracks timeout occurrences
- Task statistics (waiting tasks, running workers) are logged when timeout occurs

### Key Metrics

- `forwarding_errors_total`: Tracks HTTP forwarding failures (non-fatal, connection continues)
- `shutdown_timeouts_total`: Tracks graceful shutdown timeouts
- `auth_token_refresh_errors_total`: Tracks OAuth2 token refresh failures
- `auth_token_refresh_duration_milliseconds`: Histogram of token refresh operation timing
- `auth_token_expiry_timestamp`: Gauge for current token expiry timestamp

### Error Handling Philosophy

- **Never crash the entire service**: Single request failures should not terminate the gRPC connection
- **Graceful degradation**: Send error responses (HTTP 500) instead of panicking or killing connections
- **Correlation IDs**: Every message has a correlation ID for tracking through logs and metrics
- **Structured logging**: All errors are logged with structured fields for easy aggregation and debugging

### Logging Conventions

All logging in this codebase follows these conventions:

1. **Always use structured logging with `WithFields()`**:
   ```go
   // ✅ GOOD
   logger.WithFields(map[string]any{
       "error": err.Error(),
       "path":  request.Path,
   }).Error("failed to process request")

   // ❌ BAD - avoid unstructured logs
   logger.Errorf("failed to process request: %v", err)
   ```

2. **Use `map[string]any` for field maps** (Go 1.18+):
   ```go
   // ✅ GOOD
   map[string]any{"key": "value"}

   // ❌ BAD
   map[string]interface{}{"key": "value"}
   ```

3. **Use context helpers for common fields**:
   The `Client` struct provides a `logWithContext()` helper that automatically includes `organization_id` and `stack_id`:
   ```go
   // Automatically includes organization_id and stack_id
   c.logWithContext().Info("starting operation")

   // Add additional fields as needed
   c.logWithContext().WithFields(map[string]any{
       "retry_count": retries,
   }).Warn("retrying operation")
   ```

4. **Use lowercase, descriptive messages without punctuation**:
   ```go
   // ✅ GOOD
   .Error("failed to connect to database")

   // ❌ BAD
   .Error("Failed to connect to database!")
   ```

## Testing

Tests run in Earthly with a PostgreSQL container available. Currently no test files exist in the repository.

## CI/CD

- GitHub Actions workflow: [.github/workflows/main.yml](.github/workflows/main.yml)
- Checks PR title format (conventional commits)
- Runs `earthly +pre-commit` to verify code quality
- Uses Earthly for all build/test operations