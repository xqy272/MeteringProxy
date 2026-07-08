# Performance pprof findings

Status: Phase 1 investigation only. No production or benchmark code changes.

Date: 2026-07-08
Repo revision: 259aaaf
Host: Windows amd64, Go 1.26.2, 13th Gen Intel(R) Core(TM) i5-13600KF

## Commands

Profiles were written outside the repo under:

```text
C:\Users\QingYang\AppData\Local\Temp\meteringproxy-pprof-a87781d73e784165805ad7121105ccc7
```

Project-local Go caches were set per `CLAUDE.md`:

```powershell
$env:GOCACHE='c:\Users\QingYang\Desktop\MeteringProxy\.gocache'
$env:GOMODCACHE='c:\Users\QingYang\Desktop\MeteringProxy\.gomodcache'
```

Profile generation:

```powershell
go test -run '^$' -bench 'BenchmarkProxyBaseline/benchmark-sse-large' -benchmem -memprofile "$pprofDir\sse-large.memprof" -benchtime=3s ./internal/proxy/
go test -run '^$' -bench 'BenchmarkProxyBaseline/benchmark-nonstream-large' -benchmem -memprofile "$pprofDir\nonstream-large.memprof" -benchtime=3s ./internal/proxy/
```

Analysis:

```powershell
go tool pprof -top -alloc_space sse-large.memprof
go tool pprof -top -alloc_objects sse-large.memprof
go tool pprof -top -alloc_space nonstream-large.memprof
go tool pprof -top -alloc_objects nonstream-large.memprof
go tool pprof -list <funcname> -alloc_space *.memprof
go tool pprof -tree -alloc_space *.memprof
```

Run results:

- `benchmark-sse-large`: `74.04 ms/op`, `44,026,687 B/op`, `110,098 allocs/op`
- `benchmark-nonstream-large`: `5.49 ms/op`, `20,099,303 B/op`, `380 allocs/op`

## Classification

- Proxy logic: `internal/proxy`, `internal/extractor`, `internal/writer`, and related project runtime code.
- Benchmark harness: `internal/proxy/benchmark_test.go` payload construction, synthetic response objects, and benchmark-only writers/readers.
- Stdlib/runtime: `encoding/json`, `net/http`, `testing`, `runtime`, `io`, `sync`, crypto, and internal standard library helpers.

## SSE large

Dominant call paths:

- `BenchmarkProxyBaseline -> benchmarkProxySequential -> Proxy.ServeHTTP -> Proxy.handleStream -> handleStream.func7 -> handleStream.func6 -> tryExtractSSEUsage -> ExtractChatUsage`
- `BenchmarkProxyBaseline -> benchmarkProxySequential -> Proxy.ServeHTTP -> Proxy.handleStream -> handleStream.func7 -> sseEventAssembler.addLine -> sseEventAssembler.flush`

### alloc_space top 10

