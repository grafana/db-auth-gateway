// SPDX-License-Identifier: AGPL-3.0-only

package inputs

import (
	"bytes"
	"io"
	"net/http"
	"regexp"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

var (
	invalidMetricChars = regexp.MustCompile("[^a-zA-Z0-9_:]")
)

func mungeSeriesName(name string) string {
	name = invalidMetricChars.ReplaceAllString(name, "_")
	return name
}

func buildWriteRequest(samples []prompb.TimeSeries) ([]byte, error) {
	req := &prompb.WriteRequest{
		Timeseries: samples,
	}

	data, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}

	compressed := snappy.Encode(nil, data)
	return compressed, nil
}

func updateRequest(payload []byte, r *http.Request) {
	r.URL.Path = "/api/prom/push"
	r.Body = io.NopCloser(bytes.NewReader(payload))
	r.RequestURI = r.URL.String()
	r.Header.Set("Content-Encoding", "snappy")
	r.Header.Set("Content-Type", "application/x-protobuf")
	r.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
}
