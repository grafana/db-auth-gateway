# Forward Auth Contract

## Overview

When `--auth.type=forward_auth` is set, the proxy delegates every inbound
request to an external HTTP auth service of your choice. On each request the
proxy POSTs a JSON sub-request to `--forward-auth.url`, inspects the response,
and either allows or rejects the original request. The auth service is
responsible for validating credentials and returning the tenant IDs and
(optionally) label policies that the proxy should enforce.

Relevant flags:

| Flag | Default | Description |
|---|---|---|
| `--forward-auth.url` | _(required)_ | URL the proxy POSTs to for every request |
| `--forward-auth.timeout` | `5s` | Per-request timeout for calls to the auth service |
| `--forward-auth.cache-ttl` | `0` (disabled) | How long to cache a successful auth result |

---

## Quick Start

**Minimal compliant exchange:**

```bash
# What the proxy sends (abbreviated):
curl -s -X POST https://my-auth-service/auth \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer eyJ..." \
  -H "X-Forwarded-For: 203.0.113.5" \
  -d '{"path":"/loki/api/v1/push","method":"POST","requiredScope":"logs:write"}'

# Minimal compliant response:
# HTTP/1.1 200 OK
# X-Scope-OrgID: tenant-a
```

---

## Request Specification

The proxy sends a `POST` request to `--forward-auth.url`.

### Method and Content-Type

```
POST <forward-auth.url>
Content-Type: application/json
```

### Forwarded Headers

All headers from the original inbound request are copied to the sub-request
**before** `Content-Type` and `X-Forwarded-For` are set, so those two values
always reflect the proxy's view of the request.

| Header | Value |
|---|---|
| `Authorization` | Copied verbatim from the original request |
| `X-Forwarded-For` | Client IP address (port stripped) |
| _(all others)_ | Copied verbatim from the original request |

### JSON Body

```json
{
  "path": "/loki/api/v1/push",
  "method": "POST",
  "requiredScope": "logs:write"
}
```

