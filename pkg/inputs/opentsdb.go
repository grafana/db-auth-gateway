// SPDX-License-Identifier: AGPL-3.0-only

package inputs

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/prometheus/prompb"
)

// OpenTSDB write requests contain either seconds or milliseconds
const maxSecondTimestamp = 1000000000000

var (
	errNonNumericValue = errors.New("can not convert metric with non-numberic value")
)

// OpenTSDBHandler receives opentsdb http write requests, transforms the data
// to the prometheus proto format and writes and proxies the request forward to
// a write proxy.
// TODO: Update the handler to return errors in the opentsdb response format
// http://opentsdb.net/docs/build/html/api_http/put.html
type OpenTSDBHandler struct {
	WriteProxy http.Handler
	Logger     log.Logger
}

func (o *OpenTSDBHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var (
		reader io.Reader
		err    error
	)
	if r.Header.Get("Content-Encoding") == "gzip" {
		reader, err = gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "unable to decompress payload", http.StatusBadRequest)
			level.Error(o.Logger).Log("msg", "unable to decompress payload", "err", err)
			return
		}
	} else {
		reader = r.Body
	}
	defer func() { _ = r.Body.Close() }()

	payload, err := io.ReadAll(reader)
	if err != nil {
		http.Error(w, "bad data payload", http.StatusBadRequest)
		level.Error(o.Logger).Log("msg", "unable to read payload", "err", err)
		return
	}

	var req openTSDBPutRequest
	err = json.Unmarshal(payload, &req)
	if err != nil {
		http.Error(w, "bad data payload", http.StatusBadRequest)
		level.Error(o.Logger).Log("msg", "unable to unmarshal json request", "err", err)
		return
	}

	series := make([]prompb.TimeSeries, 0, len(req))
	for _, ts := range req {
		m, err := ts.generatePromMetric()
		if err != nil {
			level.Warn(o.Logger).Log("msg", "unable to generate prometheus metrics from opentsdb metric", "err", err)
			continue
		}
		series = append(series, m)
	}

	data, err := buildWriteRequest(series)
	if err != nil {
		http.Error(w, "bad data payload", http.StatusBadRequest)
		level.Error(o.Logger).Log("msg", "unable to format write request in prompb format", "err", err)
		return
	}

	updateRequest(data, r)
	o.WriteProxy.ServeHTTP(w, r)
}

type openTSDBMetric struct {
	Metric    string            `json:"metric"`
	Timestamp int64             `json:"timestamp"`
	Value     interface{}       `json:"value"`
	Tags      map[string]string `json:"tags"`
}

type openTSDBPutRequest []openTSDBMetric

func (m openTSDBMetric) generatePromMetric() (prompb.TimeSeries, error) {
	var (
		value     float64
		timestamp int64
		labels    []prompb.Label
	)

	switch m.Value.(type) {
	case float64:
		value = m.Value.(float64)
	default:
		return prompb.TimeSeries{}, errNonNumericValue
	}

	if m.Timestamp > maxSecondTimestamp {
		timestamp = m.Timestamp
	} else {
		timestamp = m.Timestamp * 1000
	}

	// Generate a labels array with the number of tags plus the name of
	// the metric
	labels = make([]prompb.Label, 0, len(m.Tags)+1)
	for name, value := range m.Tags {
		labels = append(labels, prompb.Label{
			Name:  name,
			Value: value,
		})
	}

	labels = append(labels, prompb.Label{
		Name:  "__name__",
		Value: mungeSeriesName(m.Metric),
	})

	return prompb.TimeSeries{
		Labels: labels,
		Samples: []prompb.Sample{
			{
				Value:     value,
				Timestamp: timestamp,
			},
		},
	}, nil
}
