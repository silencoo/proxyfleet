FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS webui
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY webui ./webui
RUN npm run check:webui && npm run build:webui

FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS builder
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY --from=webui /usr/local/bin/node /usr/local/bin/node
COPY go.mod go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN go env -w GOPROXY=${GOPROXY} && go mod download
COPY . .
COPY --from=webui /src/webui/dist ./webui/dist
RUN node scripts/build.mjs --skip-webui --target linux/${TARGETARCH} --version "${VERSION}" --commit "${COMMIT}"

FROM debian:bookworm-slim AS runtime
ARG TARGETARCH
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates gosu \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r -g 10001 easy \
    && useradd -r -u 10001 -g easy easy \
    && mkdir -p /etc/proxyfleet /app/logs \
    && chown -R easy:easy /etc/proxyfleet /app/logs
WORKDIR /app
COPY --from=builder /src/dist/proxyfleet-linux-${TARGETARCH} /usr/local/bin/proxyfleet
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
# Pool/Hybrid mode: 2323, Management: 9091, Multi-port/Hybrid mode: 24000-24200
EXPOSE 2323 9091 24000-24200
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["--config", "/etc/proxyfleet/config.yaml"]
