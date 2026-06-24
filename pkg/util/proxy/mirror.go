// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"go.uber.org/atomic"
	"golang.org/x/sync/errgroup"
)

type MirrorTarget struct {
	URL      string  `yaml:"url" category:"advanced" doc:"description=Configures the URL the requests are mirrored to."`
	Fraction float64 `yaml:"fraction,omitempty" category:"advanced" doc:"description=Fraction of the requests that should be mirrored. 0 nothing is mirrored, 1 everything is mirrored|default=1.0"`
}

type MirrorTargets []MirrorTarget

func (m *MirrorTargets) Set(value string) error {
	if len(value) == 0 {
		*m = nil // no targets
		return nil
	}

	parts := strings.Split(value, ",")

	targets := make([]MirrorTarget, len(parts))
	for idx, part := range parts {
		u, err := url.ParseRequestURI(part) // ensure the URL is valid
		if err != nil {
			return fmt.Errorf("invalid mirror target: %w", err)
		}
		q := u.Query()
		u.RawQuery = "" // Clear the query part to avoid confusion

		targets[idx].URL = u.String()
		targets[idx].Fraction = 1.0 // Default to 1.0 if no fraction is provided
		for k, v := range q {
			switch k {
			case "fraction":
				fraction, err := strconv.ParseFloat(v[0], 64)
				if err != nil {
					return fmt.Errorf("invalid mirror target fraction: %s: %w", part, err)
				}
				if fraction < 0 || fraction > 1 {
					return fmt.Errorf("invalid mirror target fraction %f: needs to be between [0, 1]", fraction)
				}
				targets[idx].Fraction = fraction
			default:
				return fmt.Errorf("invalid mirror target parameter: %s", k)
			}
		}
	}

	*m = targets

	return nil
}

func (m MirrorTargets) String() string {
	parts := make([]string, len(m))

	for idx, target := range m {
		u, _ := url.Parse(target.URL)
		q := make(url.Values)
		q.Add("fraction", strconv.FormatFloat(target.Fraction, 'f', -1, 64))
		u.RawQuery = q.Encode()
		parts[idx] = u.String()
	}
	return strings.Join(parts, ",")
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (m *MirrorTargets) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var t []struct {
		URL      string   `yaml:"url"`
		Fraction *float64 `yaml:"fraction,omitempty"`
	}
	if err := unmarshal(&t); err != nil {
		return err
	}

	parts := make([]string, len(t))
	for idx, target := range t {
		u, err := url.ParseRequestURI(target.URL)
		if err != nil {
			return fmt.Errorf("invalid mirror target: %s: %w", target.URL, err)
		}
		fraction := 1.0
		if target.Fraction != nil {
			fraction = *target.Fraction
		}
		q := make(url.Values)
		q.Add("fraction", strconv.FormatFloat(fraction, 'f', -1, 64))
		u.RawQuery = q.Encode()
		parts[idx] = u.String()
	}
	return m.Set(strings.Join(parts, ","))
}

// MarshalYAML implements yaml.Marshaler.
func (m MirrorTargets) MarshalYAML() (interface{}, error) {
	t := make([]struct {
		URL      string  `yaml:"url"`
		Fraction float64 `yaml:"fraction,omitempty"`
	}, len(m))
	for idx := range m {
		t[idx].URL = m[idx].URL
		t[idx].Fraction = m[idx].Fraction
	}
	return &t, nil
}

type mirrorRoundTripper struct {
	logger log.Logger
	cfg    []MirrorTarget
	next   http.RoundTripper
	pool   httputil.BufferPool

	ctx     context.Context
	cancel  context.CancelFunc
	proxies []*httputil.ReverseProxy

	g *errgroup.Group

	maxInflight int
	timeout     time.Duration
}

