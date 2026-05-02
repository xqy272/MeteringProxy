package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/extractor"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/writer"
)

// RecordWriter is the interface the proxy uses to enqueue usage records.
type RecordWriter interface {
	Enqueue(event writer.StatsEvent) bool
	IncrParseErrors()
}

type Proxy struct {
	upstream     string
	hasher       *hash.Hasher
	writer       RecordWriter
	maxSample    int64
	transport    *http.Transport
	SSELineSkips int64
}

func New(upstream string, hasher *hash.Hasher, rw RecordWriter, maxSample int64) *Proxy {
	return &Proxy{
		upstream:  upstream,
		hasher:    hasher,
		writer:    rw,
		maxSample: maxSample,
		transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Read a small prefix of the body for model detection only.
	// The full body is forwarded upstream through a counting reader.
	var bodyPrefix []byte
	if r.Body != nil {
		bodyPrefix, _ = io.ReadAll(io.LimitReader(r.Body, 4096))
	}
	cr := &countingReader{r: &replayReader{prefix: bodyPrefix, src: r.Body}}
	r.Body = cr

	clientIP := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		clientIP = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	clientIPHash := p.hasher.Hash(clientIP)
	apiKeyHash := p.hasher.Hash(bearerToken(r.Header.Get("Authorization")))

	modelRequested := extractModel(bodyPrefix)

	// Best-effort stream hint from the request, used only as a fallback
	// when the response Content-Type is missing or unparseable.
	requestSuggestsStream := r.URL.Query().Get("stream") == "true" ||
		streamFromJSON(bodyPrefix) ||
		isSSEMediaType(r.Header.Get("Accept"))

	endpoint := r.URL.Path

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, p.upstream+r.URL.RequestURI(), r.Body)
	if err != nil {
		p.writeError(w, start, 0, endpoint, r.Method, clientIPHash, apiKeyHash, modelRequested, cr.bytesRead, 502, err)
		return
	}
	upstreamReq.ContentLength = r.ContentLength

	for k, vs := range r.Header {
		for _, v := range vs {
			upstreamReq.Header.Add(k, v)
		}
	}

	resp, err := p.transport.RoundTrip(upstreamReq)
	ttfb := time.Since(start)
	if err != nil {
		p.writeError(w, start, ttfb, endpoint, r.Method, clientIPHash, apiKeyHash, modelRequested, cr.bytesRead, 502, err)
		return
	}
	defer resp.Body.Close()

	isStream := responseIndicatesStream(resp.Header.Get("Content-Type"), requestSuggestsStream)
	requestID := requestIDFromHeaders(resp.Header, r.Header)

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Metering-Proxy", "1")
	w.WriteHeader(resp.StatusCode)

	if isStream {
		p.handleStream(w, r, resp, start, ttfb, endpoint, requestID, clientIPHash, apiKeyHash, modelRequested, cr)
	} else {
		p.handleNonStream(w, r, resp, start, ttfb, endpoint, requestID, clientIPHash, apiKeyHash, modelRequested, cr)
	}
}

// ---------- countingReader ----------

// countingReader wraps a ReadCloser and tracks the total bytes read.
// This is used for request bodies so request_bytes reflects the actual
// bytes consumed during upstream forwarding, which is accurate even for
// chunked or HTTP/2 requests where ContentLength is -1.
type countingReader struct {
	r         io.ReadCloser
	bytesRead int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.bytesRead += int64(n)
	return n, err
}

func (cr *countingReader) Close() error {
	return cr.r.Close()
}

// ---------- replayReader ----------

type replayReader struct {
	prefix []byte
	src    io.ReadCloser
	offset int
}

func (r *replayReader) Read(p []byte) (int, error) {
	if r.offset < len(r.prefix) {
		n := copy(p, r.prefix[r.offset:])
		r.offset += n
		return n, nil
	}
	if r.src == nil {
		return 0, io.EOF
	}
	return r.src.Read(p)
}

func (r *replayReader) Close() error {
	if r.src != nil {
		return r.src.Close()
	}
	return nil
}

// ---------- streaming path (byte-transparent) ----------

