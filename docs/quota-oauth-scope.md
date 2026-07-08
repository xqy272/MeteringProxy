# Quota Snapshot and OAuth Scope

This document records the current design decision for possible CLIProxyAPI quota and OAuth integration.

Current design decision: quota and OAuth are not transparent-proxy core
features. The appropriate shape is a CLIProxyAPI management mirror:
Credential/Auth state is the primary feature, CPA cooldown state is a separate
maintenance signal, and provider quota snapshots are optional manual checks.
Full quota is reported only when a server-side provider adapter produces
normalized quota rows; otherwise the UI falls back to CPA Auth health and marks
provider quota as not refreshed, unsupported, unavailable, or partial. A generic
`/api-call` endpoint alone is not treated as proof of full quota support.

## Project Boundary

MeteringProxy's core priorities remain:

- transparent proxying for LLM traffic
- minimal impact on streaming and non-streaming LLM experience
- asynchronous metering and read-only operational visibility

Any quota or OAuth feature must stay outside the LLM request path. It must not
run before forwarding `/v1/chat/completions` or `/v1/responses`, and failure to
fetch quota, mirror auth state, reset CPA cooldown, or start OAuth must never
affect LLM traffic.

## Recommended System Shape

The best fit is not a standalone quota system. It is a three-layer management
mirror that keeps the LLM proxy path clean.

Layer 1: `CPA Auth Mirror`

- Source: `GET /v0/management/auth-files`.
- Purpose: show credential health and maintenance signals.
- Data: provider, auth index, label/name, status, status message, disabled,
  unavailable, success/failed counters, recent requests, next retry time, plan
  hints, project hints, and websocket capability.
- Refresh mode: manual by default. A very slow scheduled refresh can be added
  later only if it proves useful.

Layer 2: `CPA Cooldown State`

- Source: CPA auth status fields, request failures, and `POST /v0/management/reset-quota`.
- Purpose: explain why CPA currently avoids or cools down an auth/model.
- Important wording: resetting CPA quota is a cooldown/routing-state reset. It
  is not provider quota recovery.

Layer 3: `Provider Quota Snapshot`

- Source: fixed server-side provider templates executed through
  `POST /v0/management/api-call`.
- Purpose: show real provider balance/usage windows only when a verified adapter
  can parse the provider response.
- Refresh mode: manual only.
- Fallback: when no adapter is available, show `unsupported` and keep Auth Mirror
  fully usable.

Recommended architecture:

```text
MeteringProxy WebUI
  -> /metering/api/cpa/auth
  -> MeteringProxy CPA Auth Mirror cache

MeteringProxy WebUI
  -> /metering/api/cpa/auth/refresh
  -> CLIProxyAPI /v0/management/auth-files

MeteringProxy WebUI
  -> /metering/api/cpa/cooldown/reset
  -> CLIProxyAPI /v0/management/reset-quota

MeteringProxy WebUI
  -> /metering/api/provider-quota
  -> MeteringProxy provider quota cache

MeteringProxy WebUI
  -> /metering/api/provider-quota/refresh
  -> MeteringProxy fixed provider quota template
  -> CLIProxyAPI /v0/management/api-call
  -> provider quota/usage endpoint

MeteringProxy WebUI
  -> /metering/api/provider-quota/diagnostics
  -> MeteringProxy bounded refresh event cache
```

The browser must not receive the CLIProxyAPI management key, provider tokens,
raw auth files, or raw `api-call` responses. MeteringProxy should call
CLIProxyAPI from the server side and return normalized management facts.

API behavior:

- `GET /metering/api/cpa/auth` reads the cached Auth Mirror only. It must not
  call CLIProxyAPI.
- `POST /metering/api/cpa/auth/refresh` is the explicit user action that calls
  `GET /v0/management/auth-files`.
- `POST /metering/api/cpa/cooldown/reset` calls CPA reset-quota and reports only
  cooldown reset success or failure. It must not mutate provider quota snapshot
  state.
- `GET /metering/api/provider-quota` reads the cached provider quota snapshot
  only. It must not call `/api-call`.
- `POST /metering/api/provider-quota/refresh` is the explicit user action that
  can call CPA `/api-call` through fixed server-side templates.
- `GET /metering/api/provider-quota/diagnostics` returns recent refresh
  attempts, normalized errors, unsupported providers, stale cache state, and
  rate-limit decisions.

If the old `/metering/api/quota` API is kept for compatibility, it should be a
read-only aggregate alias. It must not become the implementation center and must
not refresh on GET.

State model:

| State | Applies to | Meaning |
|---|---|---|
| `disabled` | Auth/Quota | Feature disabled by configuration |
| `not_refreshed` | Auth/Quota | Enabled but never manually refreshed |
| `refreshing` | Auth/Quota | Refresh requested and still in flight |
| `available` | Auth/Quota | Current normalized cache is usable |
| `partial` | Quota | Some provider/credential rows are usable and some are not |
| `unsupported` | Quota | No verified adapter exists for the provider |
| `unavailable` | Auth/Quota | CPA management or provider request path is unreachable |
| `rate_limited` | Auth/Quota | MeteringProxy min interval or upstream limit blocked refresh |
| `stale` | Auth/Quota | Old cache exists but exceeds the freshness window |
| `error` | Auth/Quota | Last refresh failed with a normalized error |

