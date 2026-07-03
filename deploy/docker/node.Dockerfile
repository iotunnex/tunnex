# tunnex-node data-plane agent — multi-stage Go build.
# Runs with NET_ADMIN in compose so it can manage WireGuard interfaces (S3.x).

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY apps/node/go.mod apps/node/go.sum* ./
ENV GOFLAGS=-mod=mod
RUN go mod download
COPY apps/node/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tunnex-node ./cmd/agent

FROM alpine:3.20
# wireguard-tools land here in S3.x; kept minimal for the foundation stub.
RUN apk add --no-cache ca-certificates
COPY --from=build /out/tunnex-node /usr/local/bin/tunnex-node
EXPOSE 9091
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD wget -qO- http://127.0.0.1:9091/healthz >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/usr/local/bin/tunnex-node"]
