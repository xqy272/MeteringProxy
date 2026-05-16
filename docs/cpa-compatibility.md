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

## Metered API Routes

| CPA route | Metering profile | Status | Notes |
|---|---|---|---|
| `POST /v1/chat/completions` | `chat_completions` | supported | OpenAI chat-compatible response/SSE usage |
| `POST /v1/completions` | `openai_completions` | supported | OpenAI completions-compatible usage |
| `POST /v1/responses` | `responses` | supported | Responses API response/SSE usage |
| `POST /v1/responses/compact` | `responses` | supported | Uses Responses extractor; verify again if CPA changes payload shape |
| `POST /backend-api/codex/responses` | `responses` | supported | Codex direct route alias |
| `POST /backend-api/codex/responses/compact` | `responses` | supported | Codex direct compact alias |
| `POST /v1/messages` | `anthropic_messages` | supported | Anthropic/Claude messages-compatible usage |
| `POST /v1/models/{model}:generateContent` | `gemini_generate_content` | supported | Gemini native usage metadata |
| `POST /v1/models/{model}:streamGenerateContent` | `gemini_generate_content` | supported | Gemini native SSE usage metadata |
| `POST /v1beta/models/{model}:generateContent` | `gemini_generate_content` | supported | Gemini native usage metadata |
| `POST /v1beta/models/{model}:streamGenerateContent` | `gemini_generate_content` | supported | Gemini native SSE usage metadata |
| `POST /api/provider/{provider}/chat/completions` | `chat_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/completions` | `openai_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/responses` | `responses` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/chat/completions` | `chat_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/completions` | `openai_completions` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/responses` | `responses` | supported | CPA Amp provider alias |
| `POST /api/provider/{provider}/v1/messages` | `anthropic_messages` | supported | CPA Amp provider alias |
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
| `POST /v1/images/generations` | pass-through | Non-LLM-token billing not implemented |
| `POST /v1/images/edits` | pass-through | Non-LLM-token billing not implemented |
| `/v0/management/*` | pass-through or direct to CPA | Management API is not proxied for user traffic |

## Management Integrations

| CPA management surface | Status | Notes |
|---|---|---|
| `GET /v0/management/auth-files` | supported | Supports CPA `{files:[...]}` and legacy `{auth_files:[...]}`; v7.0.9 `project_id` is ignored safely |
| `GET /v0/management/usage-queue?count=N` | supported | Requires CPA `usage-statistics-enabled: true` |
| RESP `AUTH` + `LPOP`/`RPOP` | supported | Disabled by CPA when `home.enabled` is true; HTTP queue is preferred in auto mode |
| `POST /v0/management/api-call` | endpoint detected only | CPA v7.0.4-v7.0.9 requires `method` and absolute `url`; it is not treated as a full quota API |

## Quota Support

Full quota snapshots are disabled by default. Verified CPA v7.0.4-v7.0.9
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
