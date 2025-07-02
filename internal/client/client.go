package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/alitto/pond"
	"github.com/formancehq/go-libs/logging"
	"github.com/formancehq/stack/ee/stargate/internal/generated"
	metrics "github.com/formancehq/stack/ee/stargate/internal/grpcmetrics"
	"github.com/formancehq/stack/ee/stargate/internal/opentelemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type WorkerPoolConfig struct {
	MaxWorkers int
	MaxTasks   int
}

func NewWorkerPoolConfig(maxWorkers, maxTasks int) WorkerPoolConfig {
	return WorkerPoolConfig{
		MaxWorkers: maxWorkers,
		MaxTasks:   maxTasks,
	}
}

type Config struct {
	OrganizationID          string
	StackID                 string
	ChanSize                int
	GatewayUrl              string
	HTTPClientTimeout       time.Duration
	HTTPMaxIdleConns        int
	HTTPMaxIdleConnsPerHost int
	MaxRetries              int
	InitialRetryDelay       time.Duration
	MaxRetryDelay           time.Duration
	RetryMultiplier         float64
}

func NewClientConfig(
	organizationID string,
	stackID string,
	chanSize int,
	gatewayUrl string,
	httpClientTimeout time.Duration,
	httpMaxIdleConns int,
	httpMaxIdleConnsPerHost int,
) Config {
	return Config{
		OrganizationID:          organizationID,
		StackID:                 stackID,
		ChanSize:                chanSize,
		GatewayUrl:              gatewayUrl,
		HTTPClientTimeout:       httpClientTimeout,
		HTTPMaxIdleConns:        httpMaxIdleConns,
		HTTPMaxIdleConnsPerHost: httpMaxIdleConnsPerHost,
		MaxRetries:              5,
		InitialRetryDelay:       time.Second,
		MaxRetryDelay:           30 * time.Second,
		RetryMultiplier:         2.0,
	}
}

type StreamInterceptor interface {
	StreamClientInterceptor() grpc.StreamClientInterceptor
}

type Client struct {
	logger         logging.Logger
	config         Config
	stargateClient generated.StargateServiceClient
	httpClient     *http.Client

	workerPool      *pond.WorkerPool
	metricsRegistry metrics.MetricsRegistry

	// gRPC connection parameters
	serverURL             string
	tlsEnabled            bool
	tlsCACertificate      string
	tlsInsecureSkipVerify bool
	authInterceptor       StreamInterceptor
	grpcConn              *grpc.ClientConn
}

func NewClient(
	l logging.Logger,
	clientConfig Config,
	workerPoolConfig WorkerPoolConfig,
	metricsRegistry metrics.MetricsRegistry,
	serverURL string,
	tlsEnabled bool,
	tlsCACertificate string,
	tlsInsecureSkipVerify bool,
	authInterceptor StreamInterceptor,
) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = clientConfig.HTTPMaxIdleConns
	transport.MaxIdleConnsPerHost = clientConfig.HTTPMaxIdleConnsPerHost

	clientConfig.GatewayUrl = strings.TrimSuffix(clientConfig.GatewayUrl, "/")

	return &Client{
		logger:                l,
		config:                clientConfig,
		workerPool:            pond.New(workerPoolConfig.MaxWorkers, workerPoolConfig.MaxTasks),
		metricsRegistry:       metricsRegistry,
		serverURL:             serverURL,
		tlsEnabled:            tlsEnabled,
		tlsCACertificate:      tlsCACertificate,
		tlsInsecureSkipVerify: tlsInsecureSkipVerify,
		authInterceptor:       authInterceptor,
		httpClient: &http.Client{
			Timeout:   clientConfig.HTTPClientTimeout,
			Transport: transport,
		},
	}
}

type ResponseChanEvent struct {
	msg *generated.StargateClientMessage
	err error
}

