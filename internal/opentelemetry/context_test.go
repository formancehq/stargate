package opentelemetry

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestPropagator_IsComposite(t *testing.T) {
	// Verify that Propagator is a composite propagator
	require.NotNil(t, Propagator)

	// Verify it has fields which indicates it's functional
	fields := Propagator.Fields()
	require.NotEmpty(t, fields, "Propagator should have fields")
}

func TestPropagator_InjectAndExtract_WithHTTPHeader(t *testing.T) {
	// Create a context with trace information
	ctx := context.Background()

	// Inject context into HTTP headers (even without span, should work)
	header := http.Header{}
	Propagator.Inject(ctx, propagation.HeaderCarrier(header))

	// Extract context from headers (should work without error)
	extractedCtx := Propagator.Extract(context.Background(), propagation.HeaderCarrier(header))
	require.NotNil(t, extractedCtx)
}

func TestPropagator_InjectAndExtract_WithMapCarrier(t *testing.T) {
	// Create a context
	ctx := context.Background()

	// Inject context into map
	carrier := make(map[string]string)
	Propagator.Inject(ctx, propagation.MapCarrier(carrier))

	// Extract context from map
	extractedCtx := Propagator.Extract(context.Background(), propagation.MapCarrier(carrier))
	require.NotNil(t, extractedCtx)
}

func TestPropagator_Fields(t *testing.T) {
	// Get the fields that the propagator will set
	fields := Propagator.Fields()

	// Should include fields from both TraceContext and Baggage propagators
	require.NotEmpty(t, fields, "propagator should declare fields")

	// TraceContext uses "traceparent" and "tracestate"
	// Baggage uses "baggage"
	hasTraceparent := false
	hasBaggage := false

	for _, field := range fields {
		if field == "traceparent" {
			hasTraceparent = true
		}
		if field == "baggage" {
			hasBaggage = true
		}
	}

	require.True(t, hasTraceparent, "should include traceparent field from TraceContext")
	require.True(t, hasBaggage, "should include baggage field from Baggage")
}

func TestPropagator_ExtractEmptyCarrier(t *testing.T) {
	// Extract from empty carrier should not panic
	emptyCarrier := make(map[string]string)
	ctx := Propagator.Extract(context.Background(), propagation.MapCarrier(emptyCarrier))

	require.NotNil(t, ctx, "extract should return a valid context even with empty carrier")
}

func TestPropagator_InjectEmptyContext(t *testing.T) {
	// Inject empty context should not panic
	carrier := make(map[string]string)
	Propagator.Inject(context.Background(), propagation.MapCarrier(carrier))

	// Empty context won't inject trace information, but shouldn't error
	require.NotNil(t, carrier)
}

func TestPropagator_RoundTrip(t *testing.T) {
	// Create a context with baggage
	ctx := context.Background()

	// Create a simple tracer and span
	tracer := noop.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(ctx, "test-span")
	defer span.End()

	// Round trip: inject then extract
	carrier := make(map[string]string)
	Propagator.Inject(ctx, propagation.MapCarrier(carrier))

	extractedCtx := Propagator.Extract(context.Background(), propagation.MapCarrier(carrier))

	require.NotNil(t, extractedCtx, "extracted context should not be nil")
}

func TestPropagator_MultipleInjects(t *testing.T) {
	ctx := context.Background()

	// Inject into first carrier
	carrier1 := make(map[string]string)
	Propagator.Inject(ctx, propagation.MapCarrier(carrier1))

	// Inject into second carrier
	carrier2 := make(map[string]string)
	Propagator.Inject(ctx, propagation.MapCarrier(carrier2))

	// Should not panic - content may be empty without active span
	require.NotNil(t, carrier1)
	require.NotNil(t, carrier2)
}

func TestPropagator_HTTPHeaderCarrier(t *testing.T) {
	ctx := context.Background()
	tracer := noop.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(ctx, "http-request")
	defer span.End()

	// Create HTTP request and inject context
	req, err := http.NewRequestWithContext(ctx, "GET", "http://example.com", nil)
	require.NoError(t, err)

	Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

	// Extract from the request headers
	extractedCtx := Propagator.Extract(context.Background(), propagation.HeaderCarrier(req.Header))
	require.NotNil(t, extractedCtx)
}

func TestPropagator_PreservesOtherHeaders(t *testing.T) {
	ctx := context.Background()
	tracer := noop.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(ctx, "test-span")
	defer span.End()

	// Create headers with existing values
	header := http.Header{}
	header.Set("X-Custom-Header", "custom-value")
	header.Set("Content-Type", "application/json")

	originalHeaders := make(map[string][]string)
	for k, v := range header {
		originalHeaders[k] = v
	}

	// Inject trace context
	Propagator.Inject(ctx, propagation.HeaderCarrier(header))

	// Verify original headers are preserved
	require.Equal(t, "custom-value", header.Get("X-Custom-Header"))
	require.Equal(t, "application/json", header.Get("Content-Type"))
}

func TestPropagator_EmptyStringValues(t *testing.T) {
	carrier := map[string]string{
		"traceparent": "",
		"baggage":     "",
	}

	// Should not panic with empty string values
	ctx := Propagator.Extract(context.Background(), propagation.MapCarrier(carrier))
	require.NotNil(t, ctx)
}

func TestPropagator_GlobalVariable(t *testing.T) {
	// Verify the global Propagator variable is initialized correctly
	require.NotNil(t, Propagator)

	// Should be usable immediately
	carrier := make(map[string]string)
	ctx := context.Background()

	// Should not panic
	Propagator.Inject(ctx, propagation.MapCarrier(carrier))
	Propagator.Extract(ctx, propagation.MapCarrier(carrier))
}