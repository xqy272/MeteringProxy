package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/writer"
)

type benchmarkRW struct {
	events      atomic.Int64
	parseErrors atomic.Int64
}

func (rw *benchmarkRW) Enqueue(writer.StatsEvent) bool {
	rw.events.Add(1)
	return true
}

func (rw *benchmarkRW) IncrParseErrors() {
	rw.parseErrors.Add(1)
}

type proxyBenchmarkCase struct {
	name                string
	method              string
	path                string
	contentType         string
	accept              string
	requestBody         []byte
	responseBody        []byte
	responseContentType string
	sseChunks           [][]byte
}

func BenchmarkProxyBaseline(b *testing.B) {
	nonstreamSmallReq := makeSizedJSONRequest(4*1024, true, false)
	nonstreamSmallResp := makeSizedJSONResponse(8 * 1024)
	nonstreamLargeReq := makeSizedJSONRequest(1*1024*1024, true, false)
	nonstreamLargeResp := makeSizedJSONResponse(1 * 1024 * 1024)
	sseSmallReq := makeSizedJSONRequest(4*1024, true, true)
	sseSmallChunks := makeSSEChunks(100, 200)
	sseLargeReq := makeSizedJSONRequest(4*1024, true, true)
	sseLargeChunks := makeSSEChunks(10000, 1024)
	requestOnlyLargeReq := bytes.Repeat([]byte("a"), 10*1024*1024)
	requestOnlyLargeResp := bytes.Repeat([]byte("b"), 10*1024*1024)

	cases := []proxyBenchmarkCase{
		{
			name:                "benchmark-nonstream-small",
			method:              http.MethodPost,
			path:                "/v1/chat/completions",
			contentType:         "application/json",
			requestBody:         nonstreamSmallReq,
			responseBody:        nonstreamSmallResp,
			responseContentType: "application/json",
		},
		{
			name:                "benchmark-nonstream-large",
			method:              http.MethodPost,
			path:                "/v1/chat/completions",
			contentType:         "application/json",
			requestBody:         nonstreamLargeReq,
			responseBody:        nonstreamLargeResp,
			responseContentType: "application/json",
		},
		{
			name:                "benchmark-sse-small",
			method:              http.MethodPost,
			path:                "/v1/chat/completions",
			contentType:         "application/json",
			accept:              "text/event-stream",
			requestBody:         sseSmallReq,
			responseContentType: "text/event-stream",
			sseChunks:           sseSmallChunks,
		},
		{
			name:                "benchmark-sse-large",
			method:              http.MethodPost,
			path:                "/v1/chat/completions",
			contentType:         "application/json",
			accept:              "text/event-stream",
			requestBody:         sseLargeReq,
			responseContentType: "text/event-stream",
			sseChunks:           sseLargeChunks,
		},
		{
			name:                "benchmark-request-only-large",
			method:              http.MethodPost,
			path:                "/v1/audio/transcriptions",
			contentType:         "application/octet-stream",
			requestBody:         requestOnlyLargeReq,
			responseBody:        requestOnlyLargeResp,
			responseContentType: "application/octet-stream",
		},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			benchmarkProxySequential(b, tc)
		})
	}

	b.Run("benchmark-concurrent", func(b *testing.B) {
		benchmarkProxyConcurrent(b, proxyBenchmarkCase{
			name:                "benchmark-concurrent",
			method:              http.MethodPost,
			path:                "/v1/chat/completions",
			contentType:         "application/json",
			requestBody:         nonstreamSmallReq,
			responseBody:        nonstreamSmallResp,
			responseContentType: "application/json",
		}, 50)
	})
}

func BenchmarkProxyPreRoundTripOverhead(b *testing.B) {
	largeRequestOnlyBody := strings.Repeat("a", 10*1024*1024)
	largeMeteredWithModel := string(makeSizedJSONRequest(10*1024*1024, true, false))
	largeMeteredNoModel := string(makeSizedJSONRequest(10*1024*1024, false, false))

	cases := []struct {
		name    string
		method  string
		path    string
		body    string
		reqMeta config.RequestMetadataConfig
	}{
		{
			name:    "request-only-10mb-no-prefix",
			method:  http.MethodPost,
			path:    "/v1/audio/transcriptions",
			body:    largeRequestOnlyBody,
			reqMeta: benchmarkRequestMetadata(false),
		},
		{
			name:    "usage-metered-10mb-default-prefix-4kb",
			method:  http.MethodPost,
			path:    "/v1/chat/completions",
			body:    largeMeteredWithModel,
			reqMeta: benchmarkRequestMetadata(false),
		},
		{
			name:    "usage-metered-10mb-extended-scan-64kb",
			method:  http.MethodPost,
			path:    "/v1/chat/completions",
			body:    largeMeteredNoModel,
			reqMeta: benchmarkRequestMetadata(true),
		},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			benchmarkPreRoundTripOnly(b, tc.method, tc.path, tc.body, tc.reqMeta)
		})
	}
}