| Field | Type | Description |
|---|---|---|
| `path` | string | URL path of the original request |
| `method` | string | HTTP method of the original request (`GET`, `POST`, etc.) |
| `requiredScope` | string | The scope the proxy requires for this endpoint (see [Scope Reference](#scope-reference)) |

---

## Response Specification

### Status Codes

| Status | Proxy behaviour |
|---|---|
| `200 OK` | Request is allowed. Proxy reads tenant IDs and label policies from response headers. |
| `401 Unauthorized` | Proxy returns `401` to the client. |
| `403 Forbidden` | Proxy returns `403` to the client. |
| Any other | Proxy returns `502 Bad Gateway` to the client. |

### Response Headers (on 200)

| Header | Format | Required | Description |
|---|---|---|---|
| `X-Scope-OrgID` | Pipe-separated string | Yes | One or more tenant IDs to route the request under |
| `X-Prom-Label-Policy` | `tenantID:<percent-encoded selector>` — repeated once per policy | No | Label selector policies to enforce per tenant |

The response body is ignored.

### Format Rules

- **`X-Scope-OrgID`** must be a single header with pipe-separated tenant IDs
  (`X-Scope-OrgID: t1|t2`). This matches the [dskit](https://github.com/grafana/dskit)
  / backend-enterprise convention. Whitespace around pipes is trimmed. Empty
  values (e.g. from stray pipes) are rejected and cause the proxy to return
  `502 Bad Gateway`.
- **`X-Prom-Label-Policy`** may appear as a repeated header or as a single
  header with comma-separated entries (both forms are equivalent, matching
  backend-enterprise convention). Each entry is parsed at the **first colon**:
  everything before is the tenant ID, everything after is the label selector
  **percent-encoded per RFC 3986** (consistent with `url.PathEscape` in Go).
  For example, `tenant-a:{env="prod"}` is encoded as
  `tenant-a:%7Benv%3D%22prod%22%7D`. Malformed entries — including empty
  entries from stray commas, entries with no colon, an empty tenant ID, an
  invalid percent-encoding, or an invalid PromQL selector — are rejected and
  cause the proxy to return `502 Bad Gateway`.

---

## Scope Reference

The `requiredScope` field in the request body will always be one of the
following values:

### Metrics

| Scope | Typical endpoints |
|---|---|
| `metrics:read` | Prometheus query / query-range / series / labels |
| `metrics:write` | Remote-write ingestion |
| `metrics:export` | Native histogram / exemplar export paths |

### Logs

| Scope | Typical endpoints |
|---|---|
| `logs:read` | Loki query / query-range / tail |
| `logs:write` | Loki push |
| `logs:delete` | Loki delete |

### Traces

| Scope | Typical endpoints |
|---|---|
| `traces:read` | Tempo query |
| `traces:write` | OTLP/Jaeger/Zipkin ingestion |

### Profiles

| Scope | Typical endpoints |
|---|---|
| `profiles:read` | Pyroscope query |
| `profiles:write` | Pyroscope push |

### Rules and Alerts

| Scope | Typical endpoints |
|---|---|
| `rules:read` | Ruler GET |
| `rules:write` | Ruler PUT / POST / DELETE |
| `alerts:read` | Alertmanager GET |
| `alerts:write` | Alertmanager PUT / POST / DELETE |

---

## Label Policy Format

`X-Prom-Label-Policy` lets the auth service attach a PromQL-style label
selector to a tenant, restricting which series/streams that tenant can read or
write. The selector portion must be percent-encoded per RFC 3986 (equivalent to
Go's `url.PathEscape`).

**Single tenant, single policy:**
```
X-Prom-Label-Policy: tenant-a:%7Benv%3D%22prod%22%7D
```
(decoded selector: `{env="prod"}`)

**Multi-tenant with per-tenant policies:**
```
X-Scope-OrgID: tenant-a|tenant-b
X-Prom-Label-Policy: tenant-a:%7Benv%3D%22prod%22%7D
X-Prom-Label-Policy: tenant-b:%7Benv%3D%22staging%22%2Cteam%3D%22ops%22%7D
```

**Multi-value selector:**
```
X-Prom-Label-Policy: tenant-a:%7Benv%3D~%22prod%7Cstaging%22%7D
```
(decoded selector: `{env=~"prod|staging"}`)

Rules:
- The tenant ID in `X-Prom-Label-Policy` must match one of the IDs returned in
  `X-Scope-OrgID`. A policy for an unknown tenant is rejected and causes the
  proxy to return `502 Bad Gateway`.
- Multiple policies for the same tenant are not deduplicated; behaviour depends
  on how the downstream signal backend interprets them.
- If no `X-Prom-Label-Policy` is returned for a tenant, no label enforcement is
  applied for that tenant.

---

## Caching Behaviour

When `--forward-auth.cache-ttl` is set to a positive duration, the proxy caches
successful (`200`) auth results.

**Cache key:** `Authorization header value` + `|` + `requiredScope`

Implications for auth service design:

- Results are cached **per-credential per-scope**. A user with a token that has
  multiple scopes will generate a separate cache entry for each scope.
- The cache is only populated on `200` responses. `401` and `403` responses are
  **not** cached — every rejected request hits the auth service.
- If a credential is revoked, cached grants remain valid until the TTL expires.
  Set `--forward-auth.cache-ttl` accordingly or leave it at `0` to disable
  caching for security-sensitive environments.
- The cache is in-process only (no external store). It is lost on proxy restart.
- The cleanup interval is `2 × cache-ttl`.

---

## Example Implementations

These are minimal handler examples — not full servers.

### Go

```go
package main

import (
    "encoding/json"
    "net/http"
)

type authRequest struct {
    Path          string `json:"path"`
    Method        string `json:"method"`
    RequiredScope string `json:"requiredScope"`
}

func authHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    var req authRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "bad request", http.StatusBadRequest)
        return
    }

    token := r.Header.Get("Authorization")
    tenantID, ok := validateToken(token, req.RequiredScope)
    if !ok {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }

    w.Header().Set("X-Scope-OrgID", tenantID)
    // Optionally restrict to prod data only:
    // w.Header().Add("X-Prom-Label-Policy", tenantID+":%7Benv%3D%22prod%22%7D")
    w.WriteHeader(http.StatusOK)
}

// validateToken is a placeholder — implement your own credential logic.
func validateToken(token, scope string) (tenantID string, ok bool) {
    // ...
    return "", false
}
```

### Python

```python
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

class AuthHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length))

        token = self.headers.get("Authorization", "")
        tenant_id, ok = validate_token(token, body.get("requiredScope", ""))

        if not ok:
            self.send_response(401)
            self.end_headers()
            return

        self.send_response(200)
        self.send_header("X-Scope-OrgID", tenant_id)
        # Optionally add label policy (selector must be percent-encoded):
        # self.send_header("X-Prom-Label-Policy", f"{tenant_id}:%7Benv%3D%22prod%22%7D")
        self.end_headers()

def validate_token(token: str, scope: str) -> tuple[str, bool]:
    # Implement your own credential logic here.
    return "", False

if __name__ == "__main__":
    HTTPServer(("", 9000), AuthHandler).serve_forever()
```

---

## OpenAPI 3.0 Spec

The following is a machine-readable spec for the single endpoint the proxy
calls. You can paste it into [Swagger Editor](https://editor.swagger.io/) or
import it into Postman to generate a server stub or test collection.

```yaml
openapi: "3.0.3"
info:
  title: db-auth-gateway Forward Auth Service
  version: "1.0"
  description: >
    Contract that a forward-auth service must implement when used with
    db-auth-gateway (--auth.type=forward_auth).

paths:
  /auth:
    post:
      summary: Authenticate and authorise a proxy request
      description: >
        Called by db-auth-gateway for every inbound request.
        All original request headers (including Authorization) are forwarded.
        The X-Forwarded-For header is set to the client IP (port stripped).
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/AuthRequest"
            example:
              path: /loki/api/v1/push
              method: POST
              requiredScope: logs:write
      responses:
        "200":
          description: Request is allowed.
          headers:
            X-Scope-OrgID:
              required: true
              description: >
                One or more tenant IDs, pipe-separated in a single header
                (dskit/backend-enterprise convention).
              schema:
                type: string
              example: tenant-a|tenant-b
            X-Prom-Label-Policy:
              required: false
              description: >
                Label selector policy per tenant. May be repeated or
                comma-separated within a single header (both forms equivalent).
                Format per entry: tenantID:<percent-encoded selector> where the
                selector is encoded per RFC 3986 (url.PathEscape in Go).
                Example decoded: tenant-a:{env="prod"}
              schema:
                type: string
              example: 'tenant-a:%7Benv%3D%22prod%22%7D,tenant-b:%7Benv%3D%22staging%22%7D'
        "401":
          description: Proxy returns 401 to the client.
        "403":
          description: Proxy returns 403 to the client.
        default:
          description: Any other status causes the proxy to return 502 to the client.

components:
  schemas:
    AuthRequest:
      type: object
      required:
        - path
        - method
        - requiredScope
      properties:
        path:
          type: string
          description: URL path of the original request.
          example: /loki/api/v1/push
        method:
          type: string
          description: HTTP method of the original request.
          example: POST
        requiredScope:
          type: string
          description: >
            Scope required for this endpoint. One of:
            metrics:read, metrics:write, metrics:export,
            logs:read, logs:write, logs:delete,
            traces:read, traces:write,
            profiles:read, profiles:write,
            rules:read, rules:write,
            alerts:read, alerts:write.
          example: logs:write
```