func (p *Proxy) handleStream(w http.ResponseWriter, r *http.Request, resp *http.Response, start time.Time, ttfb time.Duration,
	endpoint, requestID, clientIPHash, apiKeyHash, modelRequested string, cr *countingReader) {

	flusher, ok := w.(http.Flusher)
	if !ok {
		written, err := io.Copy(w, resp.Body)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		p.recordUsage(start, ttfb, endpoint, r.Method, requestID, resp.StatusCode, true,
			clientIPHash, apiKeyHash, modelRequested, nil, cr.bytesRead, written, errStr)
		return
	}

	var lastUsage *extractor.UsageInfo
	var totalBytes int64
	var sseParseErrs int64

	// reportParseErrors flushes accumulated SSE parse error count to the writer.
	reportParseErrors := func() {
		for i := int64(0); i < sseParseErrs; i++ {
			p.writer.IncrParseErrors()
		}
		sseParseErrs = 0
	}

	const maxLine = 256 * 1024
	lineBuf := make([]byte, 0, 4096)
	buf := make([]byte, 32*1024)

	for {
		select {
		case <-r.Context().Done():
			reportParseErrors()
			errStr := ""
			if err := r.Context().Err(); err != nil {
				errStr = err.Error()
			}
			p.recordUsage(start, ttfb, endpoint, r.Method, requestID, resp.StatusCode, true,
				clientIPHash, apiKeyHash, modelRequested, lastUsage, cr.bytesRead, totalBytes, errStr)
			return
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			totalBytes += int64(n)

			if _, werr := w.Write(buf[:n]); werr != nil {
				reportParseErrors()
				p.recordUsage(start, ttfb, endpoint, r.Method, requestID, resp.StatusCode, true,
					clientIPHash, apiKeyHash, modelRequested, lastUsage, cr.bytesRead, totalBytes, werr.Error())
				return
			}
			flusher.Flush()

			chunk := buf[:n]
			for len(chunk) > 0 {
				nl := bytes.IndexByte(chunk, '\n')
				if nl < 0 {
					if len(lineBuf)+len(chunk) <= maxLine {
						lineBuf = append(lineBuf, chunk...)
					}
					break
				}
				var line []byte
				if len(lineBuf) > 0 {
					if len(lineBuf)+nl <= maxLine {
						line = append(lineBuf, chunk[:nl]...)
					} else {
						atomic.AddInt64(&p.SSELineSkips, 1)
					}
					lineBuf = lineBuf[:0]
				} else {
					if nl <= maxLine {
						line = chunk[:nl]
					}
				}
				chunk = chunk[nl+1:]

				if len(line) > 0 {
					u, err := p.tryExtractSSEUsage(line, endpoint)
					if err != nil {
						sseParseErrs++
					} else if u != nil {
						lastUsage = u
					}
				}
			}
		}

		if readErr != nil {
			errStr := ""
			if readErr != io.EOF {
				errStr = readErr.Error()
			}
			reportParseErrors()
			p.recordUsage(start, ttfb, endpoint, r.Method, requestID, resp.StatusCode, true,
				clientIPHash, apiKeyHash, modelRequested, lastUsage, cr.bytesRead, totalBytes, errStr)
			return
		}
	}
}

func (p *Proxy) tryExtractSSEUsage(line []byte, endpoint string) (*extractor.UsageInfo, error) {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil, nil
	}
	data := bytes.TrimSpace(trimmed[5:])
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return nil, nil
	}
	if strings.Contains(endpoint, "chat/completions") {
		return extractor.ExtractChatUsage(trimmed)
	}
	if strings.Contains(endpoint, "responses") {
		return extractor.ExtractResponsesUsage(trimmed)
	}
	return nil, nil
}

// ---------- non-streaming path ----------

type limitedBuffer struct {
	buf      []byte
	max      int
	overflow bool
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	origLen := len(p)
	if lb.overflow {
		return origLen, nil
	}
	need := lb.max - len(lb.buf)
	if len(p) > need {
		p = p[:need]
		lb.overflow = true
	}
	lb.buf = append(lb.buf, p...)
	return origLen, nil
}

func (lb *limitedBuffer) Bytes() []byte {
	return lb.buf
}

