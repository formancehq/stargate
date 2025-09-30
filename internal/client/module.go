package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-chi/chi/v5"

	runtimedebug "runtime/debug"

	"github.com/formancehq/go-libs/health"
	"github.com/formancehq/go-libs/httpserver"
	"github.com/formancehq/go-libs/logging"
	"github.com/formancehq/stack/ee/stargate/internal/client/controllers"
	"github.com/formancehq/stack/ee/stargate/internal/client/interceptors"
	"github.com/formancehq/stack/ee/stargate/internal/client/routes"
	metrics "github.com/formancehq/stack/ee/stargate/internal/grpcmetrics"
	"github.com/formancehq/stack/ee/stargate/internal/middlewares"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/fx"
)

func Module(
	bind string,
	serverURL string,
	tlsEnabled bool,
	tlsCACertificate string,
	tlsInsecureSkipVerify bool,
	debug bool,
) fx.Option {
	options := make([]fx.Option, 0)

	options = append(options,
		fx.Provide(routes.NewRouter),
		fx.Provide(controllers.NewStargateController),
		health.Module(),
		fx.Invoke(func(lc fx.Lifecycle, h chi.Router, l logging.Logger) {
			if debug {
				wrappedRouter := chi.NewRouter()
				wrappedRouter.Use(middlewares.Log())
				wrappedRouter.Mount("/", h)
				h = wrappedRouter
			}

			l.WithFields(map[string]any{
				"bind": bind,
			}).Info("HTTP server listening")
			lc.Append(httpserver.NewHook(h, httpserver.WithAddress(bind)))
		}),

		fx.Provide(interceptors.NewAuthInterceptor),
		fx.Provide(fx.Annotate(noop.NewMeterProvider, fx.As(new(metric.MeterProvider)))),
		fx.Provide(metrics.RegisterMetricsRegistry),
		fx.Provide(func(
			l logging.Logger,
			clientConfig Config,
			workerPoolConfig WorkerPoolConfig,
			metricsRegistry metrics.MetricsRegistry,
			authInterceptor *interceptors.AuthInterceptor,
		) *Client {
			return NewClient(
				l,
				clientConfig,
				workerPoolConfig,
				metricsRegistry,
				serverURL,
				tlsEnabled,
				tlsCACertificate,
				tlsInsecureSkipVerify,
				authInterceptor,
			)
		}),
		fx.Invoke(func(lc fx.Lifecycle, client *Client, authInterceptor *interceptors.AuthInterceptor, l logging.Logger, shutdowner fx.Shutdowner) {
			var runCtx context.Context
			var runCancel context.CancelFunc
			var clientDone chan error

			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					if err := authInterceptor.ScheduleRefreshToken(); err != nil {
						return err
					}

					runCtx, runCancel = context.WithCancel(context.Background())
					clientDone = make(chan error, 1)

					go func() {
						defer func() {
							if r := recover(); r != nil {
								runtimedebug.PrintStack()
								err, ok := r.(error)
								if ok {
									l.WithFields(map[string]any{
										"error": err.Error(),
									}).Error("recovering error")
									clientDone <- err
								} else {
									l.WithFields(map[string]any{
										"panic": r,
									}).Error("recovering panic")
									clientDone <- fmt.Errorf("panic: %v", r)
								}

								if err := shutdowner.Shutdown(); err != nil {
									l.WithFields(map[string]any{
										"error": err.Error(),
									}).Error("error during shutdown")
									panic(err)
								}
							}
						}()

						err := client.Run(runCtx)
						clientDone <- err
						if err != nil {
							if errors.Is(err, context.Canceled) {
								l.Info("client stopped gracefully")
							} else {
								l.WithFields(map[string]any{
									"error": err.Error(),
								}).Error("client stopped with error")
								if err := shutdowner.Shutdown(); err != nil {
									l.WithFields(map[string]any{
										"error": err.Error(),
									}).Error("error during shutdown")
									panic(err)
								}
							}
						}
					}()

					return nil
				},
				OnStop: func(ctx context.Context) error {
					l.Info("stopping stargate client...")

					// Cancel the client context
					runCancel()

					// Wait for client to finish with timeout
					select {
					case err := <-clientDone:
						if !errors.Is(err, context.Canceled) {
							l.WithFields(map[string]any{
								"error": err.Error(),
							}).Error("client error during shutdown")
						}
					case <-time.After(30 * time.Second):
						l.Error("timeout waiting for client to stop")
					}

					authInterceptor.Close()

					return client.Close()
				},
			})
		}),
	)

	return fx.Options(options...)
}
