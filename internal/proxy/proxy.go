package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"ai-gateway-metering-proxy/internal/config"
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
	upstream          string
	hasher            *hash.Hasher
	writer            RecordWriter
	maxSample         int64
	meteringEnabled   atomic.Bool
	registry          *profile.Registry
	transport         *http.Transport
	reqMeta           config.RequestMetadataConfig
	correlationHeader string
}

func New(upstream string, hasher *hash.Hasher, rw RecordWriter, maxSample int64, reqMeta config.RequestMetadataConfig) *Proxy {
	p := &Proxy{
		upstream:  upstream,
		hasher:    hasher,
		writer:    rw,
		maxSample: maxSample,
		registry:  profile.NewRegistry(),
		transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true,
			ResponseHeaderTimeout: 60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
		},
		reqMeta:           reqMeta,
		correlationHeader: "X-Request-ID",
	}
	p.meteringEnabled.Store(true)
	return p
}

func (p *Proxy) SetMeteringEnabled(enabled bool) {
	p.meteringEnabled.Store(enabled)
	metrics.SetMeteringEnabled(enabled)
}

func (p *Proxy) Registry() *profile.Registry {
	return p.registry
}

func (p *Proxy) SetCorrelationHeader(header string) {
	header = strings.TrimSpace(header)
	if header == "" {
		header = "X-Request-ID"
	}
	p.correlationHeader = header
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	prof, err := p.registry.Match(r.Method, r.URL.Path)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	endpoint := r.URL.Path

	if !p.meteringEnabled.Load() || !prof.IsMetered() {
		p.forwardTransparent(w, r)
		return
	}

	var bodyPrefix []byte
	var streamProbePrefix []byte
	if r.Body != nil {
		initialBytes := p.reqMeta.InitialBytes
		if initialBytes <= 0 {
			initialBytes = 4096
		}
		bodyPrefix, _ = io.ReadAll(io.LimitReader(r.Body, initialBytes))
		streamProbePrefix = append(streamProbePrefix, bodyPrefix...)

		modelRequested := extractModel(bodyPrefix)
		if modelRequested != "" || !p.reqMeta.ExtendedModelScan || !prof.IsMetered() {
			// Model found or extended scan disabled or passthrough
		} else {
			maxBytes := p.reqMeta.MaxBytes
			if maxBytes <= initialBytes {
				maxBytes = 65536
			}
			for int64(len(bodyPrefix)) < maxBytes {
				remaining := maxBytes - int64(len(bodyPrefix))
				readSize := int64(4096)
				if remaining < readSize {
					readSize = remaining
				}
				chunk, readErr := io.ReadAll(io.LimitReader(r.Body, readSize))
				if len(chunk) > 0 {
					bodyPrefix = append(bodyPrefix, chunk...)
					if extractModel(bodyPrefix) != "" {
						break
					}
				}
				if readErr != nil || len(chunk) == 0 {
					break
				}
			}
		}
	}
	cr := &countingReader{r: &replayReader{prefix: bodyPrefix, src: r.Body}}
	r.Body = cr

	clientIP := clientIPFromRequest(r)
	clientIPHash := p.hasher.Hash(clientIP)
	apiKeyHash := p.hasher.Hash(apiKeyToken(r))

	modelRequested := extractModel(bodyPrefix)
	if modelRequested == "" {
		modelRequested = extractModelFromPath(r.URL.Path)
	}
	requestSuggestsStream := r.URL.Query().Get("stream") == "true" ||
		streamFromJSON(streamProbePrefix) ||
		isSSEMediaType(r.Header.Get("Accept")) ||
		streamFromPath(r.URL.Path)

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
	requestID := p.requestIDFromHeaders(resp.Header, r.Header)

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
		w.Header().Set("X-Metering-Proxy", "1")
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
		w.Header().Set("X-Metering-Proxy", "1")
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
			errStr = safeOperationalError(err)
		}
		captureOutcome := event.OutcomeSkipped
		captureReason := event.ReasonUsageNotPresent
		p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
			clientIPHash, apiKeyHash, modelRequested, nil, cr.bytesRead, written, errStr,
			captureOutcome, captureReason, nil, "", "", "", "")
		return
	}

	var lastUsage *extractor.UsageInfo
	var totalBytes int64
	var sseParseErrs int64

	var responsesState *extractor.ResponsesStreamState
	if prof != nil && prof.Name == "responses" {
		responsesState = extractor.NewResponsesStreamState()
	}

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
	streamErrInfo := func(result streamResult) *extractor.ErrorInfo {
		if result.errInfo != nil {
			return result.errInfo
		}
		return finalizeStreamErrInfo()
	}

	maxLine := 256 * 1024
	if prof != nil && prof.StreamProtocol.MaxLineSize > 0 {
		maxLine = prof.StreamProtocol.MaxLineSize
	}
	initCap := maxLine
	if initCap > 32*1024 {
		initCap = 32 * 1024
	}
	lineBuf := make([]byte, 0, initCap)
	lineOverflow := false
	buf := make([]byte, 32*1024)
	recordLineSkip := func() {
		metrics.AddSSELineSkips(1)
	}
	assembler := newSSEEventAssembler(maxLine)
	processSSEEvent := func(line []byte) {
		if errPayloads != nil {
			errPayloads.addStrippedPayload(stripSSEDataLine(line, prof))
		}
		if responsesState != nil {
			responsesState.ProcessSSEEvent(line)
		}
		u, err := p.tryExtractSSEUsage(line, prof)
		if err != nil {
			sseParseErrs++
		} else if u != nil {
			lastUsage = mergeUsageInfo(lastUsage, u)
		}
	}
	processPhysicalLine := func(line []byte) {
		events, skipped := assembler.addLine(line)
		if skipped {
			recordLineSkip()
		}
		for _, eventLine := range events {
			processSSEEvent(eventLine)
		}
	}
	flushPendingEvent := func() {
		if eventLine, skipped := assembler.flush(); skipped {
			recordLineSkip()
		} else if len(eventLine) > 0 {
			processSSEEvent(eventLine)
		}
	}

	for {
		select {
		case <-r.Context().Done():
			flushPendingEvent()
			errStr := ""
			if err := r.Context().Err(); err != nil {
				errStr = safeOperationalError(err)
			}
			result := p.finalizeStreamUsage(lastUsage, responsesState, prof, sseParseErrs)
			reportParseErrors()
			p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
				clientIPHash, apiKeyHash, modelRequested, result.usage, cr.bytesRead, totalBytes, errStr,
				result.captureOutcome, result.captureReason, streamErrInfo(result), result.modelReturned, result.modelReturnedSource, result.terminalEvent, result.terminalReason)
			return
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			totalBytes += int64(n)

			if _, werr := w.Write(buf[:n]); werr != nil {
				flushPendingEvent()
				result := p.finalizeStreamUsage(lastUsage, responsesState, prof, sseParseErrs)
				reportParseErrors()
				p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
					clientIPHash, apiKeyHash, modelRequested, result.usage, cr.bytesRead, totalBytes, safeOperationalError(werr),
					result.captureOutcome, result.captureReason, streamErrInfo(result), result.modelReturned, result.modelReturnedSource, result.terminalEvent, result.terminalReason)
				return
			}
			flusher.Flush()

			chunk := buf[:n]
			for len(chunk) > 0 {
				nl, lineBreakLen := nextLineBreak(chunk)
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
				chunk = chunk[nl+lineBreakLen:]

				processPhysicalLine(line)
			}
		}

		if readErr != nil {
			flushPendingEvent()
			errStr := ""
			if readErr != io.EOF {
				errStr = safeOperationalError(readErr)
			}
			result := p.finalizeStreamUsage(lastUsage, responsesState, prof, sseParseErrs)
			reportParseErrors()
			p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, true,
				clientIPHash, apiKeyHash, modelRequested, result.usage, cr.bytesRead, totalBytes, errStr,
				result.captureOutcome, result.captureReason, streamErrInfo(result), result.modelReturned, result.modelReturnedSource, result.terminalEvent, result.terminalReason)
			return
		}
	}
}

