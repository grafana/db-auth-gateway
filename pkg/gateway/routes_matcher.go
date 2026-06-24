// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/grafana/db-auth-gateway/pkg/router"
)

var (
	routeParametersRegexp = regexp.MustCompile(`(\{[^{}]+\})`)
)

type routesMatcher struct {
	staticPaths        map[string]struct{}
	parameterizedPaths []*regexp.Regexp
}

func newRoutesMatcher() *routesMatcher {
	return &routesMatcher{
		staticPaths: map[string]struct{}{},
	}
}

func (m *routesMatcher) addRoutes(routes []router.Route) {
	for _, route := range routes {
		m.addRoute(route)
	}
}

func (m *routesMatcher) addRoute(route router.Route) {
	if !strings.Contains(route.Path, "{") {
		m.staticPaths[route.Path] = struct{}{}
		return
	}

	// The route is parameterized. We convert the route in a regular expression.
	const placeholder = "XXXXXXXXXX"
	pattern := routeParametersRegexp.ReplaceAllString(route.Path, placeholder)
	pattern = fmt.Sprintf("^%s$", regexp.QuoteMeta(pattern))
	pattern = strings.ReplaceAll(pattern, placeholder, "[^/]+")

	m.parameterizedPaths = append(m.parameterizedPaths, regexp.MustCompile(pattern))
}

func (m *routesMatcher) matches(path string) bool {
	if _, ok := m.staticPaths[path]; ok {
		return true
	}

	for _, matcher := range m.parameterizedPaths {
		if matcher.MatchString(path) {
			return true
		}
	}

	return false
}
