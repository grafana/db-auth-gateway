# syntax=docker/dockerfile:1@sha256:2780b5c3bab67f1f76c781860de469442999ed1a0d7992a5efdf2cffc0e3d769

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

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
FROM gcr.io/distroless/static:nonroot@sha256:e3f945647ffb95b5839c07038d64f9811adf17308b9121d8a2b87b6a22a80a39

COPY --from=builder /out/db-auth-gateway /usr/local/bin/db-auth-gateway

# 80: main HTTP proxy, 8001: instrumentation (/metrics, /debug/pprof), 8002: readiness, 9095: gRPC
EXPOSE 80 8001 8002 9095

ENTRYPOINT ["/usr/local/bin/db-auth-gateway"]