func nextLineBreak(chunk []byte) (idx, width int) {
	nl := bytes.IndexByte(chunk, '\n')
	cr := bytes.IndexByte(chunk, '\r')
	switch {
	case nl < 0 && cr < 0:
		return -1, 0
	case cr >= 0 && (nl < 0 || cr < nl):
		if cr+1 < len(chunk) && chunk[cr+1] == '\n' {
			return cr, 2
		}
		return cr, 1
	default:
		return nl, 1
	}
}

type sseEventAssembler struct {
	maxBytes int
	data     [][]byte
	size     int
	overflow bool
}

func newSSEEventAssembler(maxBytes int) *sseEventAssembler {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	return &sseEventAssembler{maxBytes: maxBytes}
}

func (a *sseEventAssembler) addLine(line []byte) ([][]byte, bool) {
	if len(line) == 0 {
		event, skipped := a.flush()
		if len(event) == 0 {
			return nil, skipped
		}
		return [][]byte{event}, skipped
	}
	trimmed := bytes.TrimLeft(line, " \t")
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil, false
	}
	if a.overflow {
		return nil, false
	}
	data := trimmed[5:]
	if len(data) > 0 && data[0] == ' ' {
		data = data[1:]
	}
	var events [][]byte
	if len(a.data) > 0 && a.pendingPayloadComplete() {
		event, skipped := a.flush()
		if skipped {
			return nil, true
		}
		if len(event) > 0 {
			events = append(events, event)
		}
	}
	a.size += len(data) + 1
	if a.size > a.maxBytes {
		a.data = nil
		a.size = 0
		a.overflow = true
		return events, true
	}
	copied := make([]byte, len(data))
	copy(copied, data)
	a.data = append(a.data, copied)
	return events, false
}