Credential Health/Auth Mirror is not a provider quota fallback. When provider
quota is unsupported or unavailable, the UI should keep Auth Mirror visible and
say provider quota is unavailable, not imply full quota support.

Provider quota adapter scope:

- Starts empty unless an adapter has been verified against the current CPA and
  provider behavior.
- Claude, Codex, and Kimi can be adapter candidates.
- Provider quota adapters are secondary to Auth Mirror and must not block it.

Deferred providers:

- Gemini CLI
- Antigravity

Gemini CLI and Antigravity require more provider-specific handling such as
project IDs, supplementary tier/credit requests, cooldown semantics, and
multiple quota endpoints. They are better treated as later manual adapters if
the simpler candidates prove useful.

## Quota Constraints

The quota feature should be guarded by explicit configuration:

```yaml
cliproxy_management:
  enabled: true
  base_url: "http://127.0.0.1:8317/v0/management"
  key_file: "/data/management_key"
  credential_health:
    enabled: true
    refresh_mode: "manual"
  quota:
    enabled: true
    refresh_mode: "manual"
    providers: ["claude", "codex", "kimi"]
    timeout: "10s"
    min_refresh_interval: "60s"
  oauth:
    enabled: false
```

Implementation constraints:

- read-only
- server-side only management key use
- fixed provider request templates; no browser-supplied arbitrary URL calls
- timeout on every upstream quota request
- bounded concurrency
- cached snapshots returned by GET APIs; refresh only on explicit user action
- manual refresh handled by a `RefreshService` style abstraction: singleflight,
  minimum refresh interval, bounded concurrency, timeouts, and bounded
  diagnostic events
- no raw quota payload persistence; current implementation stores only normalized current-state rows and bounded diagnostic refresh events
- credential health may store normalized CPA management diagnostics such as status messages, recent success/failure totals, next retry times, structured error codes, and plan labels; it must not store raw auth files, provider tokens, email addresses, account IDs, or full request timelines
- no impact on proxy forwarding if quota fails
- no startup quota probe
- no WebUI-open quota refresh
- no background quota polling by default

Configuration migration:

- `quota.enabled: true` means manual provider quota refresh is allowed.
- It must no longer imply that MeteringProxy starts a background quota poller.
- Missing `refresh_mode` should default to `manual`.
- A future scheduled mode must use an explicit value such as
  `refresh_mode: "scheduled"` plus separate interval/backoff settings.

## UI Direction

The UI should present `CPA Auth & Quota`, not a standalone quota management
product.

Recommended WebUI section: `CPA Auth & Quota`.

Primary cards:

- provider name
- credential count and health count
- unavailable/cooldown count
- next retry time when known
- last manual refresh time

Auth Mirror table columns:

- Provider
- Credential
- Plan
- Status
- Status Message
- Success/Failed
- Recent Requests
- Next Retry
- Websockets
- Last Checked

Provider quota snapshot columns, only when a verified adapter returns data:

- Provider
- Credential
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
- If provider quota is unsupported, keep the Auth Mirror visible and label quota
  as `unsupported`.
- If the user has never clicked refresh, show `not_refreshed` instead of
  triggering a refresh.
- `Reset CPA cooldown` must be visually and textually separate from `Refresh
  provider quota`.

Plan display:

- Claude can usually show `Free`, `Pro`, `Max`, `Team`, or `Unknown` by querying profile data.
- Codex can usually show a normalized plan from `plan_type` / `planType` or from auth metadata.
- Kimi should be `Unknown` unless its usage payload exposes a stable plan field.

Plan display is helpful but should not block quota rendering. If plan lookup fails while quota succeeds, show `Unknown`.

## OAuth Scope

OAuth is possible but should be treated as an optional CPA convenience entry,
not a MeteringProxy credential system.

Recommended default:

- Do not implement OAuth in MeteringProxy by default.
- Keep OAuth in CLIProxyAPI Management Center.
- MeteringProxy may show a link or hint directing the operator to Management Center when no credentials are available.
- Phase 1 should not add OAuth buttons or status polling to the WebUI. It should
  focus on Auth Mirror reachability, cached auth state, and explicit manual
  refresh.
- If enabled in a later phase, MeteringProxy may call CPA `*-auth-url` endpoints
  and display `get-auth-status`, but CPA still owns state, callback handling,
  and credential persistence.

If OAuth is added later, it must be explicitly enabled and should still proxy
through CLIProxyAPI Management API rather than implementing provider OAuth flows
directly in MeteringProxy.

Minimum OAuth constraints:

- disabled by default
- protected by the same external authentication as WebUI
- server-side management key only
- no provider token persistence in MeteringProxy
- no auth file persistence in MeteringProxy
- state/callback handling delegated to CLIProxyAPI
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