| Rank | Flat | Cum | Allocation point | Category | Notes |
|---:|---:|---:|---|---|---|
| 1 | 3030.39 MB, 47.31% | 3321.43 MB, 51.86% | `extractor.ExtractChatUsage` | Proxy logic | `extractor.go:37` converts `[]byte` to `string`; `extractor.go:46` converts back and unmarshals JSON for each SSE event. |
| 2 | 1546.44 MB, 24.14% | 3024.39 MB, 47.22% | `proxy.(*sseEventAssembler).addLine` | Proxy logic | `proxy.go:689` copies each `data:` payload. |
| 3 | 1477.94 MB, 23.07% | 1477.94 MB, 23.07% | `proxy.(*sseEventAssembler).flush` | Proxy logic | `proxy.go:710` allocates another joined event buffer. |
| 4 | 219.03 MB, 3.42% | 289.53 MB, 4.52% | `encoding/json.Unmarshal` | Stdlib via proxy extractor | JSON cost triggered by `ExtractChatUsage`. |
| 5 | 70.50 MB, 1.10% | 70.50 MB, 1.10% | `encoding/json.(*scanner).pushParseState` | Stdlib via proxy extractor | JSON scanner object churn. |
| 6 | 34.67 MB, 0.54% | 34.67 MB, 0.54% | `internal/bytealg.MakeNoZero` | Benchmark harness | Called from benchmark payload setup, mostly `makeSizedBytes`. |
| 7 | 9.32 MB, 0.15% | 23.99 MB, 0.37% | `proxy.makeSizedBytes` | Benchmark harness | Pre-builds synthetic SSE chunks before timer reset; visible in profile but not hot path. |
| 8 | 6.71 MB, 0.10% | 6352.52 MB, 99.18% | `proxy.(*Proxy).handleStream` | Proxy logic | Small direct allocations; cumulative points to extractor/assembler children. |
| 9 | 4.01 MB, 0.063% | 4.01 MB, 0.063% | `runtime.mallocgc` | Runtime | Low direct share. |
| 10 | 2.01 MB, 0.031% | 2.01 MB, 0.031% | `sync.(*Pool).pinSlow` | Stdlib/runtime | Low direct share. |

### alloc_objects top 10

| Rank | Flat objects | Cum objects | Allocation point | Category | Notes |
|---:|---:|---:|---|---|---|
| 1 | 4,612,733, 29.69% | 9,403,197, 60.51% | `extractor.ExtractChatUsage` | Proxy logic | Per-event string conversion and JSON decode path. |
| 2 | 4,548,956, 29.28% | 6,062,368, 39.01% | `proxy.(*sseEventAssembler).addLine` | Proxy logic | Per-event payload copy at `proxy.go:689`. |
| 3 | 3,194,950, 20.56% | 3,194,950, 20.56% | `encoding/json.(*scanner).pushParseState` | Stdlib via proxy extractor | Caused by per-event JSON unmarshal. |
| 4 | 1,594,928, 10.26% | 4,789,878, 30.83% | `encoding/json.Unmarshal` | Stdlib via proxy extractor | Triggered by `ExtractChatUsage`. |
| 5 | 1,513,412, 9.74% | 1,513,412, 9.74% | `proxy.(*sseEventAssembler).flush` | Proxy logic | Per-event joined buffer allocation at `proxy.go:710`. |
| 6 | 32,768, 0.21% | 32,768, 0.21% | `regexp.mergeRuneSets.func2` | Stdlib | Low share. |
| 7 | 16,704, 0.11% | 16,704, 0.11% | `runtime.mallocgc` | Runtime | Low share. |
| 8 | 13,840, 0.089% | 13,840, 0.089% | `internal/bytealg.MakeNoZero` | Benchmark harness | Synthetic payload construction. |
| 9 | 7,177, 0.046% | 21,015, 0.14% | `proxy.makeSizedBytes` | Benchmark harness | Synthetic payload construction. |
| 10 | 1,489, 0.0096% | 1,489, 0.0096% | `net/textproto.MIMEHeader.Add` | Stdlib/proxy header copy | Negligible. |

### SSE large optimization targets

Worth optimizing:

- `internal/extractor/extractor.go:37` and `:46`: add a cheap byte-level prefilter before `string(data)` and `json.Unmarshal`. For chat-completions SSE, most chunks have no `usage`; returning early when the event payload lacks `"usage"` should avoid parsing 9999 of 10000 chunks in this benchmark. Keep this in the extractor path, after bytes have already been forwarded.
- `internal/extractor/extractor.go:37`: replace string-based `stripSSEPrefix(string(data))` with byte-based trimming to avoid `[]byte -> string -> []byte` churn.
- `internal/proxy/proxy.go:689` and `:710`: reduce `sseEventAssembler` copies. For the common single-line `data:` event, process a slice view directly and reserve allocations for multi-line, cross-chunk, or overflow cases.

