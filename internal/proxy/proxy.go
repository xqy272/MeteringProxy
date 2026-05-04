package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/extractor"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/metrics"
	"ai-gateway-metering-proxy/internal/profile"
	"ai-gateway-metering-proxy/internal/writer"
)

// RecordWriter is the interface the proxy uses to enqueue usage events.
type RecordWriter interface {
	Enqueue(event writer.StatsEvent) bool
	IncrParseErrors()
}

type Proxy struct {
	upstream        string
	hasher          *hash.Hasher
	writer          RecordWriter
	maxSample       int64
	meteringEnabled bool
	registry        *profile.Registry
	transport       *http.Transport
}

func New(upstream string, hasher *hash.Hasher, rw RecordWriter, maxSample int64) *Proxy {
	return &Proxy{
		upstream:        upstream,
		hasher:          hasher,
		writer:          rw,
		maxSample:       maxSample,
		meteringEnabled: true,
		registry:        profile.NewRegistry(),
		transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
	}
}

func (p *Proxy) SetMeteringEnabled(enabled bool) {
	p.meteringEnabled = enabled
	metrics.SetMeteringEnabled(enabled)
}

func (p *Proxy) Registry() *profile.Registry {
	return p.registry
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	prof, err := p.registry.Match(r.Method, r.URL.Path)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	endpoint := r.URL.Path

	if !p.meteringEnabled || !prof.IsMetered() {
		p.forwardTransparent(w, r)
		return
	}

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
	requestSuggestsStream := r.URL.Query().Get("stream") == "true" ||
		streamFromJSON(bodyPrefix) ||
		isSSEMediaType(r.Header.Get("Accept"))

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, p.upstream+r.URL.RequestURI(), r.Body)
	if err != nil {
		p.writeError(w, start, 0, prof, endpoint, r.Method, clientIPHash, apiKeyHash, modelRequested, cr.bytesRead, 502, err)
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
		p.writeError(w, start, ttfb, prof, endpoint, r.Method, clientIPHash, apiKeyHash, modelRequested, cr.bytesRead, 502, err)
		return
	}
	defer resp.Body.Close()

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	isStream := responseIndicatesStream(resp.Header.Get("Content-Type"), requestSuggestsStream)
	requestID := requestIDFromHeaders(resp.Header, r.Header)

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Metering-Proxy", "1")
	w.WriteHeader(status)

	if isStream {
		p.handleStream(w, r, resp, start, ttfb, prof, endpoint, requestID, clientIPHash, apiKeyHash, modelRequested, cr, status)
	} else {
		p.handleNonStream(w, r, resp, start, ttfb, prof, endpoint, requestID, clientIPHash, apiKeyHash, modelRequested, cr, status)
	}
}

// forwardTransparent forwards the request without body prefix read or metering.
func (p *Proxy) forwardTransparent(w http.ResponseWriter, r *http.Request) {
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, p.upstream+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, "upstream error", 502)
		return
	}
	upstreamReq.ContentLength = r.ContentLength
	for k, vs := range r.Header {
		for _, v := range vs {
			upstreamReq.Header.Add(k, v)
		}
	}

	resp, err := p.transport.RoundTrip(upstreamReq)
	if err != nil {
		http.Error(w, "upstream error", 502)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Metering-Proxy", "1")
	status := resp.StatusCode
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	if responseIndicatesStream(resp.Header.Get("Content-Type"), isSSEMediaType(r.Header.Get("Accept"))) {
		copyAndFlush(w, resp.Body)
		return
	}
	io.Copy(w, resp.Body)
}

