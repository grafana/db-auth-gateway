# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

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
FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99

COPY --from=builder /out/db-auth-gateway /usr/local/bin/db-auth-gateway

# 80: main HTTP proxy, 8001: instrumentation (/metrics, /debug/pprof), 8002: readiness, 9095: gRPC
EXPOSE 80 8001 8002 9095

ENTRYPOINT ["/usr/local/bin/db-auth-gateway"]
