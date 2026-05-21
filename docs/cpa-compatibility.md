# CLIProxyAPI Compatibility Matrix

This matrix documents the MeteringProxy compatibility target for verified
CLIProxyAPI releases and the rules for future CPA upgrades. The invariant is
conservative: unsupported or unverified CPA surfaces must be forwarded
transparently or shown as unavailable; they must not be reported as fully
metered quota data.

## Verified Versions

| CPA version | Release date | Verification |
|---|---:|---|
| v7.0.4 | 2026-05-12 | Covered by `internal/compat` fake-CPA contract tests |
| v7.0.9 | 2026-05-16 | Compared with v7.0.4 release tag; metered routes, usage queue, and `api-call` contract are unchanged. `auth-files` adds `project_id`, which MeteringProxy ignores safely. |
| v7.1.17 | 2026-05-19 | Compared with v7.0.9 release tag; metered LLM routes, HTTP usage queue, and `api-call` contract are unchanged. New xAI OAuth and `/v1/videos*` routes stay transparent. RESP usage queue is disabled; use HTTP usage queue. |
| v7.1.19 | 2026-05-20 | Current compatibility target. Image generation/edit routes are captured with image token usage when provider responses expose OpenAI Images `usage`; variations, audio, and videos are request-only until provider usage contracts are verified. |

## Captured API Routes