Not worth optimizing now:

- `makeSizedBytes` / `makeSSEChunks`: benchmark harness, less than 0.4% alloc_space.
- Header copy and runtime/std helpers below 0.1%: low leverage.

## nonstream large

Important correction: this profile is not purely response-side. The largest allocation is request-side `requestBodyProbe` work triggered while the synthetic upstream reads the 1MB request body. It is not the pre-`RoundTrip` 4KB prefix scan measured in Phase 0.

Dominant call paths:

- Request side: `BenchmarkProxyBaseline -> benchmarkProxySequential -> roundTripFunc.RoundTrip -> io.Copy(io.Discard, req.Body) -> countingReader.Read -> requestBodyProbe.Write -> prefixTailBuffer.Write`
- Response sampler: `BenchmarkProxyBaseline -> benchmarkProxySequential -> Proxy.ServeHTTP -> Proxy.handleNonStream -> io.Copy -> io.TeeReader -> limitedBuffer.Write`
- Response extraction: `BenchmarkProxyBaseline -> benchmarkProxySequential -> Proxy.ServeHTTP -> Proxy.handleNonStream -> profile extractor -> ExtractNonStreaming -> tryChatFormat -> decodeJSON -> encoding/json.Decoder.refill`

### alloc_space top 10

| Rank | Flat | Cum | Allocation point | Category | Notes |
|---:|---:|---:|---|---|---|
| 1 | 8896.71 MB, 51.31% | 8896.71 MB, 51.31% | `proxy.(*prefixTailBuffer).Write` | Proxy logic, request side | `proxy.go:892` appends full chunks to tail; `proxy.go:894` copies the last half into a fresh slice repeatedly. |
| 2 | 4623.61 MB, 26.66% | 4623.61 MB, 26.66% | `proxy.(*limitedBuffer).Write` | Proxy logic, response side | `proxy.go:860` grows the nonstream response sample buffer while `io.Copy` forwards. |
| 3 | 3640.15 MB, 20.99% | 3640.15 MB, 20.99% | `encoding/json.(*Decoder).refill` | Stdlib via proxy extractor | Triggered by `extractor.decodeJSON` at `extractor.go:565`, parsing the 1MB sampled response. |
| 4 | 64.32 MB, 0.37% | 64.32 MB, 0.37% | `proxy.(*prefixTailBuffer).Bytes` | Proxy logic, request side | `proxy.go:919` builds prefix+tail sample for request metadata finalization. |
| 5 | 34.83 MB, 0.20% | 34.83 MB, 0.20% | `internal/bytealg.MakeNoZero` | Benchmark harness/std helper | Mostly synthetic payload setup. |
| 6 | 34.04 MB, 0.20% | 13559.90 MB, 78.20% | `io.copyBuffer` | Stdlib coordinator | Cumulative points to proxy sampler writes; not a direct optimization target. |
| 7 | 14.32 MB, 0.083% | 29.15 MB, 0.17% | `proxy.makeSizedBytes` | Benchmark harness | Synthetic payload construction. |
| 8 | 10.52 MB, 0.061% | 10.52 MB, 0.061% | `io.ReadAll` | Stdlib/harness path | Low share. |
| 9 | 4.02 MB, 0.023% | 17286.41 MB, 99.69% | `proxy.(*Proxy).ServeHTTP` | Proxy logic | Small direct allocations; cumulative root. |
| 10 | 3.53 MB, 0.020% | 3.53 MB, 0.020% | `io.init.func1` | Stdlib | Low share. |

### alloc_objects top 10