func copyAndFlush(w http.ResponseWriter, r io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// ---------- countingReader ----------

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

// ---------- streaming path ----------

func (p *Proxy) handleStream(w http.ResponseWriter, r *http.Request, resp *http.Response, start time.Time, ttfb time.Duration,
	prof *profile.EndpointProfile, endpoint, requestID, clientIPHash, apiKeyHash, modelRequested string, cr *countingReader, status int) {

	flusher, ok := w.(http.Flusher)
	if !ok {
		written, err := io.Copy(w, resp.Body)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		captureOutcome := event.OutcomeSkipped
		captureReason := event.ReasonUsageNotPresent
		p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
			clientIPHash, apiKeyHash, modelRequested, nil, cr.bytesRead, written, errStr,
			captureOutcome, captureReason, nil)
		return
	}

	var lastUsage *extractor.UsageInfo
	var totalBytes int64
	var sseParseErrs int64

	var errPayloads *errorPayloadSampler
	if status >= 400 {
		errPayloads = newErrorPayloadSampler()
	}

	finalizeStreamErrInfo := func() *extractor.ErrorInfo {
		if errPayloads == nil {
			return nil
		}
		sample := errPayloads.finalize()
		if len(sample) == 0 {
			return nil
		}
		info, ierr := extractor.ExtractErrorInfo(sample, status, resp.Header.Get("Content-Type"))
		if ierr != nil || info == nil {
			return nil
		}
		return info
	}

	reportParseErrors := func() {
		for i := int64(0); i < sseParseErrs; i++ {
			p.writer.IncrParseErrors()
		}
		sseParseErrs = 0
	}

	maxLine := 256 * 1024
	if prof != nil && prof.StreamProtocol.MaxLineSize > 0 {
		maxLine = prof.StreamProtocol.MaxLineSize
	}
	lineBuf := make([]byte, 0, 4096)
	lineOverflow := false
	buf := make([]byte, 32*1024)
	recordLineSkip := func() {
		metrics.AddSSELineSkips(1)
	}

	for {
		select {
		case <-r.Context().Done():
			reportParseErrors()
			errStr := ""
			if err := r.Context().Err(); err != nil {
				errStr = err.Error()
			}
			captureOutcome := event.OutcomeCaptured
			captureReason := ""
			if lastUsage == nil {
				captureOutcome = event.OutcomeSkipped
				captureReason = event.ReasonUsageNotPresent
			}
			p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
				clientIPHash, apiKeyHash, modelRequested, lastUsage, cr.bytesRead, totalBytes, errStr,
				captureOutcome, captureReason, finalizeStreamErrInfo())
			return
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			totalBytes += int64(n)

			if _, werr := w.Write(buf[:n]); werr != nil {
				reportParseErrors()
				captureOutcome := event.OutcomeCaptured
				captureReason := ""
				if lastUsage == nil {
					captureOutcome = event.OutcomeSkipped
					captureReason = event.ReasonUsageNotPresent
				}
				p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
					clientIPHash, apiKeyHash, modelRequested, lastUsage, cr.bytesRead, totalBytes, werr.Error(),
					captureOutcome, captureReason, finalizeStreamErrInfo())
				return
			}
			flusher.Flush()

			chunk := buf[:n]
			for len(chunk) > 0 {
				nl := bytes.IndexByte(chunk, '\n')
				if nl < 0 {
					if !lineOverflow {
						if len(lineBuf)+len(chunk) <= maxLine {
							lineBuf = append(lineBuf, chunk...)
						} else {
							recordLineSkip()
							lineOverflow = true
							lineBuf = lineBuf[:0]
						}
					}
					break
				}
				var line []byte
				if lineOverflow {
					lineOverflow = false
					lineBuf = lineBuf[:0]
				} else {
					if len(lineBuf) > 0 {
						if len(lineBuf)+nl <= maxLine {
							line = append(lineBuf, chunk[:nl]...)
						} else {
							recordLineSkip()
						}
						lineBuf = lineBuf[:0]
					} else {
						if nl <= maxLine {
							line = chunk[:nl]
						} else {
							recordLineSkip()
						}
					}
				}
				chunk = chunk[nl+1:]

				if len(line) > 0 {
					if errPayloads != nil {
						errPayloads.addStrippedPayload(stripSSEDataLine(line, prof))
					}
					u, err := p.tryExtractSSEUsage(line, prof)
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
			captureOutcome := event.OutcomeCaptured
			captureReason := ""
			if lastUsage == nil {
				captureOutcome = event.OutcomeSkipped
				captureReason = event.ReasonUsageNotPresent
			}
			p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
				clientIPHash, apiKeyHash, modelRequested, lastUsage, cr.bytesRead, totalBytes, errStr,
				captureOutcome, captureReason, finalizeStreamErrInfo())
			return
		}
	}
}