| CPA route | Metering profile | Status | Notes |
|---|---|---|---|
| `POST /v1/chat/completions` | `chat_completions` | supported | OpenAI chat-compatible response/SSE usage |
| `POST /v1/completions` | `openai_completions` | supported | OpenAI completions-compatible usage |
| `POST /v1/responses` | `responses` | supported | Responses API response/SSE usage; image_generation_call records image facts without storing result payloads |
| `POST /v1/responses/compact` | `responses` | supported | Uses Responses extractor; verify again if CPA changes payload shape |
| `POST /backend-api/codex/responses` | `responses` | supported | Codex direct route alias |
| `POST /backend-api/codex/responses/compact` | `responses` | supported | Codex direct compact alias |
| `POST /v1/messages` | `anthropic_messages` | supported | Anthropic/Claude messages-compatible usage |
| `POST /v1/models/{model}:generateContent` | `gemini_generate_content` | supported | Gemini native usage metadata |
| `POST /v1/models/{model}:streamGenerateContent` | `gemini_generate_content` | supported | Gemini native SSE usage metadata |
| `POST /v1beta/models/{model}:generateContent` | `gemini_generate_content` | supported | Gemini native usage metadata |
| `POST /v1beta/models/{model}:streamGenerateContent` | `gemini_generate_content` | supported | Gemini native SSE usage metadata |
| `POST /v1/images/generations` | `openai_images_generations` | supported | OpenAI Images JSON/SSE usage, image count, partial image count, safe request metadata |
| `POST /v1/images/edits` | `openai_images_edits` | supported | OpenAI Images JSON/SSE usage plus JSON/multipart image metadata without storing image bytes, prompt, filenames, or mask bytes |
| `POST /v1/images/variations` | `openai_images_variations` | request-only | Captures request facts; promote to usage-metered only after provider response contract is verified |
| `POST /v1/embeddings` | `openai_embeddings` | supported | Embedding response usage/prompt tokens |
| `POST /v1/audio/speech` | `openai_audio` | request-only | Captures request facts; audio duration/token adapters are not yet verified |
| `POST /v1/audio/transcriptions` | `openai_audio` | request-only | Captures request facts without storing audio bytes |
| `POST /v1/audio/translations` | `openai_audio` | request-only | Captures request facts without storing audio bytes |
| `POST /v1/videos` and `/v1/videos/edits` | `openai_videos` | request-only | Captures request facts for async video tasks; usage adapter pending |
| `GET /v1/videos/{request_id}` and video subroutes | `openai_videos` | request-only | Captures status/retrieval request facts; not treated as completed usage by itself |
| `POST /api/provider/{provider}/chat/completions` | `chat_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/completions` | `openai_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/responses` | `responses` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/images/generations` | `openai_images_generations` | supported | CPA provider alias without explicit API version |
| `POST /api/provider/{provider}/images/edits` | `openai_images_edits` | supported | CPA provider alias without explicit API version |
| `POST /api/provider/{provider}/images/variations` | `openai_images_variations` | request-only | CPA provider alias without explicit API version |
| `POST /api/provider/{provider}/embeddings` | `openai_embeddings` | supported | CPA provider alias without explicit API version |
| `POST /api/provider/{provider}/v1/chat/completions` | `chat_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/completions` | `openai_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/responses` | `responses` | supported | CPA Amp provider alias; image_generation_call image facts included |
| `POST /api/provider/{provider}/v1/messages` | `anthropic_messages` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/images/generations` | `openai_images_generations` | supported | CPA provider alias |
| `POST /api/provider/{provider}/v1/images/edits` | `openai_images_edits` | supported | CPA provider alias |
| `POST /api/provider/{provider}/v1/images/variations` | `openai_images_variations` | request-only | CPA provider alias |
| `POST /api/provider/{provider}/v1/embeddings` | `openai_embeddings` | supported | CPA provider alias |
| `POST /api/provider/{provider}/v1/audio/*` | `openai_audio` | request-only | CPA provider alias |
| `/api/provider/{provider}/v1/videos*` | `openai_videos` | request-only | CPA provider alias |
| `POST /api/provider/{provider}/v1/models/{model}:generateContent` | `gemini_generate_content` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/models/{model}:streamGenerateContent` | `gemini_generate_content` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1beta/models/{model}:generateContent` | `gemini_generate_content` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1beta/models/{model}:streamGenerateContent` | `gemini_generate_content` | supported | CPA Amp provider alias |

## Transparent Pass-Through Routes

These routes are intentionally not token-metered by MeteringProxy's verified
CPA compatibility support. They should either route directly to CPA in Caddy or
pass through MeteringProxy as `unknown_passthrough`.

| CPA route | Status | Reason |
|---|---|---|
| `GET /v1/models` and provider model-list aliases | pass-through | No token usage |
| `GET /v1/responses` | pass-through | WebSocket route; no current HTTP usage extractor |
| `POST /v1/messages/count_tokens` | pass-through | Token counting helper, not a billable generation record |
| `/v0/management/*` | pass-through or direct to CPA | Management API is not proxied for user traffic |

## Management Integrations

| CPA management surface | Status | Notes |
|---|---|---|
| `GET /v0/management/auth-files` | supported | Supports CPA `{files:[...]}` and legacy `{auth_files:[...]}`; v7.0.9 `project_id` is ignored safely |
| `GET /v0/management/usage-queue?count=N` | supported | Requires CPA `usage-statistics-enabled: true`; v7.1.17+ may include `response_headers`, which MeteringProxy ignores safely |
| RESP `AUTH` + `LPOP`/`RPOP` | legacy only | Available in v7.0.4-v7.0.9; v7.1.17+ returns `ERR RESP AUTH disabled; use mTLS`. Configure `usage_queue.transport` as `auto` or `http` for new CPA releases |
| `POST /v0/management/api-call` | endpoint detected only | CPA v7.0.4-v7.1.19 requires `method` and absolute `url`; it is not treated as a full quota API |

## Quota Support

Full quota snapshots are disabled by default. Verified CPA v7.0.4-v7.1.19
releases expose a generic management `/api-call` helper, but not a normalized
quota contract. Until a provider-specific adapter is implemented and covered by
compatibility tests, WebUI quota should report credential-health fallback rather
than claiming full quota availability.

Supported today:

- Credential health from `auth-files`.
- Quota module status as disabled, partial, unsupported, or unavailable.
- Provider quota rows only when explicit provider adapters produce normalized
  `quota_current` rows.

Not supported today:

- Automatic Codex/Claude/Kimi quota snapshots from verified CPA releases without
  provider-specific templates.
- Browser-driven generic `/api-call` proxying.

## Future CPA Upgrade Rule

For each CPA release:

1. Update or add fake-CPA fixtures in `internal/compat`.
2. Run `go test ./...`.
3. Compare new CPA routes and management payloads against this matrix.
4. Add metered profiles only when response usage extraction is verified.
5. Keep unknown or changed routes transparent until the contract is known.
