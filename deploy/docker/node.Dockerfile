# tunnex-node data-plane agent — multi-stage Go build.
# Runs with NET_ADMIN in compose so it can manage WireGuard interfaces (S3.x).

FROM golang:1.25.11-alpine AS build
WORKDIR /src
COPY apps/node/go.mod apps/node/go.sum* ./
ENV GOFLAGS=-mod=readonly
RUN go mod download
COPY apps/node/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tunnex-node ./cmd/agent

FROM alpine:3.20
# WireGuard data plane (S3.2): wg/wg-quick + ip (iproute2) drive the kernel
# WireGuard module (present in most modern kernels incl. Docker's LinuxKit VM).
# ca-certificates for the control channel. If a host lacks the module the agent
# fails readiness with a diagnosable error rather than pretending success.
RUN apk add --no-cache ca-certificates wireguard-tools iproute2
COPY --from=build /out/tunnex-node /usr/local/bin/tunnex-node
EXPOSE 9091
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD wget -qO- http://127.0.0.1:9091/healthz >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/usr/local/bin/tunnex-node"]