func (a *sseEventAssembler) flush() ([]byte, bool) {
	if a.overflow {
		a.overflow = false
		a.data = nil
		a.size = 0
		return nil, true
	}
	if len(a.data) == 0 {
		return nil, false
	}
	joinedSize := len("data: ")
	for _, part := range a.data {
		joinedSize += len(part)
	}
	joinedSize += len(a.data) - 1
	out := make([]byte, 0, joinedSize)
	out = append(out, "data: "...)
	for i, part := range a.data {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, part...)
	}
	a.data = nil
	a.size = 0
	return out, false
}

func (a *sseEventAssembler) pendingPayloadComplete() bool {
	if len(a.data) == 0 {
		return false
	}
	payload := a.pendingPayload()
	trimmed := bytes.TrimSpace(payload)
	if bytes.Equal(trimmed, []byte("[DONE]")) {
		return true
	}
	return json.Valid(trimmed)
}

func (a *sseEventAssembler) pendingPayload() []byte {
	if len(a.data) == 0 {
		return nil
	}
	if len(a.data) == 1 {
		return a.data[0]
	}
	size := len(a.data) - 1
	for _, part := range a.data {
		size += len(part)
	}
	out := make([]byte, 0, size)
	for i, part := range a.data {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, part...)
	}
	return out
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
	if s.locked {
		return
	}
	if len(s.payloads) >= 5 || s.totalBytes+len(payload) > 8*1024 {
		s.overflow = true
		return
	}
	pl := make([]byte, len(payload))
	copy(pl, payload)
	s.payloads = append(s.payloads, pl)
	s.totalBytes += len(pl)
}