func (c *Client) createGRPCConnection() error {
	var credential credentials.TransportCredentials
	if !c.tlsEnabled {
		c.logger.Infof("TLS not enabled")
		credential = insecure.NewCredentials()
	} else {
		var certPool *x509.CertPool
		if c.tlsCACertificate != "" {
			certPool = x509.NewCertPool()
			c.logger.Infof("Load server certificate from config")
			if !certPool.AppendCertsFromPEM([]byte(c.tlsCACertificate)) {
				return fmt.Errorf("failed to add server CA's certificate")
			}
		} else {
			var err error
			certPool, err = x509.SystemCertPool()
			if err != nil {
				return err
			}
		}

		if c.tlsInsecureSkipVerify {
			c.logger.Infof("Disable certificate checks")
		}

		credential = credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: c.tlsInsecureSkipVerify,
			RootCAs:            certPool,
		})
	}

	// Close existing connection if any
	if c.grpcConn != nil {
		c.grpcConn.Close()
	}

	conn, err := grpc.Dial(
		c.serverURL,
		grpc.WithStreamInterceptor(c.authInterceptor.StreamClientInterceptor()),
		grpc.WithTransportCredentials(credential),
	)
	if err != nil {
		c.logger.Errorf("failed to connect to stargate server '%s': %s", c.serverURL, err)
		return err
	}

	c.grpcConn = conn
	c.stargateClient = generated.NewStargateServiceClient(conn)
	return nil
}

func (c *Client) Run(ctx context.Context) error {
	c.logger.Info("starting client...")

	retryCount := 0
	for {
		err := c.runStream(ctx)
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			c.logger.Info("context cancelled, stopping client")
			return ctx.Err()
		}

		if !c.shouldRetry(err) {
			c.logger.Errorf("non-retryable error occurred: %v", err)
			return err
		}

		if retryCount >= c.config.MaxRetries {
			c.logger.WithFields(map[string]any{
				"max_retries": c.config.MaxRetries,
			}).Error("max retries reached, giving up")
			return fmt.Errorf("max retries (%d) reached: %w", c.config.MaxRetries, err)
		}

		retryCount++
		delay := c.calculateBackoff(retryCount)

		c.metricsRegistry.ConnectionRetries().Add(ctx, 1, metric.WithAttributes(
			attribute.Int("retry_count", retryCount),
			attribute.String("organization_id", c.config.OrganizationID),
			attribute.String("stack_id", c.config.StackID),
		))

		c.logger.WithFields(map[string]any{
			"retry_count": retryCount,
			"delay":       delay,
			"error":       err.Error(),
		}).Info("connection lost, retrying...")

		// Force reconnection on next attempt
		if c.grpcConn != nil {
			c.grpcConn.Close()
			c.grpcConn = nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (c *Client) runStream(ctx context.Context) error {
	// Create or recreate gRPC connection if needed
	if c.grpcConn == nil {
		if err := c.createGRPCConnection(); err != nil {
			return fmt.Errorf("failed to create gRPC connection: %w", err)
		}
	}

	ctx = metadata.AppendToOutgoingContext(
		ctx,
		"organization-id", c.config.OrganizationID,
		"stack-id", c.config.StackID,
	)

	c.logger.WithFields(map[string]any{
		"organization_id": c.config.OrganizationID,
		"stack_id":        c.config.StackID,
	}).Info("connecting to stargate server...")

	stream, err := c.stargateClient.Stargate(ctx)
	if err != nil {
		return err
	}

	c.logger.WithFields(map[string]any{
		"organization_id": c.config.OrganizationID,
		"stack_id":        c.config.StackID,
	}).Info("connected to stargate server")

	c.metricsRegistry.ConnectionStatus().Add(ctx, 1, metric.WithAttributes(
		attribute.String("organization_id", c.config.OrganizationID),
		attribute.String("stack_id", c.config.StackID),
	))

	defer func() {
		c.metricsRegistry.ConnectionStatus().Add(ctx, -1, metric.WithAttributes(
			attribute.String("organization_id", c.config.OrganizationID),
			attribute.String("stack_id", c.config.StackID),
		))
		c.logger.WithFields(map[string]any{
			"organization_id": c.config.OrganizationID,
			"stack_id":        c.config.StackID,
		}).Info("disconnected from stargate server")
	}()

	responseChan := make(chan *ResponseChanEvent, c.config.ChanSize)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		for {
			in, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					return nil
				}

				return err
			}

			c.logger.WithFields(map[string]any{
				"event": in,
			}).Debug("received message from server")

			c.workerPool.Submit(func() {
				out := c.Forward(ctx, in)
				select {
				case <-ctx.Done():
					return
				case responseChan <- out:
				}
			})
		}
	})

	eg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case response := <-responseChan:
				if response.err != nil {
					// Note: how should we handle errors here?
					return response.err
				}

				if response.msg == nil {
					continue
				}

				c.logger.WithFields(map[string]any{
					"response": response,
				}).Debug("sending response message to server")

				err := stream.Send(response.msg)
				if err != nil {
					return err
				}
			}
		}
	})

	return eg.Wait()
}