// newMirrorRoundTripper creates a new http.RoundTripper which mirror http.Requests to multiple target.
// The result of the mirrored request will not be waited for or processed in any way.
//
// In order to not block the main request, the request body is read into a buffer and then
// the buffer is read by the mirrored requests.
func newMirrorRoundTripper(logger log.Logger, cfg []MirrorTarget, next http.RoundTripper, pool httputil.BufferPool) *mirrorRoundTripper {
	rt := &mirrorRoundTripper{
		logger: logger,
		cfg:    cfg,
		next:   next,
		pool:   pool,

		proxies: make([]*httputil.ReverseProxy, len(cfg)),

		maxInflight: 500,
		timeout:     10 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt.cancel = cancel
	rt.g, rt.ctx = errgroup.WithContext(ctx)
	rt.g.SetLimit(rt.maxInflight)

	for i := range cfg {
		u, err := url.Parse(cfg[i].URL)
		// should never happen, but if it does, we panic
		if err != nil {
			panic(err)
		}
		rt.proxies[i] = httputil.NewSingleHostReverseProxy(u)
		rt.proxies[i].BufferPool = pool
	}
	return rt
}

func (m *mirrorRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	// select proxies to use
	selected := make([]int, 0, len(m.cfg))
	for i, cfg := range m.cfg {
		if cfg.Fraction == 0 {
			continue
		}
		if cfg.Fraction >= 1 || rand.Float64() < cfg.Fraction {
			selected = append(selected, i)
		}
	}

	// no proxies selected, use next
	if len(selected) == 0 {
		return m.next.RoundTrip(r)
	}

	// create body handler
	buf, readers := newMultiReaderBuffer(m.pool, len(selected))
	defer func() { _ = buf.Close() }()
	r.Body = buf.TeeReader(r.Body)

	for idx, selectedIdx := range selected {
		ok := m.g.TryGo(func() error {
			ctx, cancel := context.WithTimeout(m.ctx, m.timeout)
			defer cancel()

			// clone request
			req := r.Clone(ctx)
			req.Body = readers[idx]

			rw := newRwHeaderOnly()
			m.proxies[selectedIdx].ServeHTTP(rw, req)

			if rw.status != http.StatusOK {
				level.Warn(m.logger).Log("msg", "failed to mirror request", "status_code", rw.status, "mirror", m.cfg[selectedIdx].URL)
			}
			return nil
		})
		if !ok {
			level.Warn(m.logger).Log("msg", "failed to mirror request", "error", "mirror inflight limit reached")
		}
	}

	return m.next.RoundTrip(r)
}

func (m *mirrorRoundTripper) Close() error {
	m.cancel()
	return m.g.Wait()
}

type multiReaderBuffer struct {
	data   [][]byte
	waitCh chan struct{} // this channel closes once a new buffer element was added. If the channel is nil, we are done.
	mtx    sync.RWMutex
	offset int

	pool    httputil.BufferPool
	readers []*multiReader
}

// newMultiReaderBuffer allows to create multiple readers (io.ReadCloser) from a
// single io.Writer input.
// The amount of readers is fixed and needs to be provided when creating the
// buffer. Once all readers have moved past a buffer segment, it can be passed back to the pool.
//
// TODO(simonswine): Actually implement returning buffer segments to the pool.
func newMultiReaderBuffer(pool httputil.BufferPool, readerCount int) (*multiReaderBuffer, []io.ReadCloser) {
	b := &multiReaderBuffer{
		pool:    pool,
		readers: make([]*multiReader, readerCount),
		waitCh:  make(chan struct{}),
	}

	// initialize readers
	readers := make([]io.ReadCloser, readerCount)
	for i := range b.readers {
		b.readers[i] = &multiReader{
			mb:      b,
			closeCh: make(chan struct{}),
		}
		readers[i] = b.readers[i]
	}

	return b, readers
}

type teeReader struct {
	buf *multiReaderBuffer
	rc  io.ReadCloser
}

func (t *teeReader) Read(p []byte) (n int, err error) {
	n, err = t.rc.Read(p)
	if n > 0 {
		if n, err := t.buf.Write(p[:n]); err != nil {
			return n, err
		}
	}
	if err == io.EOF {
		_ = t.buf.Close()
	}
	return n, err
}

func (t *teeReader) Close() error {
	_ = t.buf.Close()
	return t.rc.Close()
}

func (mb *multiReaderBuffer) TeeReader(reader io.ReadCloser) io.ReadCloser {
	return &teeReader{
		buf: mb,
		rc:  reader,
	}
}

func (mb *multiReaderBuffer) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Cannot retain p, so we must copy it:
	var p2 []byte
	if mb.pool != nil {
		p2 = mb.pool.Get()
	}
	if cap(p2) < len(p) {
		p2 = make([]byte, len(p))
	} else {
		p2 = p2[:len(p)]
	}
	copy(p2, p)
	mb.mtx.Lock()
	mb.data = append(mb.data, p2)
	// TODO(simonswine): Implement returning no longer used buffer segments to the pool.

	// notify readers that we have new data
	if mb.waitCh == nil {
		// we were closed and the readers were already notified
		mb.mtx.Unlock()
		return 0, nil
	}
	close(mb.waitCh)
	mb.waitCh = make(chan struct{})

	mb.mtx.Unlock()
	return len(p), nil
}

