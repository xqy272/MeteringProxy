# Test Fixtures

## Fixture Files

| File | Description |
|------|-------------|
| `chat_completions_stream.txt` | OpenAI chat completions SSE stream with 4 data chunks + [DONE]. Usage in the final chunk: prompt_tokens=15, completion_tokens=8, total_tokens=23, model=gpt-4o-2026-03-18. |
| `chat_completions_nonstream.json` | OpenAI chat completions non-streaming JSON response. Same token counts as stream fixture for cross-validation. |
| `responses_stream.txt` | OpenAI Responses API SSE stream with response.created, output_text.delta, response.completed events. Usage at the end: input_tokens=20, output_tokens=10, total_tokens=30, reasoning_tokens=3, cached_tokens=5. |
| `responses_nonstream.json` | OpenAI Responses API non-streaming JSON response. Same token counts as responses stream fixture. |

## Sanitization

All fixtures are synthetic/sanitized:
- No real prompt body or response content beyond dummy text
- No real API keys
- No real client IPs
- No real request IDs

## Golden Test Expectations

### Stream (SSE) path
- Every byte forwarded from upstream must reach the client unchanged (including `\r\n` vs `\n`).
- SSE `data:` lines containing `usage` must be parsed into UsageInfo.
- Cross-chunk SSE lines (where a single SSE event is split across multiple TCP reads) must be reassembled and parsed correctly.
- Long lines (>256KB) must be forwarded but skip SSE parsing (not block forwarding).

### Non-stream path
- The full response body must be forwarded without truncation.
- Usage extraction uses a bounded prefix sample (default 2MB).
- If the sample is truncated before the JSON parse completes, no parse error is counted.
- Read-while-write semantics: the proxy must NOT buffer the full response before forwarding.

### Queue overflow
- When the writer queue is full, events are dropped.
- Dropped events must NOT modify or truncate the response bytes sent to the client.
- The HTTP status code must still reflect the upstream response.

## Regression Definition

A regression is ANY of:
1. Response byte differences between proxy input and output (golden file test failure).
2. Non-stream path buffering full response before forwarding (loss of read-while-write).
3. Queue full condition causing response truncation or status code change.
4. SSE parse errors on valid usage lines (token extraction regression).
5. Stream detection behavior change for edge cases (Content-Type fallback).