func (c *Client) shouldRetry(err error) bool {
	if err == nil {
		return false
	}

	st, ok := status.FromError(err)
	if !ok {
		return true
	}

	switch st.Code() {
	case codes.Canceled, codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied, codes.Unauthenticated:
		return false
	default:
		return true
	}
}

func (c *Client) calculateBackoff(retryCount int) time.Duration {
	delay := float64(c.config.InitialRetryDelay) * math.Pow(c.config.RetryMultiplier, float64(retryCount-1))
	if delay > float64(c.config.MaxRetryDelay) {
		delay = float64(c.config.MaxRetryDelay)
	}
	return time.Duration(delay)
}

func (c *Client) Forward(ctx context.Context, in *generated.StargateServerMessage) *ResponseChanEvent {
	attrs := []attribute.KeyValue{}

	switch ev := in.Event.(type) {
	case *generated.StargateServerMessage_ApiCall:

		ctx = opentelemetry.Propagator.Extract(ctx, propagation.MapCarrier(ev.ApiCall.OtlpContext))

		attrs = append(attrs, attribute.String("message_type", "api_call"))
		c.metricsRegistry.ServerMessageReceivedByType().Add(ctx, 1, metric.WithAttributes(attrs...))

		attrs = append(attrs, attribute.String("path", ev.ApiCall.Path))
		path := strings.TrimPrefix(ev.ApiCall.Path, "/")

		req, err := http.NewRequestWithContext(ctx, ev.ApiCall.Method, c.config.GatewayUrl+"/"+path, bytes.NewReader(ev.ApiCall.Body))
		if err != nil {
			return &ResponseChanEvent{
				err: err,
			}
		}

		opentelemetry.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

		q := req.URL.Query()
		for k, v := range ev.ApiCall.Query {
			for _, vv := range v.Values {
				q.Add(k, vv)
			}
		}
		req.URL.RawQuery = q.Encode()

		for k, v := range ev.ApiCall.Headers {
			for _, vv := range v.Values {
				req.Header.Add(k, vv)
			}
		}

		now := time.Now()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.logger.Errorf("error making http request: %v", err)
			return &ResponseChanEvent{
				err: nil,
				msg: &generated.StargateClientMessage{
					CorrelationId: in.CorrelationId,
					Event: &generated.StargateClientMessage_ApiCallResponse{ApiCallResponse: &generated.StargateClientMessage_APICallResponse{
						StatusCode: http.StatusInternalServerError,
						Body:       []byte{},
						Headers:    map[string]*generated.Values{},
					}},
				},
			}
		}
		latency := time.Since(now)
		c.metricsRegistry.HTTPCallLatencies().Record(ctx, latency.Milliseconds(), metric.WithAttributes(attrs...))

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return &ResponseChanEvent{
				err: err,
			}
		}

		headers := make(map[string]*generated.Values)
		for k, v := range resp.Header {
			headers[k] = &generated.Values{
				Values: v,
			}
		}

		attrs = append(attrs, attribute.Int("status_code", resp.StatusCode))
		c.metricsRegistry.HTTPCallStatusCodes().Add(ctx, 1, metric.WithAttributes(attrs...))

		return &ResponseChanEvent{
			err: nil,
			msg: &generated.StargateClientMessage{
				CorrelationId: in.CorrelationId,
				Event: &generated.StargateClientMessage_ApiCallResponse{ApiCallResponse: &generated.StargateClientMessage_APICallResponse{
					StatusCode: int32(resp.StatusCode),
					Body:       body,
					Headers:    headers,
				}},
			},
		}
	case *generated.StargateServerMessage_Ping_:
		return &ResponseChanEvent{
			err: nil,
			msg: &generated.StargateClientMessage{
				CorrelationId: in.CorrelationId,
				Event: &generated.StargateClientMessage_Pong_{
					Pong: &generated.StargateClientMessage_Pong{},
				},
			},
		}
	}

	return &ResponseChanEvent{
		err: nil,
		msg: nil,
	}
}

func (c *Client) Close() error {
	c.workerPool.StopAndWait()
	if c.grpcConn != nil {
		c.grpcConn.Close()
	}
	return nil
}