| Rank | Flat objects | Cum objects | Allocation point | Category | Notes |
|---:|---:|---:|---|---|---|
| 1 | 231,504, 52.28% | 231,504, 52.28% | `proxy.(*prefixTailBuffer).Write` | Proxy logic, request side | Repeated tail slice allocation. |
| 2 | 47,100, 10.64% | 47,100, 10.64% | `runtime.mallocgc` | Runtime | General allocator samples. |
| 3 | 32,768, 7.40% | 32,768, 7.40% | `encoding/json.(*scanner).pushParseState` | Stdlib via proxy extractor | JSON decode state churn. |
| 4 | 21,846, 4.93% | 21,846, 4.93% | `testing.(*B).ResetTimer` | Benchmark harness/testing | Not production. |
| 5 | 13,742, 3.10% | 13,742, 3.10% | `encoding/json.(*Decoder).refill` | Stdlib via proxy extractor | Response sample JSON decode. |
| 6 | 12,816, 2.89% | 12,816, 2.89% | `internal/bytealg.MakeNoZero` | Benchmark harness/std helper | Synthetic payload setup. |
| 7 | 12,302, 2.78% | 25,116, 5.67% | `proxy.makeSizedBytes` | Benchmark harness | Synthetic payload construction. |
| 8 | 11,482, 2.59% | 11,482, 2.59% | `proxy.(*limitedBuffer).Write` | Proxy logic, response side | Response sample buffer growth. |
| 9 | 10,923, 2.47% | 88,225, 19.93% | `proxy.(*Proxy).handleNonStream` | Proxy logic | Cumulative includes extractor and sampler children. |
| 10 | 9,904, 2.24% | 9,904, 2.24% | `io.ReadAll` | Stdlib/harness path | Low share. |

### nonstream-large optimization targets

Worth optimizing:

- `internal/proxy/proxy.go:892` and `:894`: `prefixTailBuffer.Write` is the largest allocation source, but it is request-side observer work, not response-side. If optimizing total nonstream-large allocations, replace append-and-copy tail maintenance with a bounded ring buffer or fixed-cap tail buffer. This should be treated as separate from the pre-`RoundTrip` prefix-scan discussion.
- `internal/proxy/proxy.go:860`: `limitedBuffer.Write` is the primary response-side allocation. Consider pre-sizing response sampling based on `resp.ContentLength` when available, or switching to a sampler that stops retaining bytes once enough usage/error information has been captured.
- `internal/proxy/proxy.go:1022` plus `internal/extractor/extractor.go:337` and `:565`: response extraction decodes the full sampled 1MB JSON. A top-level usage/model extractor that scans only the needed fields, or a decoder path that avoids loading irrelevant padding, would address the 20.99% `encoding/json.Decoder.refill` share.

Not worth optimizing now:

- `benchmarkResponse`, `makeSizedBytes`, `makeSSEChunks`: benchmark harness and small compared with proxy logic.
- `testing.(*B).ResetTimer`: benchmark-only object samples.
- `net/http.NewRequestWithContext`, `MIMEHeader.Add`, hashing, and runtime helpers below 1%: not the current bottleneck.

## Recommended next steps

1. Start with SSE because the profile is cleanly response-side and the largest two causes are local to proxy/extractor code.
   - Target `internal/extractor/extractor.go:37` and `:46` with a byte-level `"usage"` prefilter and byte-based SSE prefix stripping.
   - Target `internal/proxy/proxy.go:689` and `:710` by avoiding copies for complete single-line SSE events.

2. For nonstream response-side work, target response sampling and JSON extraction.
   - Target `internal/proxy/proxy.go:860` with a lower-allocation sampler.
   - Target `internal/extractor/extractor.go:565` by avoiding full response JSON decode when only `model` and `usage` are needed.

3. Track the request-side `prefixTailBuffer` issue separately.
   - It is high impact in nonstream-large, but it is not the Phase 0 pre-`RoundTrip` 4KB prefix cost and not response-side.
   - Target `internal/proxy/proxy.go:892` and `:894` with a ring/fixed tail buffer only after deciding that request observer allocations belong in the next optimization slice.

All suggested changes must preserve transparent forwarding: bytes are already written before parsing in streaming mode, and response/header transparency must remain unchanged.
