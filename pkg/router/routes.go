// SPDX-License-Identifier: AGPL-3.0-only

package router

import (
	"net/http"
	"strings"

	"github.com/grafana/dskit/middleware"
)

// Route is used to define an API route to proxy requests.
type Route struct {
	Path            string
	Methods         []string
	Permission      string // one of auth.Scope* constants; empty = no auth required
	Middleware      middleware.Interface
	TimeoutOverride middleware.Interface
	PathPrefix      bool
	Websocket       bool
}

// RouteOption is used to configure a provided route.
type RouteOption interface {
	set(*Route)
}

// PathPrefix enables prefix based routing as opposed to explicit path routing.
func PathPrefix() RouteOption { return &pathPrefixOption{} }

type pathPrefixOption struct{}

func (p *pathPrefixOption) set(r *Route) {
	r.PathPrefix = true
}

// Websocket enables the websocket option for the specified route. This ensures
// that the proper config can be set when the route is registered with the router.
func Websocket() RouteOption { return &websocketOption{} }

type websocketOption struct{}

func (p *websocketOption) set(r *Route) {
	r.Websocket = true
}

// Middleware injects a custom middleware into the provided route. Only one middleware can
// be set per route. If multiple are required it is recommended to merge them into a single
// middleware.
func Middleware(m middleware.Interface) RouteOption { return &middlewareOption{middleware: m} }

type middlewareOption struct {
	middleware middleware.Interface
}

func (p *middlewareOption) set(r *Route) {
	if r.Middleware != nil {
		r.Middleware = middleware.Merge(r.Middleware, p.middleware)
	} else {
		r.Middleware = p.middleware
	}
}

// TrimPrefix will remove the prefix from the route matched before proxying the request.
func TrimPrefix(prefix string) RouteOption {
	return Middleware(
		middleware.Func(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
				next.ServeHTTP(w, req)
			})
		}),
	)
}

// ReplacePrefix replaces the prefix of the request path if it starts with original
func ReplacePrefix(original, replacement string) RouteOption {
	return Middleware(middleware.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reqPath := r.URL.Path; strings.HasPrefix(reqPath, original) {
				r.URL.Path = strings.Replace(reqPath, original, replacement, 1)
				r.URL.RawPath = strings.Replace(r.URL.RawPath, original, replacement, 1)
				r.RequestURI = r.URL.Path
			}
			next.ServeHTTP(w, r)
		})
	}))
}

// NewRoute creates a new route struct that will be registered with the proxy router.
// It allows for any number of custom configuration options to be provided as well.
func NewRoute(path string, methods []string, permission string, opts ...RouteOption) Route {
	r := Route{
		Path:            path,
		Methods:         methods,
		Permission:      permission,
		Middleware:      nil,
		TimeoutOverride: nil,
		PathPrefix:      false,
		Websocket:       false,
	}
	for _, opt := range opts {
		opt.set(&r)
	}
	return r
}
