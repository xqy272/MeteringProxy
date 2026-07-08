# MeteringProxy performance baseline

Status: Phase 0 baseline, no production behavior changes.

Date: 2026-07-08
Repo revision: b3197d8 plus uncommitted benchmark/doc files
Host: Windows amd64, Go 1.26.2, 13th Gen Intel(R) Core(TM) i5-13600KF

## Command

Project-local Go caches were set per `CLAUDE.md`:

```powershell
$env:GOCACHE='c:\Users\QingYang\Desktop\MeteringProxy\.gocache'
$env:GOMODCACHE='c:\Users\QingYang\Desktop\MeteringProxy\.gomodcache'
go test -bench=. -benchmem ./internal/proxy/
```

Validation also passed:

```powershell
go test ./...
go vet ./...
```

## Benchmark scope

The benchmark suite lives in `internal/proxy/benchmark_test.go` and uses in-process `roundTripFunc` upstreams. It does not call real CLIProxyAPI.

Covered baseline scenarios:

- `benchmark-nonstream-small`: request 4KB, response 8KB
- `benchmark-nonstream-large`: request 1MB, response 1MB
- `benchmark-sse-small`: 100 SSE chunks, 200 bytes each
- `benchmark-sse-large`: 10000 SSE chunks, 1KB each
- `benchmark-request-only-large`: request-only profile, request 10MB, response 10MB
- `benchmark-concurrent`: 50 concurrent non-streaming requests

Additional isolation benchmark:

- `BenchmarkProxyPreRoundTripOverhead`: compares request-only no-prefix, default 4KB usage-metered prefix, and 64KB extended scan before upstream `RoundTrip`.

## Results

`pre_roundtrip_*` measures from immediately before `Proxy.ServeHTTP` to entry of the upstream `RoundTrip` function. On this Windows host, very short intervals can quantize to `0ns`; use averages and p99 for regression comparison when p50/p95 are zero.

| Benchmark | ns/op | B/op | allocs/op | pre-RT avg | pre-RT p50/p95/p99 | SSE chunk avg/max |
|---|---:|---:|---:|---:|---:|---:|
| nonstream small | 37,207 | 100,298 | 102 | 7,414 ns | 0 / 0 / 0 ns | n/a |
| nonstream large | 5,498,442 | 20,099,696 | 380 | 27,638 ns | 0 / 0 / 1,502,300 ns | n/a |
| SSE small | 209,005 | 203,584 | 1,191 | 7,937 ns | 0 / 0 / 0 ns | 2.675 / 1,510,500 ns |
| SSE large | 75,851,100 | 44,025,509 | 110,098 | 0 ns | 0 / 0 / 0 ns | 3.654 / 584,600 ns |
| request-only large | 433,101 | 3,995 | 53 | 9,517 ns | 0 / 0 / 502,800 ns | n/a |
| concurrent, 50 workers | 43,106 | 102,186 | 105 | 663,663 ns | 0 / 4,599,300 / 10,446,100 ns | n/a |

Prefix isolation:

| Benchmark | ns/op | B/op | allocs/op | body reads at RoundTrip | pre-RT avg | pre-RT p50/p95/p99 |
|---|---:|---:|---:|---:|---:|---:|
| request-only 10MB, no prefix | 5,233 | 3,984 | 55 | 0 | 2,954 ns | 0 / 0 / 0 ns |
| usage-metered 10MB, default 4KB prefix | 21,417 | 54,160 | 92 | 7 | 12,097 ns | 0 / 0 / 517,500 ns |
| usage-metered 10MB, extended 64KB scan | 801,557 | 458,352 | 247 | 112 | 790,001 ns | 1,000,200 / 1,509,900 / 2,009,900 ns |

`body reads at RoundTrip` is the `countingTestReadCloser` read-call count observed when upstream `RoundTrip` starts. It is a call-count diagnostic, not bytes.

## Observations

- The request-only fast path reaches upstream `RoundTrip` with zero request body reads, matching the intended `forwardRequestOnly` behavior.
- Default usage-metered prefix sampling adds measurable pre-`RoundTrip` work versus request-only, but the isolated 4KB cost is small on this host: about 12 us average pre-RT versus about 3 us for request-only.
- The 64KB extended scan is the clearest request-body prefix bottleneck in this baseline: about 790 us average pre-RT, 458KB/op, and 247 allocs/op. It remains disabled by default.
- Large non-streaming response handling allocates about 20MB/op for the 1MB/1MB scenario. This points at response sampling/extraction cost rather than only request-prefix overhead.
- Large SSE handling allocates about 44MB/op and 110k allocs/op for 10000 chunks. The benchmark forwards each chunk before parsing, but event assembly/parsing allocation is visible.
- The 50-worker concurrent benchmark reports high pre-RT p95/p99 because it includes local scheduler contention and benchmark context instrumentation. Treat it as a regression baseline, not a production latency SLO.

No fixes are applied in this phase.
