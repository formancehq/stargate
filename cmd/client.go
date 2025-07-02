package cmd

import (
	"github.com/formancehq/go-libs/licence"
	"github.com/formancehq/go-libs/otlp/otlpmetrics"
	"github.com/formancehq/go-libs/otlp/otlptraces"
	app "github.com/formancehq/go-libs/service"
	"github.com/formancehq/stack/ee/stargate/internal/client"
	"github.com/formancehq/stack/ee/stargate/internal/client/controllers"
	"github.com/formancehq/stack/ee/stargate/internal/client/interceptors"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

const (
	serviceName = "stargate"

	organizationIDFlag = "organization-id"
	stackIDFlag        = "stack-id"

	bindFlag = "bind"

	stargateServerURLFlag = "stargate-server-url"
	gatewayURLFlag        = "gateway-url"

	workerPoolMaxWorkersFlag = "worker-pool-max-worker"
	workerPoolMaxTasksFlag   = "worker-pool-max-tasks"

	ClientChanSizeFlag                = "client-chan-size"
	HTTPClientTimeoutFlag             = "http-client-timeout"
	HTTPClientMaxIdleConnsFlag        = "http-client-max-idle-conns"
	HTTPClientMaxIdleConnsPerHostFlag = "http-client-max-idle-conns-per-host"

	MaxRetriesFlag        = "max-retries"
	InitialRetryDelayFlag = "initial-retry-delay"
	MaxRetryDelayFlag     = "max-retry-delay"
	RetryMultiplierFlag   = "retry-multiplier"

	AuthRefreshTokenDurationBeforeExpireTimeFlag = "auth-refresh-token-duration-before-expire-time"
	StargateAuthClientIDFlag                     = "stargate-auth-client-id"
	StargateAuthClientSecretFlag                 = "stargate-auth-client-secret"
	StargateAuthIssuerURLFlag                    = "stargate-auth-issuer-url"
	TlsEnabledFlag                               = "tls-enabled"
	TlsInsecureSkipVerifyFlag                    = "tls-insecure-skip-verify"
	TlsCACertificateFlag                         = "tls-ca-cert"
)

func newClient() *cobra.Command {
	return &cobra.Command{
		Use:          "client",
		Short:        "Launch client",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.New(cmd.OutOrStdout(), resolveClientOptions(cmd)...).Run(cmd)
		},
	}
}

func resolveClientOptions(cmd *cobra.Command) []fx.Option {
	options := make([]fx.Option, 0)
	options = append(options, fx.NopLogger)

	workerPoolMaxTasks, _ := cmd.Flags().GetInt(workerPoolMaxTasksFlag)
	workerPoolMaxWorkers, _ := cmd.Flags().GetInt(workerPoolMaxWorkersFlag)
	organizationID, _ := cmd.Flags().GetString(organizationIDFlag)
	stackID, _ := cmd.Flags().GetString(stackIDFlag)
	clientChanSize, _ := cmd.Flags().GetInt(ClientChanSizeFlag)
	gatewayURL, _ := cmd.Flags().GetString(gatewayURLFlag)
	httpClientTimeout, _ := cmd.Flags().GetDuration(HTTPClientTimeoutFlag)
	httpClientMaxIdleConns, _ := cmd.Flags().GetInt(HTTPClientMaxIdleConnsFlag)
	httpClientMaxIdleConnsPerHost, _ := cmd.Flags().GetInt(HTTPClientMaxIdleConnsPerHostFlag)
	maxRetries, _ := cmd.Flags().GetInt(MaxRetriesFlag)
	initialRetryDelay, _ := cmd.Flags().GetDuration(InitialRetryDelayFlag)
	maxRetryDelay, _ := cmd.Flags().GetDuration(MaxRetryDelayFlag)
	retryMultiplier, _ := cmd.Flags().GetFloat64(RetryMultiplierFlag)
	stargateAuthIssuerURL, _ := cmd.Flags().GetString(StargateAuthIssuerURLFlag)
	authRefreshTokenDuration, _ := cmd.Flags().GetDuration(AuthRefreshTokenDurationBeforeExpireTimeFlag)
	stargateAuthClientID, _ := cmd.Flags().GetString(StargateAuthClientIDFlag)
	stargateAuthClientSecret, _ := cmd.Flags().GetString(StargateAuthClientSecretFlag)
	bind, _ := cmd.Flags().GetString(bindFlag)
	stargateServerURL, _ := cmd.Flags().GetString(stargateServerURLFlag)
	tlsEnabled, _ := cmd.Flags().GetBool(TlsEnabledFlag)
	tlsCaCert, _ := cmd.Flags().GetString(TlsCACertificateFlag)
	tlsInsecureSkipVerify, _ := cmd.Flags().GetBool(TlsInsecureSkipVerifyFlag)

	options = append(options,
		otlptraces.FXModuleFromFlags(cmd),
		otlpmetrics.FXModuleFromFlags(cmd),
		licence.FXModuleFromFlags(cmd, serviceName),
		fx.Provide(func() client.WorkerPoolConfig {
			return client.NewWorkerPoolConfig(
				workerPoolMaxWorkers,
				workerPoolMaxTasks,
			)
		}),
		fx.Provide(func() client.Config {
			config := client.NewClientConfig(
				organizationID,
				stackID,
				clientChanSize,
				gatewayURL,
				httpClientTimeout,
				httpClientMaxIdleConns,
				httpClientMaxIdleConnsPerHost,
			)
			if maxRetries > 0 {
				config.MaxRetries = maxRetries
			}
			if initialRetryDelay > 0 {
				config.InitialRetryDelay = initialRetryDelay
			}
			if maxRetryDelay > 0 {
				config.MaxRetryDelay = maxRetryDelay
			}
			if retryMultiplier > 0 {
				config.RetryMultiplier = retryMultiplier
			}
			return config
		}),

		fx.Provide(func() interceptors.Config {
			return interceptors.NewConfig(
				stargateAuthIssuerURL,
				authRefreshTokenDuration,
				stargateAuthClientID,
				stargateAuthClientSecret,
			)
		}),
		fx.Provide(func() controllers.StargateControllerConfig {
			return controllers.NewStargateControllerConfig(Version)
		}),
		client.Module(
			bind,
			stargateServerURL,
			tlsEnabled,
			tlsCaCert,
			tlsInsecureSkipVerify,
			app.IsDebug(cmd),
		),
	)

	return options
}