func (p *Proxy) handleNonStream(w http.ResponseWriter, r *http.Request, resp *http.Response, start time.Time, ttfb time.Duration,
	endpoint, requestID, clientIPHash, apiKeyHash, modelRequested string, cr *countingReader) {

	lb := &limitedBuffer{max: int(p.maxSample)}
	reader := io.TeeReader(resp.Body, lb)
	written, copyErr := io.Copy(w, reader)

	var usage *extractor.UsageInfo
	sample := lb.Bytes()
	if len(sample) > 0 {
		u, err := extractor.ExtractNonStreaming(sample, endpoint)
		if err != nil {
			// Count parse error only when the sample is complete (not truncated).
			if !lb.overflow {
				p.writer.IncrParseErrors()
			}
		} else {
			usage = u
		}
	}

	errStr := ""
	if copyErr != nil {
		errStr = copyErr.Error()
	}
	p.recordUsage(start, ttfb, endpoint, r.Method, requestID, resp.StatusCode, false,
		clientIPHash, apiKeyHash, modelRequested, usage, cr.bytesRead, written, errStr)
}

// ---------- recording ----------

func (p *Proxy) recordUsage(start time.Time, ttfb time.Duration, endpoint, method, requestID string, status int, stream bool,
	clientIPHash, apiKeyHash, modelRequested string, usage *extractor.UsageInfo,
	requestBytes, responseBytes int64, errStr string) {

	if requestBytes < 0 {
		requestBytes = 0
	}

	record := db.UsageRecord{
		CreatedAt:      start.UTC().Format(time.RFC3339),
		RequestID:      requestID,
		Endpoint:       endpoint,
		Method:         method,
		Status:         status,
		LatencyMs:      time.Since(start).Milliseconds(),
		TTFBMs:         ttfb.Milliseconds(),
		Stream:         stream,
		ClientIPHash:   clientIPHash,
		APIKeyHash:     apiKeyHash,
		ModelRequested: modelRequested,
		RequestBytes:   requestBytes,
		ResponseBytes:  responseBytes,
		Error:          errStr,
	}

	if usage != nil {
		record.ModelReturned = usage.Model
		record.InputTokens = usage.InputTokens
		record.OutputTokens = usage.OutputTokens
		record.ReasoningTokens = usage.ReasoningTokens
		record.CachedTokens = usage.CachedTokens
		record.TotalTokens = usage.TotalTokens
	}

	p.writer.Enqueue(writer.StatsEvent{Record: record})
}

func (p *Proxy) writeError(w http.ResponseWriter, start time.Time, ttfb time.Duration, endpoint, method, clientIPHash, apiKeyHash, modelRequested string, requestBytes int64, status int, err error) {
	w.Header().Set("X-Metering-Proxy", "1")
	http.Error(w, fmt.Sprintf("upstream error: %v", err), status)
	p.recordUsage(start, ttfb, endpoint, method, "", status, false,
		clientIPHash, apiKeyHash, modelRequested, nil, requestBytes, 0, err.Error())
}

// ---------- helpers ----------

func extractModel(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err == nil && req.Model != "" {
		return req.Model
	}
	return ""
}

// isSSEMediaType returns true if the Content-Type or Accept header value
// indicates text/event-stream, using case-insensitive MIME type matching.
func isSSEMediaType(headerValue string) bool {
	if headerValue == "" {
		return false
	}
	for _, part := range strings.Split(headerValue, ",") {
		mediatype, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err == nil && strings.EqualFold(mediatype, "text/event-stream") {
			return true
		}
	}
	return false
}

func responseIndicatesStream(contentType string, fallback bool) bool {
	if contentType == "" {
		return fallback
	}
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fallback
	}
	return strings.EqualFold(mediatype, "text/event-stream")
}

func bearerToken(auth string) string {
	fields := strings.Fields(auth)
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return fields[1]
	}
	return auth
}

func requestIDFromHeaders(respHeader, reqHeader http.Header) string {
	for _, name := range []string{"OpenAI-Request-ID", "X-Request-ID", "X-Request-Id", "Request-ID"} {
		if value := respHeader.Get(name); value != "" {
			return value
		}
	}
	for _, name := range []string{"X-Request-ID", "X-Request-Id", "Request-ID"} {
		if value := reqHeader.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func streamFromJSON(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var req struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	return req.Stream
}
