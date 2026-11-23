# syntax=docker/dockerfile:1@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

WORKDIR /src

# Cache module downloads separately from source changes
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a fully static binary (required for distroless)
ARG VERSION=dev
ARG BRANCH=
ARG REVISION=
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION} -X main.Branch=${BRANCH} -X main.Revision=${REVISION}" \
      -o /out/db-auth-gateway \
      ./cmd/db-auth-gateway

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

COPY --from=builder /out/db-auth-gateway /usr/local/bin/db-auth-gateway

# 80: main HTTP proxy, 8001: instrumentation (/metrics, /debug/pprof), 8002: readiness, 9095: gRPC
EXPOSE 80 8001 8002 9095

ENTRYPOINT ["/usr/local/bin/db-auth-gateway"]