func benchmarkProxySequential(b *testing.B, tc proxyBenchmarkCase) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(tc.requestBody) + tc.responseSize()))

	rw := &benchmarkRW{}
	p := newBenchmarkProxy(rw, benchmarkRequestMetadata(false))

	preRoundTrip := make([]time.Duration, b.N)
	var requestStart time.Time
	var iteration int
	var currentTracker *benchmarkSSELatency
	var roundTripErr error

	p.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		preRoundTrip[iteration] = time.Since(requestStart)
		if req.Body != nil {
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				roundTripErr = err
				return nil, err
			}
		}
		return benchmarkResponse(req, tc, currentTracker), nil
	})

	template := newBenchmarkRequestTemplate(tc)
	var totalSSELatency int64
	var totalSSEChunks int64
	var maxSSELatency int64
	var flushes int64

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		iteration = i
		var tracker *benchmarkSSELatency
		if len(tc.sseChunks) > 0 {
			tracker = &benchmarkSSELatency{}
		}
		currentTracker = tracker
		req := newBenchmarkRequest(template, tc.requestBody)
		rec := newBenchmarkResponseWriter(tracker)
		requestStart = time.Now()
		p.ServeHTTP(rec, req)
		if roundTripErr != nil {
			b.Fatalf("round trip: %v", roundTripErr)
		}
		if status := rec.statusCode(); status != http.StatusOK {
			b.Fatalf("status = %d, want %d", status, http.StatusOK)
		}
		if rec.bytes != int64(tc.responseSize()) {
			b.Fatalf("response bytes = %d, want %d", rec.bytes, tc.responseSize())
		}
		if tracker != nil {
			totalSSELatency += tracker.totalLatencyNs
			totalSSEChunks += tracker.chunks
			if tracker.maxLatencyNs > maxSSELatency {
				maxSSELatency = tracker.maxLatencyNs
			}
			flushes += rec.flushes
		}
	}
	b.StopTimer()

	reportDurationStats(b, "pre_roundtrip", preRoundTrip)
	if totalSSEChunks > 0 {
		b.ReportMetric(float64(totalSSELatency)/float64(totalSSEChunks), "sse_chunk_forward_avg_ns")
		b.ReportMetric(float64(maxSSELatency), "sse_chunk_forward_max_ns")
		b.ReportMetric(float64(totalSSEChunks)/float64(b.N), "sse_chunks/op")
		b.ReportMetric(float64(flushes)/float64(b.N), "flushes/op")
	}
}

func benchmarkProxyConcurrent(b *testing.B, tc proxyBenchmarkCase, concurrency int) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(tc.requestBody) + tc.responseSize()))

	rw := &benchmarkRW{}
	p := newBenchmarkProxy(rw, benchmarkRequestMetadata(false))
	preRoundTrip := make([]time.Duration, b.N)
	requestStarts := make([]time.Time, b.N)

	p.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		idx, _ := req.Context().Value(benchmarkRequestIndexKey{}).(int)
		preRoundTrip[idx] = time.Since(requestStarts[idx])
		if req.Body != nil {
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				return nil, err
			}
		}
		return benchmarkResponse(req, tc, nil), nil
	})

	template := newBenchmarkRequestTemplate(tc)
	var next atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	b.ResetTimer()
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx := int(next.Add(1) - 1)
				if idx >= b.N {
					return
				}
				req := newBenchmarkRequest(template, tc.requestBody)
				requestStarts[idx] = time.Now()
				req = req.WithContext(context.WithValue(req.Context(), benchmarkRequestIndexKey{}, idx))
				rec := newBenchmarkResponseWriter(nil)
				p.ServeHTTP(rec, req)
				if status := rec.statusCode(); status != http.StatusOK {
					select {
					case errCh <- benchmarkStatusError(status):
					default:
					}
					return
				}
				if rec.bytes != int64(tc.responseSize()) {
					select {
					case errCh <- benchmarkByteCountError{got: rec.bytes, want: int64(tc.responseSize())}:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	b.StopTimer()

	select {
	case err := <-errCh:
		b.Fatal(err)
	default:
	}
	reportDurationStats(b, "pre_roundtrip", preRoundTrip)
	b.ReportMetric(float64(concurrency), "concurrency")
}

func benchmarkPreRoundTripOnly(b *testing.B, method, path, body string, reqMeta config.RequestMetadataConfig) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))

	rw := &benchmarkRW{}
	p := newBenchmarkProxy(rw, reqMeta)
	preRoundTrip := make([]time.Duration, b.N)
	bodyReads := make([]int64, b.N)

	var requestStart time.Time
	var iteration int
	var currentReads *atomic.Int64

	p.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		preRoundTrip[iteration] = time.Since(requestStart)
		if currentReads != nil {
			bodyReads[iteration] = currentReads.Load()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    req,
		}, nil
	})

	tc := proxyBenchmarkCase{
		method:      method,
		path:        path,
		contentType: "application/json",
	}
	if strings.Contains(path, "/audio/") {
		tc.contentType = "application/octet-stream"
	}
	template := newBenchmarkRequestTemplate(tc)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		iteration = i
		reads := &atomic.Int64{}
		currentReads = reads
		req := newBenchmarkStringRequest(template, body, reads)
		rec := newBenchmarkResponseWriter(nil)
		requestStart = time.Now()
		p.ServeHTTP(rec, req)
		if status := rec.statusCode(); status != http.StatusOK {
			b.Fatalf("status = %d, want %d", status, http.StatusOK)
		}
	}
	b.StopTimer()

	reportDurationStats(b, "pre_roundtrip", preRoundTrip)
	reportInt64Average(b, "body_reads_at_roundtrip/op", bodyReads)
}

