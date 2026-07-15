# Tunnex control-plane API — multi-stage Go build.
# Build context is the repo root (see docker-compose.yml).

FROM golang:1.25.11-alpine AS build
WORKDIR /src

# Download deps first for layer caching. go.sum is created on first build.
COPY apps/api/go.mod apps/api/go.sum* ./
ENV GOFLAGS=-mod=readonly
RUN go mod download

COPY apps/api/ ./
# Edition selector (open-core). Empty (default) = OPEN build — the enterprise policy
# engine is //go:build enterprise-tagged and is NOT linked, so the default image can
# never contain it (the CI edition-isolation guard asserts this on the open build).
# Set TUNNEX_BUILD_TAGS=enterprise to build the ENTERPRISE image reproducibly from
# committed config (Zero Trust policy/enforcement/device-approval) — used for local +
# self-hosted enterprise testing. This replaces the old temporary `sed` hack; the same
# tag `make build-editions`/`test-editions` already compile-check now plumbs into the image.
ARG TUNNEX_BUILD_TAGS=""
RUN CGO_ENABLED=0 GOOS=linux go build -tags "$TUNNEX_BUILD_TAGS" -trimpath -ldflags="-s -w" -o /out/tunnex-api ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 tunnex
# Pre-own the secrets AND flowlog mountpoints as uid 10001 so each named volume inherits
# uid-10001 on first init and the non-root process can write. A fresh Docker volume mounts
# ROOT-owned unless its mountpoint pre-exists in the image with the right owner — without
# pre-creating flowlog here, the tunnex_flowlog volume mounts root-owned and the 10001 api
# cannot create the JSONL source-of-truth (flowlog_writer_failed, PG-only, no SIEM stream).
RUN mkdir -p /var/lib/tunnex/secrets /var/lib/tunnex/flowlog \
    && chown -R 10001:10001 /var/lib/tunnex \
    && chmod 700 /var/lib/tunnex/secrets
USER tunnex
COPY --from=build /out/tunnex-api /usr/local/bin/tunnex-api
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/tunnex-api"]
