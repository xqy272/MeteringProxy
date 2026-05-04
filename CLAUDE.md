# Agent Notes

This file is for coding agents working in this repository. User-facing setup, deployment, backup, rollback, and operations documentation belongs in `README.md`.

## Project Summary

MeteringProxy is a transparent HTTP metering proxy for AI gateway traffic. It forwards LLM API requests to CLIProxyAPI while asynchronously recording usage statistics in SQLite for a read-only WebUI.

Primary traffic path:

```text
Client -> Caddy -> MeteringProxy -> CLIProxyAPI -> upstream provider
```

Only the metered API paths should be handled by this proxy in production:

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `POST /v1beta/models/{model}:generateContent`
- `POST /v1beta/models/{model}:streamGenerateContent`
- `POST /v1/models/{model}:generateContent`
- `POST /v1/models/{model}:streamGenerateContent`

## Non-Negotiable Invariants

1. LLM request forwarding is more important than metering completeness.
2. Do not store prompt bodies, response bodies, plaintext API keys, or plaintext client IPs.
3. Preserve request and response transparency: method, path, query, headers, body bytes, status code, and SSE bytes.
4. Do not block client traffic on DB writes, cost calculation, dashboard queries, or parsing.
5. Queue overflow must drop metering events rather than slow or fail LLM traffic.
6. Database migrations must be additive and compatible with older SQLite files.
7. Keep the entire hashing scheme stable across deployments. This means:
   - The salt file bytes must be identical (back it up with the SQLite DB).
   - The hash algorithm is HMAC-SHA256; do not change it.
   - The output format is 64-char lowercase hex; do not add prefixes or alter encoding.
   - The 2026-05 switch from SHA256(salt+value) to HMAC-SHA256 was a one-time
     migration. Future algorithm changes REQUIRE a dual-read/write path in the
     DB layer — direct replacement breaks all historical API-key and client-IP
     grouping and is forbidden.
   - If salt is lost or regenerated, historical groupings are irrecoverable.
     Deployment scripts must refuse to start if the DB has data but the salt
     file is missing or has changed.

## Code Map

```text
main.go                 wiring, HTTP server, shutdown, health reporter
internal/config         YAML config defaults and validation
internal/db             SQLite schema, migrations, queries
internal/extractor      usage extraction for OpenAI, Anthropic, and Gemini APIs
internal/hash           HMAC-SHA256 hashing with stable salt bytes
internal/pricing        estimated cost calculation
internal/proxy          reverse proxy, SSE handling, usage capture
internal/webui          dashboard API and embedded static UI
internal/writer         async batch writer and counters
```

## Data Model Notes

- `latency_ms` is total request lifecycle duration.
- `ttfb_ms` is time until upstream response headers are received.
- `created_at_unix` and `timestamp_unix` are used for range queries; text timestamps remain for display and compatibility.
- Empty model and key hash values are grouped as `unknown` in aggregate queries.
- `request_usage` is append-only for normal operation.
- `health_metrics` stores periodic snapshots of process-lifetime counters.

## Extractor Notes

- Chat Completions usage uses `prompt_tokens`, `completion_tokens`, and `total_tokens`.
- Responses API usage uses `input_tokens`, `output_tokens`, and `total_tokens`.
- Anthropic Messages usage uses `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`. These four fields are mutually exclusive billing dimensions — the extractor sums them for total input tokens; this is NOT double-counting.
- Gemini generateContent usage uses `usageMetadata` token counts.
- Reasoning and cached token fields are detail fields and may be absent.
- SSE parsing is best-effort and must never alter forwarded bytes.
- Error extraction (`error.go`) classifies upstream error responses but intentionally does NOT persist provider error messages to the database. Provider error bodies may contain user content or prompt fragments; storing them would violate invariant #2. Only `error_class`, `error_type`, `error_code`, and `error_param` are stored. The `error_message` DB column is reserved for future system-generated diagnostic messages.

## Pricing Notes

Reasoning tokens are treated as a subset of output tokens.

- If `reasoning_per_1m` is set: charge regular output as `output_tokens - reasoning_tokens`, clamped at zero, plus reasoning at `reasoning_per_1m`.
- If `reasoning_per_1m` is not set: charge all `output_tokens` at `output_per_1m` and do not add reasoning again.
- Anthropic cache creation tokens use `cache_creation_per_1m` when configured, otherwise they fall back to regular input pricing.

## Test Commands

Use project-local Go caches when running locally on Windows:

```powershell
$env:GOCACHE='c:\Users\QingYang\Desktop\MeteringProxy\.gocache'
$env:GOMODCACHE='c:\Users\QingYang\Desktop\MeteringProxy\.gomodcache'
go test ./...
go vet ./...
go build -o ai-gateway-metering-proxy.exe .
```

## Local WebUI Development

Two flags support local frontend iteration without Docker redeploy:

```bash
# First run: start with demo data
go run . --config config.dev.yaml --dev-static --seed-demo

# Subsequent runs (DB already has data):
go run . --config config.dev.yaml --dev-static
```

- `--dev-static` serves static files from `internal/webui/static/` on disk instead of the embedded FS. Edit and refresh — no rebuild needed.
- `--seed-demo` inserts ~220 realistic records into the database. Requires `--dev-static` and a `*.dev.sqlite` database path (refuses absolute paths and non-dev filenames).
- Both flags default to `false`. Production behavior is unchanged when they are omitted.
- `config.dev.yaml` points to `127.0.0.1:8320`, `usage.dev.sqlite`, and local `salt`/`pricing.yaml` files. It is gitignored.

For shell script edits:

```bash
bash -n scripts/backup.sh
```

## Editing Guidance

- Keep production behavior conservative and transparent.
- Prefer focused tests around changed behavior.
- Do not replace existing migrations with destructive schema rebuilds.
- Do not add request logging by default if it can expose usage patterns or increase load.
- Keep deployment and operator documentation in `README.md`, not here.

## Known Design Tradeoffs

- **Accept-Encoding / compressed SSE:** The proxy does NOT strip `Accept-Encoding` from forwarded client headers (invariant #3 — header transparency). If a client sends `Accept-Encoding: gzip` and the upstream honors it for an SSE stream, metering may silently skip that stream because the proxy cannot parse compressed bytes. This is intentional per invariant #1 (forwarding > metering). A proper fix would require side-channel decompression for metering while forwarding original compressed bytes — not a one-line header deletion.
- **ErrorTimeline no-baseline first bucket:** When no health-metrics row exists before the query range, the first data point shows absolute cumulative counter values rather than a delta. This is a known semantic edge case; the UI should not display the first bucket as a per-interval delta when there is no prior baseline.