func (p *Proxy) tryExtractSSEUsage(line []byte, prof *profile.EndpointProfile) (*extractor.UsageInfo, error) {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil, nil
	}
	data := bytes.TrimSpace(trimmed[5:])
	if len(data) == 0 {
		return nil, nil
	}
	if prof != nil && prof.StreamProtocol.CompletionMarker != "" {
		if bytes.Equal(data, []byte(prof.StreamProtocol.CompletionMarker)) {
			return nil, nil
		}
	} else if bytes.Equal(data, []byte("[DONE]")) {
		return nil, nil
	}
	if prof != nil && prof.StreamExtractor != nil {
		return prof.StreamExtractor(trimmed)
	}
	return nil, nil
}

// errorPayloadSampler collects SSE data: payloads for error classification.
type errorPayloadSampler struct {
	payloads   [][]byte
	totalBytes int
	overflow   bool
	locked     bool
}

func newErrorPayloadSampler() *errorPayloadSampler {
	return &errorPayloadSampler{}
}

func (s *errorPayloadSampler) addStrippedPayload(payload []byte) {
	if len(payload) == 0 {
		return
	}
	if s.locked || s.overflow {
		return
	}
	if len(s.payloads) >= 5 {
		s.overflow = true
		s.payloads = nil
		return
	}
	pl := make([]byte, len(payload))
	copy(pl, payload)
	s.payloads = append(s.payloads, pl)
	s.totalBytes += len(pl)
	if s.totalBytes > 8*1024 {
		s.overflow = true
		s.payloads = nil
	}
}

func (s *errorPayloadSampler) finalize() []byte {
	s.locked = true
	if s.overflow || len(s.payloads) == 0 {
		return nil
	}
	var result []byte
	for i, p := range s.payloads {
		if i > 0 {
			result = append(result, '\n')
		}
		result = append(result, p...)
	}
	return result
}

// stripSSEDataLine strips the "data:" prefix from an SSE line.
func stripSSEDataLine(line []byte, prof *profile.EndpointProfile) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil
	}
	data := bytes.TrimSpace(trimmed[5:])
	if len(data) == 0 {
		return nil
	}
	if prof != nil && prof.StreamProtocol.CompletionMarker != "" {
		if bytes.Equal(data, []byte(prof.StreamProtocol.CompletionMarker)) {
			return nil
		}
	} else if bytes.Equal(data, []byte("[DONE]")) {
		return nil
	}
	return data
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
	prof *profile.EndpointProfile, endpoint, requestID, clientIPHash, apiKeyHash, modelRequested string, cr *countingReader, status int) {

	lb := &limitedBuffer{max: int(p.maxSample)}
	reader := io.TeeReader(resp.Body, lb)
	written, copyErr := io.Copy(w, reader)

	var usage *extractor.UsageInfo
	captureOutcome := event.OutcomeCaptured
	captureReason := ""

	sample := lb.Bytes()
	if len(sample) > 0 {
		if prof != nil && prof.NonStreamExtractor != nil {
			u, err := prof.NonStreamExtractor(sample, endpoint)
			if err != nil {
				if !lb.overflow {
					p.writer.IncrParseErrors()
				}
				if lb.overflow {
					captureReason = event.ReasonSampleLimitExceeded
				} else {
					captureReason = event.ReasonParseError
					captureOutcome = event.OutcomeFailed
				}
			} else {
				usage = u
			}
		}
	}
	if usage == nil && captureOutcome == event.OutcomeCaptured && captureReason == "" {
		captureReason = event.ReasonUsageNotPresent
		captureOutcome = event.OutcomeSkipped
	}

	var errInfo *extractor.ErrorInfo
	if status >= 400 && len(sample) > 0 {
		contentType := resp.Header.Get("Content-Type")
		if info, ierr := extractor.ExtractErrorInfo(sample, status, contentType); ierr == nil && info != nil {
			errInfo = info
		}
	}

	errStr := ""
	if copyErr != nil {
		errStr = copyErr.Error()
	}
	p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, false,
		clientIPHash, apiKeyHash, modelRequested, usage, cr.bytesRead, written, errStr,
		captureOutcome, captureReason, errInfo)
}

// ---------- recording ----------