func (mb *multiReaderBuffer) reclaimBuffer() {
	mb.mtx.Lock()
	defer mb.mtx.Unlock()

	if mb.data == nil {
		return
	}

	minI, maxI := math.MaxInt, -1
	for _, reader := range mb.readers {
		i := int(reader.i.Load())
		if i == -1 {
			continue
		}
		if i > maxI {
			maxI = i
		}
		if i < minI {
			minI = i
		}
	}

	// readers are done if they either all closed or all read the last buffer
	if maxI == -1 || (minI == len(mb.data)+mb.offset && mb.waitCh == nil) {
		// free memory
		if mb.pool != nil {
			for _, d := range mb.data {
				mb.pool.Put(d)
			}
		}
		mb.data = nil
		mb.offset = 0
		if mb.waitCh != nil {
			close(mb.waitCh)
			mb.waitCh = nil
		}
		return
	}

	if minI > mb.offset {
		diff := minI - mb.offset
		mb.offset = minI

		// recycle pools
		if mb.pool != nil {
			for i := 0; i < diff; i++ {
				mb.pool.Put(mb.data[i])
			}
		}
		copy(mb.data, mb.data[diff:])
		mb.data = mb.data[:len(mb.data)-diff]
	}
}

func (mb *multiReaderBuffer) Close() error {
	mb.mtx.Lock()
	defer mb.mtx.Unlock()

	// notify readers that we are done
	if mb.waitCh != nil {
		close(mb.waitCh)
		mb.waitCh = nil
	}

	return nil
}

type multiReader struct {
	mb *multiReaderBuffer

	mtx      sync.RWMutex // protects closeCh and data
	closeCh  chan struct{}
	i        atomic.Int64
	data     []byte
	finished sync.Once
}

func (mbr *multiReader) finish() {
	mbr.finished.Do(func() {
		mbr.i.Store(-1)
		mbr.mtx.Lock()
		mbr.data = nil
		mbr.mtx.Unlock()
		mbr.mb.reclaimBuffer()
	})
}

func (mbr *multiReader) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	// wait for data to be available
	for {
		mbr.mtx.RLock()
		dataLen := len(mbr.data)
		mbr.mtx.RUnlock()

		if dataLen > 0 {
			break
		}

		mbr.mtx.Lock()
		mbr.data = nil
		mbr.mtx.Unlock()

		mbr.mtx.RLock()
		closeCh := mbr.closeCh
		mbr.mtx.RUnlock()

		if closeCh == nil {
			// the reader was closed
			return 0, io.EOF
		}

		mb := mbr.mb
		mb.mtx.RLock()
		waitCh := mb.waitCh
		i := int(mbr.i.Load())

		if i == -1 {
			// the reader was closed
			mb.mtx.RUnlock()
			return 0, io.EOF
		}

		if i < len(mb.data)+mb.offset && i != -1 {
			mbr.mtx.Lock()
			mbr.data = mb.data[i-mb.offset]
			mbr.mtx.Unlock()
			mbr.i.Add(1)

			// we have data, so we can break out of the loop
			mb.mtx.RUnlock()
			break
		}
		mb.mtx.RUnlock()

		// if we have no waitCh, we are done
		if waitCh == nil {
			mbr.finish()
			return 0, io.EOF
		}

		mbr.mtx.RLock()
		closeCh = mbr.closeCh
		mbr.mtx.RUnlock()

		if closeCh == nil {
			// the reader was closed
			return 0, io.EOF
		}

		select {
		case <-waitCh:
		case <-closeCh:
			mbr.finish()
			return 0, io.EOF
		}
	}

	mbr.mtx.Lock()
	n = copy(p, mbr.data)
	mbr.data = mbr.data[n:]
	mbr.mtx.Unlock()
	return n, nil
}

func (mbr *multiReader) Close() error {
	mbr.mtx.Lock()
	ch := mbr.closeCh
	if ch != nil {
		mbr.closeCh = nil
		close(ch)
	}
	mbr.mtx.Unlock()
	mbr.finish()
	return nil
}

type rwHeaderOnly struct {
	header http.Header
	status int
}

func newRwHeaderOnly() *rwHeaderOnly {
	return &rwHeaderOnly{
		header: make(http.Header),
	}
}

func (m *rwHeaderOnly) Header() http.Header {
	return m.header
}

func (m *rwHeaderOnly) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (m *rwHeaderOnly) WriteHeader(statusCode int) {
	m.status = statusCode
}

func (m *rwHeaderOnly) Flush() {}
