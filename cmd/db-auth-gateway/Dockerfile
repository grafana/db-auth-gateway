# syntax=docker/dockerfile:1@sha256:2780b5c3bab67f1f76c781860de469442999ed1a0d7992a5efdf2cffc0e3d769

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder

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
FROM gcr.io/distroless/static:nonroot@sha256:d62067879de890a1def9da0e7766b7d20810a5e5555cef8506b752437d7a3e88

COPY --from=builder /out/db-auth-gateway /usr/local/bin/db-auth-gateway

# 80: main HTTP proxy, 8001: instrumentation (/metrics, /debug/pprof), 8002: readiness, 9095: gRPC
EXPOSE 80 8001 8002 9095

ENTRYPOINT ["/usr/local/bin/db-auth-gateway"]