func (s *errorPayloadSampler) finalize() []byte {
	s.locked = true
	if len(s.payloads) == 0 {
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
	if usage == nil {
		if captureOutcome == event.OutcomeCaptured && captureReason == "" {
			captureReason = event.ReasonUsageNotPresent
			captureOutcome = event.OutcomeSkipped
		} else if captureReason == event.ReasonSampleLimitExceeded {
			captureOutcome = event.OutcomeSkipped
		}
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
		errStr = safeOperationalError(copyErr)
	}

	modelReturnedSource := ""
	modelReturned := ""
	terminalEvent := ""
	if usage != nil && usage.Model != "" {
		modelReturned = usage.Model
		modelReturnedSource = event.SourceHTTPResponse
	}

	p.recordUsage(start, ttfb, prof, endpoint, r.Method, requestID, status, false,
		clientIPHash, apiKeyHash, modelRequested, usage, cr.bytesRead, written, errStr,
		captureOutcome, captureReason, errInfo, modelReturned, modelReturnedSource, terminalEvent, "")
}

// streamResult holds the outcome of finalizing a stream's usage extraction.
type streamResult struct {
	usage               *extractor.UsageInfo
	modelReturned       string
	modelReturnedSource string
	terminalEvent       string
	terminalReason      string
	captureOutcome      string
	captureReason       string
	errInfo             *extractor.ErrorInfo
}

func (p *Proxy) finalizeStreamUsage(lastUsage *extractor.UsageInfo, responsesState *extractor.ResponsesStreamState, prof *profile.EndpointProfile, sseParseErrs int64) streamResult {
	if responsesState != nil {
		res := responsesState.Result()
		usage := res.Usage
		modelReturnedSource := res.ModelReturnedSource
		terminalEvent := res.TerminalEvent
		terminalReason := res.TerminalReason
		captureOutcome := res.CaptureOutcome
		captureReason := res.CaptureReason

		if usage != nil && lastUsage != nil {
			usage = mergeUsageInfo(lastUsage, usage)
		} else if usage == nil && lastUsage != nil {
			usage = lastUsage
			if res.ModelReturned == "" && usage.Model != "" {
				if modelReturnedSource == "" {
					modelReturnedSource = event.SourceUsage
				}
			}
			if captureOutcome == "" || captureOutcome == event.OutcomeSkipped {
				if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0 {
					captureOutcome = event.OutcomeCaptured
					captureReason = ""
				}
			}
		}
		if usage != nil {
			if res.ModelReturned != "" {
				usage.Model = res.ModelReturned
			}
		}
		return streamResult{
			usage:               usage,
			modelReturned:       res.ModelReturned,
			modelReturnedSource: modelReturnedSource,
			terminalEvent:       terminalEvent,
			terminalReason:      terminalReason,
			captureOutcome:      captureOutcome,
			captureReason:       captureReason,
			errInfo:             res.ErrorInfo,
		}
	}

	captureOutcome := event.OutcomeCaptured
	captureReason := ""
	if lastUsage == nil {
		captureOutcome = event.OutcomeSkipped
		captureReason = event.ReasonUsageNotPresent
	}

	var terminalEvent string
	modelReturnedSource := ""
	if prof != nil {
		if lastUsage != nil && lastUsage.Model != "" {
			modelReturnedSource = event.SourceUsage
		}
	}
	if sseParseErrs > 0 && (lastUsage == nil || captureOutcome != event.OutcomeCaptured) {
		captureOutcome = event.OutcomeFailed
		captureReason = event.ReasonParseError
		terminalEvent = event.TerminalStreamError
	}

	return streamResult{
		usage:               lastUsage,
		modelReturned:       modelFromUsage(lastUsage),
		captureOutcome:      captureOutcome,
		captureReason:       captureReason,
		terminalEvent:       terminalEvent,
		modelReturnedSource: modelReturnedSource,
	}
}

// ---------- recording ----------

func (p *Proxy) recordUsage(start time.Time, ttfb time.Duration, prof *profile.EndpointProfile, endpoint, method, requestID string, status int, stream bool,
	clientIPHash, apiKeyHash, modelRequested string, usage *extractor.UsageInfo,
	requestBytes, responseBytes int64, errStr string,
	captureOutcome, captureReason string, errInfo *extractor.ErrorInfo,
	modelReturned, modelReturnedSource, terminalEvent, terminalReason string) {

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

		CaptureOutcome:      captureOutcome,
		CaptureReason:       captureReason,
		ModelReturned:       modelReturned,
		ModelReturnedSource: modelReturnedSource,
		TerminalEvent:       terminalEvent,
		TerminalReason:      terminalReason,
	}

	if errInfo != nil {
		ev.ErrorClass = errInfo.Class
		ev.ErrorType = errInfo.Type
		ev.ErrorCode = errInfo.Code
		ev.ErrorParam = errInfo.Param
	}

	if usage != nil {
		if usage.Model != "" {
			ev.ModelReturned = usage.Model
		}
		ev.InputTokens = usage.InputTokens
		ev.OutputTokens = usage.OutputTokens
		ev.ReasoningTokens = usage.ReasoningTokens
		ev.CachedTokens = usage.CachedTokens
		ev.CacheCreationTokens = usage.CacheCreationTokens
		ev.TotalTokens = usage.TotalTokens
		if ev.CaptureOutcome == "" {
			ev.CaptureOutcome = event.OutcomeCaptured
		}
		if ev.ModelReturnedSource == "" {
			ev.ModelReturnedSource = event.SourceUsage
		}
		ev.UsageSource = event.UsageSourceHTTPResponse
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
	end := maxUsageRawJSONBytes
	for end > 0 && !utf8.RuneStart(raw[end]) {
		end--
	}
	if end == 0 {
		end = maxUsageRawJSONBytes
	}
	return raw[:end], true
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
		clientIPHash, apiKeyHash, modelRequested, nil, requestBytes, 0, safeOperationalError(err),
		captureOutcome, event.ReasonUpstreamError, errInfo, "", "", "", "")
}

const maxErrorStringLen = 500

