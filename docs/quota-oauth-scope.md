# Quota Snapshot and OAuth Scope

This document records the current design decision for possible CLIProxyAPI quota and OAuth integration.

Current implementation note: MeteringProxy already exposes credential health,
quota status, and refresh diagnostics in the read-only WebUI. Full quota is
reported only when a server-side provider adapter produces normalized quota
rows; otherwise the UI falls back to credential health and marks quota as
partial, unavailable, unsupported, or disabled. A generic `/api-call` endpoint
alone is not treated as proof of full quota support.

## Project Boundary

MeteringProxy's core priorities remain:

- transparent proxying for LLM traffic
- minimal impact on streaming and non-streaming LLM experience
- asynchronous metering and read-only operational visibility

Any quota or OAuth feature must stay outside the LLM request path. It must not run before forwarding `/v1/chat/completions` or `/v1/responses`, and failure to fetch quota or start OAuth must never affect LLM traffic.

## Quota Snapshot

Quota querying is a reasonable fit for MeteringProxy if implemented as a read-only WebUI feature.

Recommended architecture:

```text
MeteringProxy WebUI
  -> /metering/api/quota
  -> MeteringProxy server-side quota client
  -> CLIProxyAPI /v0/management/auth-files
  -> CLIProxyAPI /v0/management/api-call
  -> provider quota/usage endpoint
```

The browser must not receive the CLIProxyAPI management key, provider tokens, or raw auth files. MeteringProxy should call CLIProxyAPI from the server side and return normalized, read-only quota snapshots.

Initial provider scope:

- Claude
- Codex
- Kimi

Deferred providers:

- Gemini CLI
- Antigravity

Gemini CLI and Antigravity require more provider-specific handling such as project IDs, supplementary tier/credit requests, and multiple quota endpoints. They are better treated as a later version if the initial Claude/Codex/Kimi feature proves useful.

## Quota Constraints

The quota feature should be guarded by explicit configuration:

```yaml
cliproxy_management:
  enabled: true
  base_url: "http://127.0.0.1:8317/v0/management"
  key_file: "/data/management_key"
  quota:
    enabled: true
    providers: ["claude", "codex", "kimi"]
    cache_ttl: "5m"
    timeout: "10s"
  oauth:
    enabled: false
```

Implementation constraints:

- read-only
- server-side only management key use
- fixed provider request templates; no browser-supplied arbitrary URL calls
- timeout on every upstream quota request
- bounded concurrency
- cached snapshots, preferably 5-10 minutes
- no raw quota payload persistence; current implementation stores only normalized current-state rows and bounded diagnostic refresh events
- no impact on proxy forwarding if quota fails

## UI Direction

Quota should be presented as an operational snapshot, not as quota management.

Recommended WebUI section: `Credential Quota`.

Provider summary cards:

- provider name
- credential count
- low/exhausted count
- next reset time when known
- last checked time

Unified table columns:

- Provider
- Credential
- Plan
- Window
- Remaining
- Reset
- Status
- Last Checked

Display rules:

- Remaining should use a progress bar where possible.
- Above 50% is normal, 20-50% is warning, below 20% is danger.
- Unknown remaining should use a neutral state.
- A single credential may produce multiple rows if the provider has multiple quota windows. Current subscription-style providers should be rendered as short-window plus long-window state, for example `5h` and `weekly`, instead of collapsing the credential to one remaining/limit number.
- Query errors should be visible per credential and must not hide other providers.

Plan display:

- Claude can usually show `Free`, `Pro`, `Max`, `Team`, or `Unknown` by querying profile data.
- Codex can usually show a normalized plan from `plan_type` / `planType` or from auth metadata.
- Kimi should be `Unknown` unless its usage payload exposes a stable plan field.

Plan display is helpful but should not block quota rendering. If plan lookup fails while quota succeeds, show `Unknown`.

## OAuth Scope

OAuth is possible but should be treated as a higher-risk optional management convenience because it creates or updates CLIProxyAPI credentials.

Recommended default:

- Do not implement OAuth in MeteringProxy by default.
- Keep OAuth in CLIProxyAPI Management Center.
- MeteringProxy may show a link or hint directing the operator to Management Center when no credentials are available.

If OAuth is added later, it must be explicitly enabled and should still proxy through CLIProxyAPI Management API rather than implementing provider OAuth flows directly in MeteringProxy.

Minimum OAuth constraints:

- disabled by default
- protected by the same external authentication as WebUI
- server-side management key only
- no provider token persistence in MeteringProxy
- no auth file persistence in MeteringProxy
- state/callback handling delegated to CLIProxyAPI where possible
- visible status polling and timeout handling
- no generic credential editing, deletion, upload, or download

OAuth operations are not part of the transparent proxy core. They should be optional and visibly separated from quota snapshots.

## Non-Goals

MeteringProxy should not add:

- quota enforcement
- per-request quota checks before forwarding
- credential upload/download/delete
- provider token display
- raw auth file display
- CLIProxyAPI config editing
- generic `/api-call` proxying from the browser
- model fallback or credential switching

Those features belong to CLIProxyAPI or its Management Center, not to the metering proxy.
