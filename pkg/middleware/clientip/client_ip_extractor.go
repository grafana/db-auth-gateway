// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"net/http"
)

type ClientIPExtractor interface {
	GetClientIP(r *http.Request) (string, error)
}