func truncateErrorString(err string) string {
	if len(err) <= maxErrorStringLen {
		return err
	}
	end := maxErrorStringLen
	for end > 0 && !utf8.RuneStart(err[end]) {
		end--
	}
	if end == 0 {
		end = maxErrorStringLen
	}
	return err[:end]
}

func safeOperationalError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return "timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_error"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "connection reset"):
		return "connection_reset"
	case strings.Contains(msg, "no such host"):
		return "dns_error"
	case strings.Contains(msg, "no route to host"):
		return "no_route"
	case strings.Contains(msg, "network is unreachable"):
		return "network_unreachable"
	case strings.Contains(msg, "tls"):
		return "tls_error"
	case strings.Contains(msg, "use of closed network connection"):
		return "connection_closed"
	case strings.Contains(msg, "broken pipe"):
		return "client_write_error"
	default:
		return "upstream_error"
	}
}

// ---------- helpers ----------

func extractModel(body []byte) string {
	if model, ok := topLevelJSONString(body, "model"); ok {
		return model
	}
	return ""
}

func extractModelFromPath(path string) string {
	const marker = "/models/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return ""
	}
	model := path[idx+len(marker):]
	if colon := strings.IndexByte(model, ':'); colon >= 0 {
		model = model[:colon]
	}
	return model
}

func streamFromPath(path string) bool {
	return strings.HasSuffix(path, ":streamGenerateContent")
}

func modelFromUsage(usage *extractor.UsageInfo) string {
	if usage == nil {
		return ""
	}
	return usage.Model
}

func mergeUsageInfo(current, next *extractor.UsageInfo) *extractor.UsageInfo {
	if next == nil {
		return current
	}
	if current == nil {
		clone := *next
		return &clone
	}

	merged := *current
	if next.Model != "" {
		merged.Model = next.Model
	}
	if next.InputTokens != 0 {
		merged.InputTokens = next.InputTokens
	}
	if next.OutputTokens != 0 {
		merged.OutputTokens = next.OutputTokens
	}
	if next.ReasoningTokens != 0 {
		merged.ReasoningTokens = next.ReasoningTokens
	}
	if next.CachedTokens != 0 {
		merged.CachedTokens = next.CachedTokens
	}
	if next.CacheCreationTokens != 0 {
		merged.CacheCreationTokens = next.CacheCreationTokens
	}
	if next.TotalTokens != 0 {
		if next.InputTokens == 0 && next.OutputTokens != 0 && current.InputTokens != 0 && next.TotalTokens == next.OutputTokens {
			merged.TotalTokens = merged.InputTokens + next.OutputTokens
		} else {
			merged.TotalTokens = next.TotalTokens
		}
	} else if next.InputTokens != 0 || next.OutputTokens != 0 || next.ReasoningTokens != 0 || next.CachedTokens != 0 {
		// For Anthropic streaming, InputTokens includes cache creation and cache
		// read tokens, so InputTokens + OutputTokens gives the correct total.
		merged.TotalTokens = merged.InputTokens + merged.OutputTokens
	}
	if next.UsageRawJSON != "" {
		merged.UsageRawJSON = next.UsageRawJSON
	}
	return &merged
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
	return ""
}

func clientIPFromRequest(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return normalizeIP(strings.TrimSpace(strings.Split(fwd, ",")[0]))
	}
	return normalizeIP(r.RemoteAddr)
}

func normalizeIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip.String()
	}
	return value
}

