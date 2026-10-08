FROM ghcr.io/formancehq/base:scratch
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/stargate /usr/bin/stargate
ENV OTEL_SERVICE_NAME stargate
ENTRYPOINT ["/usr/bin/stargate"]
CMD ["client"]