func newBenchmarkProxy(rw RecordWriter, reqMeta config.RequestMetadataConfig) *Proxy {
	return New("http://benchmark-upstream", hash.NewWithSalt("benchmark-salt"), rw, 2*1024*1024, reqMeta)
}

func benchmarkRequestMetadata(extendedScan bool) config.RequestMetadataConfig {
	return config.RequestMetadataConfig{
		InitialBytes:      4096,
		MaxBytes:          65536,
		ExtendedModelScan: extendedScan,
	}
}

func benchmarkResponse(req *http.Request, tc proxyBenchmarkCase, tracker *benchmarkSSELatency) *http.Response {
	body := io.NopCloser(bytes.NewReader(tc.responseBody))
	cl := int64(-1)
	if len(tc.sseChunks) > 0 {
		body = &benchmarkSSEBody{chunks: tc.sseChunks, tracker: tracker}
		cl = -1 // streaming responses do not advertise a fixed Content-Length
	} else {
		cl = int64(len(tc.responseBody))
	}
	return &http.Response{
		StatusCode:     http.StatusOK,
		Header:         http.Header{"Content-Type": []string{tc.responseContentType}},
		Body:           body,
		ContentLength:  cl,
		Request:        req,
	}
}

func newBenchmarkRequestTemplate(tc proxyBenchmarkCase) *http.Request {
	req := httptest.NewRequest(tc.method, tc.path, nil)
	if tc.contentType != "" {
		req.Header.Set("Content-Type", tc.contentType)
	}
	if tc.accept != "" {
		req.Header.Set("Accept", tc.accept)
	}
	req.Header.Set("Authorization", "Bearer benchmark-key")
	return req
}

func newBenchmarkRequest(template *http.Request, body []byte) *http.Request {
	req := new(http.Request)
	*req = *template
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	return req
}

func newBenchmarkStringRequest(template *http.Request, body string, reads *atomic.Int64) *http.Request {
	req := new(http.Request)
	*req = *template
	req.Body = &countingTestReadCloser{reader: strings.NewReader(body), reads: reads}
	req.ContentLength = int64(len(body))
	return req
}

func (tc proxyBenchmarkCase) responseSize() int {
	if len(tc.sseChunks) == 0 {
		return len(tc.responseBody)
	}
	total := 0
	for _, chunk := range tc.sseChunks {
		total += len(chunk)
	}
	return total
}

func makeSizedJSONRequest(size int, includeModel bool, stream bool) []byte {
	fields := make([]string, 0, 2)
	if includeModel {
		fields = append(fields, `"model":"gpt-4o"`)
	}
	if stream {
		fields = append(fields, `"stream":true`)
	}
	prefix := `{`
	if len(fields) > 0 {
		prefix += strings.Join(fields, ",") + `,`
	}
	prefix += `"messages":[{"role":"user","content":"`
	suffix := `"}]}`
	return makeSizedBytes(prefix, suffix, size)
}