// apiKeyToken extracts the API key from request headers or query parameters.
// The key is later hashed for metering storage; it is never stored or logged in plaintext.
// For Gemini, the ?key= query parameter is intentionally forwarded to the upstream
// per invariant #3 (preserve request transparency).
func apiKeyToken(r *http.Request) string {
	if token := bearerToken(r.Header.Get("Authorization")); token != "" {
		return token
	}
	for _, name := range []string{"X-API-Key", "X-Goog-API-Key"} {
		if token := strings.TrimSpace(r.Header.Get(name)); token != "" {
			return token
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("key"))
}

func (p *Proxy) requestIDFromHeaders(respHeader, reqHeader http.Header) string {
	clientHeaders := dedupeStrings([]string{p.correlationHeader, "X-Request-ID", "Request-ID"})
	for _, name := range clientHeaders {
		if value := reqHeader.Get(name); value != "" {
			return value
		}
	}
	for _, name := range []string{"OpenAI-Request-ID", "X-Request-ID", "Request-ID"} {
		if value := respHeader.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func streamFromJSON(body []byte) bool {
	stream, ok := topLevelJSONBool(body, "stream")
	return ok && stream
}

func topLevelJSONString(body []byte, key string) (string, bool) {
	valueStart, ok := findTopLevelJSONValue(body, key)
	if !ok {
		return "", false
	}
	end, ok := scanJSONStringEnd(body, valueStart)
	if !ok {
		return "", false
	}
	raw := body[valueStart:end]
	if !bytes.ContainsAny(raw, `\`) {
		return string(raw[1 : len(raw)-1]), true
	}
	var out strings.Builder
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			out.WriteByte(raw[i])
			continue
		}
		i++
		if i >= len(raw)-1 {
			return "", false
		}
		switch raw[i] {
		case '"', '\\', '/':
			out.WriteByte(raw[i])
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		default:
			return "", false
		}
	}
	return out.String(), true
}

func topLevelJSONBool(body []byte, key string) (bool, bool) {
	valueStart, ok := findTopLevelJSONValue(body, key)
	if !ok {
		return false, false
	}
	switch {
	case hasJSONLiteralAt(body, valueStart, "true"):
		return true, true
	case hasJSONLiteralAt(body, valueStart, "false"):
		return false, true
	default:
		return false, false
	}
}

func findTopLevelJSONValue(body []byte, key string) (int, bool) {
	i := skipJSONWhitespace(body, 0)
	if i >= len(body) || body[i] != '{' {
		return 0, false
	}
	i++
	for {
		i = skipJSONWhitespace(body, i)
		if i >= len(body) {
			return 0, false
		}
		if body[i] == '}' {
			return 0, false
		}
		keyEnd, ok := scanJSONStringEnd(body, i)
		if !ok {
			return 0, false
		}
		field := body[i+1 : keyEnd-1]
		i = skipJSONWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return 0, false
		}
		i = skipJSONWhitespace(body, i+1)
		if string(field) == key {
			return i, true
		}
		next, ok := skipJSONValueBytes(body, i)
		if !ok {
			return 0, false
		}
		i = skipJSONWhitespace(body, next)
		if i >= len(body) {
			return 0, false
		}
		if body[i] == ',' {
			i++
			continue
		}
		if body[i] == '}' {
			return 0, false
		}
		return 0, false
	}
}

func skipJSONWhitespace(body []byte, i int) int {
	for i < len(body) {
		switch body[i] {
		case ' ', '\n', '\r', '\t':
			i++
		default:
			return i
		}
	}
	return i
}

func scanJSONStringEnd(body []byte, i int) (int, bool) {
	if i >= len(body) || body[i] != '"' {
		return 0, false
	}
	escaped := false
	for i++; i < len(body); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch body[i] {
		case '\\':
			escaped = true
		case '"':
			return i + 1, true
		}
	}
	return 0, false
}

func skipJSONValueBytes(body []byte, i int) (int, bool) {
	i = skipJSONWhitespace(body, i)
	if i >= len(body) {
		return 0, false
	}
	switch body[i] {
	case '"':
		return scanJSONStringEnd(body, i)
	case '{':
		return skipJSONComposite(body, i, '{', '}')
	case '[':
		return skipJSONComposite(body, i, '[', ']')
	default:
		for i < len(body) {
			switch body[i] {
			case ',', '}', ']', ' ', '\n', '\r', '\t':
				return i, true
			default:
				i++
			}
		}
		return i, true
	}
}

func skipJSONComposite(body []byte, i int, open, close byte) (int, bool) {
	depth := 0
	for i < len(body) {
		switch body[i] {
		case '"':
			end, ok := scanJSONStringEnd(body, i)
			if !ok {
				return 0, false
			}
			i = end
			continue
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case '{':
			if open != '{' {
				end, ok := skipJSONComposite(body, i, '{', '}')
				if !ok {
					return 0, false
				}
				i = end
				continue
			}
		case '[':
			if open != '[' {
				end, ok := skipJSONComposite(body, i, '[', ']')
				if !ok {
					return 0, false
				}
				i = end
				continue
			}
		}
		i++
	}
	return 0, false
}

func hasJSONLiteralAt(body []byte, i int, literal string) bool {
	if i+len(literal) > len(body) || string(body[i:i+len(literal)]) != literal {
		return false
	}
	end := i + len(literal)
	if end == len(body) {
		return true
	}
	switch body[end] {
	case ',', '}', ']', ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}
