// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/grpcutil"
	"github.com/grafana/dskit/tracing"
	"github.com/grafana/dskit/user"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
)

type status string

const (
	StatusClientClosedRequest = 499

	// StatusNetworkReadTimeout is an unofficial HTTP status code we use when the proxy hit the timeout
	// while reading the client's HTTP request body. The reason why we use a custom code is because this
	// way we can track it differently in metrics, eventually excluding it from SLO budget burn.
	StatusNetworkReadTimeout = 598

	statusError status = "error"
)

type errorType string

const (
	errorTimeout     errorType = "timeout"
	errorCanceled    errorType = "canceled"
	errorUnavailable errorType = "unavailable"
)

type ErrorResponse struct {
	Status    status    `json:"status"`
	ErrorType errorType `json:"errorType,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// NewErrorHandler returns a function to be used as a ReverseProxy error handler
func NewErrorHandler(logger log.Logger, pm *Metrics) func(w http.ResponseWriter, r *http.Request, err error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		errorHandler(w, r, err, logger, pm)
	}
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error, logger log.Logger, pm *Metrics) {
	response := ErrorResponse{
		Status: statusError,
		Error:  err.Error(),
	}
	ctx := r.Context()
	var statusCode int
	var maxBytesErr *http.MaxBytesError

	switch {
	case isContextCanceledError(ctx, err) || errors.Is(err, io.ErrUnexpectedEOF):
		statusCode = StatusClientClosedRequest
		response.ErrorType = errorCanceled
	case errors.Is(err, context.DeadlineExceeded), isGRPCTimeout(err):
		statusCode = http.StatusGatewayTimeout
		response.ErrorType = errorTimeout
	case isNetworkTimeout(err):

		// If the error is net.OpError then the underlying system call could
		// tell us if the timeout occurred while reading or writing.
		if opError, ok := err.(*net.OpError); ok {
			if opError.Op == "read" {
				statusCode = StatusNetworkReadTimeout
				response.ErrorType = errorTimeout
			} else {
				statusCode = http.StatusGatewayTimeout
				response.ErrorType = errorTimeout
			}
			break
		}

		if r.Body != nil {
			// Try to read 1 byte from the request body. If it fails with the same error
			// it means the timeout occurred while reading the request body, return a 598.
			if _, readErr := r.Body.Read([]byte{0}); isNetworkTimeout(readErr) {
				statusCode = StatusNetworkReadTimeout
				response.ErrorType = errorTimeout
				break
			}
		}
		statusCode = http.StatusGatewayTimeout
		response.ErrorType = errorTimeout
	case errors.Is(err, errHTTPRequestReadTimeout):
		statusCode = StatusNetworkReadTimeout
		response.ErrorType = errorTimeout
	case errors.As(err, &maxBytesErr):
		statusCode = http.StatusRequestEntityTooLarge
		response.ErrorType = errorCanceled
		msg := createErrorMessageFromRequest(r)
		msg = append(msg, "msg", "Request failed due to size", "url", r.URL.String(), "method", r.Method, "status", statusCode, "err", err)
		level.Warn(logger).Log(msg...)
	default:
		statusCode = http.StatusBadGateway
		response.ErrorType = errorUnavailable
		msg := createErrorMessageFromRequest(r)
		msg = append(msg, "msg", "Request failed", "url", r.URL.String(), "method", r.Method, "status", statusCode, "err", err)
		level.Warn(logger).Log(msg...)
	}

	reason := errorToReason(ctx, err)
	pm.RequestsErrors.WithLabelValues(reason).Inc()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		level.Warn(logger).Log("msg", "failed to encode error response", "err", err)
	}
}

func errorToReason(ctx context.Context, err error) string {
	var maxBytesErr *http.MaxBytesError
	switch {
	case isContextCanceledError(ctx, err) || errors.Is(err, io.ErrUnexpectedEOF):
		return CanceledLabel
	case errors.Is(err, context.DeadlineExceeded), isGRPCTimeout(err):
		return NetworkTimedOutLabel
	case isNetworkTimeout(err):
		return NetworkTimedOutLabel
	case errors.Is(err, errHTTPRequestReadTimeout):
		return NetworkTimedOutLabel
	case errors.As(err, &maxBytesErr):
		return TooLargeLabel
	default:
		return OtherLabel
	}
}

func createErrorMessageFromRequest(r *http.Request) []any {
	msg := make([]interface{}, 0, 10)
	if tenant, err := user.ExtractOrgID(r.Context()); err == nil && tenant != "" {
		msg = append(msg, "org", tenant)
	} else if tenant, _, err := user.ExtractOrgIDFromHTTPRequest(r); err == nil && tenant != "" {
		msg = append(msg, "org", tenant)
	}
	ok, id := extractAuthorization(r)
	if ok {
		msg = append(msg, "user", id)
	}
	if traceID, traceIDValid := tracing.ExtractSampledTraceID(r.Context()); traceIDValid {
		msg = append(msg, "traceID", traceID)
	}
	return msg
}

func isNetworkTimeout(err error) bool {
	if err == nil {
		return false
	}

	netErr, ok := errors.Cause(err).(net.Error)
	return ok && netErr.Timeout()
}

func isGRPCTimeout(err error) bool {
	return grpcutil.ErrorToStatusCode(errors.Cause(err)) == codes.DeadlineExceeded
}

func isContextCanceledError(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx.Err() == nil {
		return false
	}
	return grpcutil.ErrorToStatusCode(errors.Cause(err)) == codes.Canceled
}

func extractAuthorization(r *http.Request) (ok bool, id string) {
	authHeader := r.Header.Get("Authorization")
	if len(authHeader) == 0 {
		return false, ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	switch parts[0] {
	case "Basic":
		id, _, ok = r.BasicAuth()
	case "Bearer":
		if len(parts) >= 2 {
			keyParts := strings.SplitN(parts[1], ":", 2)
			if len(keyParts) >= 2 {
				id = keyParts[0]
				ok = true
			}
		}
	}
	return ok, id
}
