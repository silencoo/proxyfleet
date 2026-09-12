FROM --platform=$BUILDPLATFORM golang:1.24 AS builder
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN go env -w GOPROXY=${GOPROXY} && go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o proxyfleet ./cmd/proxyfleet

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates gosu \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r -g 10001 easy \
    && useradd -r -u 10001 -g easy easy \
    && mkdir -p /etc/proxyfleet /app/logs \
    && chown -R easy:easy /etc/proxyfleet /app/logs
WORKDIR /app
COPY --from=builder /src/proxyfleet /usr/local/bin/proxyfleet
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
# Pool/Hybrid mode: 2323, Management: 9091, Multi-port/Hybrid mode: 24000-24200
EXPOSE 2323 9091 24000-24200
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["--config", "/etc/proxyfleet/config.yaml"]