func (p *Proxy) recordUsage(start time.Time, ttfb time.Duration, prof *profile.EndpointProfile, endpoint, method, requestID string, status int, stream bool,
	clientIPHash, apiKeyHash, modelRequested string, usage *extractor.UsageInfo,
	requestBytes, responseBytes int64, errStr string,
	captureOutcome, captureReason string, errInfo *extractor.ErrorInfo) {

	if requestBytes < 0 {
		requestBytes = 0
	}

	profileName := ""
	captureMode := event.CapturePassthrough
	meteringKind := event.MeteringNone
	if prof != nil {
		profileName = prof.Name
		captureMode = prof.CaptureMode
		meteringKind = prof.MeteringKind
	}

	latencyMs := time.Since(start).Milliseconds()
	metrics.ObserveRequest(latencyMs, ttfb.Milliseconds())

	ev := event.Event{
		ID:        requestID,
		Timestamp: start,

		EndpointProfile: profileName,
		CaptureMode:     captureMode,
		MeteringKind:    meteringKind,

		Method:    method,
		Path:      endpoint,
		Status:    status,
		Stream:    stream,
		LatencyMs: latencyMs,
		TTFBMs:    ttfb.Milliseconds(),

		APIKeyHash:   apiKeyHash,
		ClientIPHash: clientIPHash,

		ModelRequested: modelRequested,

		RequestBytes:  requestBytes,
		ResponseBytes: responseBytes,
		Error:         errStr,

		CaptureOutcome: captureOutcome,
		CaptureReason:  captureReason,
	}

	if errInfo != nil {
		ev.ErrorClass = errInfo.Class
		ev.ErrorType = errInfo.Type
		ev.ErrorCode = errInfo.Code
		ev.ErrorParam = errInfo.Param
		ev.ErrorMessage = errInfo.Message
		ev.ErrorMessageTruncated = errInfo.MessageTruncated
	}

	if usage != nil {
		ev.ModelReturned = usage.Model
		ev.InputTokens = usage.InputTokens
		ev.OutputTokens = usage.OutputTokens
		ev.ReasoningTokens = usage.ReasoningTokens
		ev.CachedTokens = usage.CachedTokens
		ev.TotalTokens = usage.TotalTokens
		ev.UsageRawJSON, ev.UsageRawTruncated = truncateUsageRawJSON(usage.UsageRawJSON)
		if ev.CaptureOutcome == "" {
			ev.CaptureOutcome = event.OutcomeCaptured
		}
	}

	if !p.writer.Enqueue(writer.StatsEvent{Event: ev}) {
		log.Printf("usage event dropped: request_id=%q endpoint=%q reason=%s", ev.ID, ev.Path, event.ReasonWriterQueueFull)
	}
}

func truncateUsageRawJSON(raw string) (string, bool) {
	const maxUsageRawJSONBytes = 4 * 1024
	if len(raw) <= maxUsageRawJSONBytes {
		return raw, false
	}
	return raw[:maxUsageRawJSONBytes], true
}

func (p *Proxy) writeError(w http.ResponseWriter, start time.Time, ttfb time.Duration, prof *profile.EndpointProfile, endpoint, method, clientIPHash, apiKeyHash, modelRequested string, requestBytes int64, status int, err error) {
	if status == 0 {
		status = http.StatusBadGateway
	}
	w.Header().Set("X-Metering-Proxy", "1")
	http.Error(w, "upstream error", status)
	log.Printf("upstream request error: endpoint=%q method=%q status=%d error=%v", endpoint, method, status, err)

	captureOutcome := event.OutcomeFailed
	errInfo := &extractor.ErrorInfo{
		Class:   "proxy_upstream_error",
		Message: "upstream request failed",
	}

	p.recordUsage(start, ttfb, prof, endpoint, method, "", status, false,
		clientIPHash, apiKeyHash, modelRequested, nil, requestBytes, 0, event.ReasonUpstreamError,
		captureOutcome, event.ReasonUpstreamError, errInfo)
}

// ---------- helpers ----------

func extractModel(body []byte) string {
	tok, ok := topLevelJSONToken(body, "model")
	if !ok {
		return ""
	}
	if model, ok := tok.(string); ok {
		return model
	}
	return ""
}

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
	tok, ok := topLevelJSONToken(body, "stream")
	if !ok {
		return false
	}
	stream, _ := tok.(bool)
	return stream
}

func topLevelJSONToken(body []byte, key string) (json.Token, bool) {
	if len(body) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, false
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		field, ok := keyTok.(string)
		if !ok {
			return nil, false
		}
		if field == key {
			valueTok, err := dec.Token()
			if err != nil {
				return nil, false
			}
			return valueTok, true
		}
		if err := skipJSONValue(dec); err != nil {
			return nil, false
		}
	}
	return nil, false
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	default:
		return nil
	}
}
