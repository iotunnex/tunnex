# Tunnex control-plane API — multi-stage Go build.
# Build context is the repo root (see docker-compose.yml).

FROM golang:1.25-alpine AS build
WORKDIR /src

# Download deps first for layer caching. go.sum is created on first build.
COPY apps/api/go.mod apps/api/go.sum* ./
ENV GOFLAGS=-mod=mod
RUN go mod download

COPY apps/api/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tunnex-api ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 tunnex
# Pre-own the secrets mountpoint as uid 10001 so the named volume inherits
# 0700/uid-10001 on first init and the non-root process can write 0600 files.
RUN mkdir -p /var/lib/tunnex/secrets \
    && chown -R 10001:10001 /var/lib/tunnex \
    && chmod 700 /var/lib/tunnex/secrets
USER tunnex
COPY --from=build /out/tunnex-api /usr/local/bin/tunnex-api
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/tunnex-api"]