func makeSizedJSONResponse(size int) []byte {
	return makeSizedBytes(
		`{"model":"gpt-4o","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"padding":"`,
		`"}`,
		size,
	)
}

func makeSSEChunks(count, chunkSize int) [][]byte {
	chunks := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		if i == count-1 {
			chunks = append(chunks, makeSizedBytes(
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"padding":"`,
				`"}`+"\n\n",
				chunkSize,
			))
			continue
		}
		chunks = append(chunks, makeSizedBytes(
			`data: {"choices":[{"delta":{"content":"`,
			`"}}]}`+"\n\n",
			chunkSize,
		))
	}
	return chunks
}

func makeSizedBytes(prefix, suffix string, size int) []byte {
	if size < len(prefix)+len(suffix) {
		panic("benchmark payload size is smaller than fixed JSON framing")
	}
	out := make([]byte, 0, size)
	out = append(out, prefix...)
	out = append(out, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...)
	out = append(out, suffix...)
	return out
}

type benchmarkResponseWriter struct {
	header  http.Header
	status  int
	bytes   int64
	flushes int64
	tracker *benchmarkSSELatency
}

func newBenchmarkResponseWriter(tracker *benchmarkSSELatency) *benchmarkResponseWriter {
	return &benchmarkResponseWriter{header: make(http.Header), tracker: tracker}
}

func (w *benchmarkResponseWriter) Header() http.Header {
	return w.header
}

func (w *benchmarkResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *benchmarkResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.bytes += int64(len(p))
	if w.tracker != nil {
		w.tracker.recordWrite()
	}
	return len(p), nil
}

func (w *benchmarkResponseWriter) Flush() {
	w.flushes++
}

func (w *benchmarkResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

type benchmarkSSELatency struct {
	lastReadUnixNano int64
	totalLatencyNs   int64
	maxLatencyNs     int64
	chunks           int64
}

func (l *benchmarkSSELatency) recordRead() {
	l.lastReadUnixNano = time.Now().UnixNano()
}

func (l *benchmarkSSELatency) recordWrite() {
	if l.lastReadUnixNano == 0 {
		return
	}
	elapsed := time.Now().UnixNano() - l.lastReadUnixNano
	l.totalLatencyNs += elapsed
	if elapsed > l.maxLatencyNs {
		l.maxLatencyNs = elapsed
	}
	l.chunks++
}

type benchmarkSSEBody struct {
	chunks  [][]byte
	index   int
	tracker *benchmarkSSELatency
}

func (b *benchmarkSSEBody) Read(p []byte) (int, error) {
	if b.index >= len(b.chunks) {
		return 0, io.EOF
	}
	chunk := b.chunks[b.index]
	b.index++
	if len(chunk) > len(p) {
		return 0, io.ErrShortBuffer
	}
	n := copy(p, chunk)
	if b.tracker != nil {
		b.tracker.recordRead()
	}
	return n, nil
}

func (b *benchmarkSSEBody) Close() error {
	return nil
}

func reportDurationStats(b *testing.B, prefix string, durations []time.Duration) {
	b.Helper()
	if len(durations) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total int64
	for _, d := range durations {
		total += d.Nanoseconds()
	}
	b.ReportMetric(float64(total)/float64(len(durations)), prefix+"_avg_ns/op")
	b.ReportMetric(float64(percentileDuration(sorted, 50).Nanoseconds()), prefix+"_p50_ns")
	b.ReportMetric(float64(percentileDuration(sorted, 95).Nanoseconds()), prefix+"_p95_ns")
	b.ReportMetric(float64(percentileDuration(sorted, 99).Nanoseconds()), prefix+"_p99_ns")
}

func percentileDuration(sorted []time.Duration, percentile int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted)*percentile + 99) / 100
	if idx <= 0 {
		return sorted[0]
	}
	if idx > len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[idx-1]
}

func reportInt64Average(b *testing.B, unit string, values []int64) {
	b.Helper()
	if len(values) == 0 {
		return
	}
	var total int64
	for _, value := range values {
		total += value
	}
	b.ReportMetric(float64(total)/float64(len(values)), unit)
}

type benchmarkRequestIndexKey struct{}

type benchmarkStatusError int

func (e benchmarkStatusError) Error() string {
	return "unexpected status: " + http.StatusText(int(e))
}

type benchmarkByteCountError struct {
	got  int64
	want int64
}

func (e benchmarkByteCountError) Error() string {
	return "unexpected response byte count: got " + formatBenchmarkInt(e.got) + ", want " + formatBenchmarkInt(e.want)
}

func formatBenchmarkInt(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
