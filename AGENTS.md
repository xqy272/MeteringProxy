# v0.5.0 Temporary Execution Contract

- Before editing, resuming, or delegating, read `CLAUDE.md`, `docs/v0.5.0-key-insights-plan.md`, and `docs/v0.5.0-execution-state.md`.
- The v0.5.0 plan is authoritative; never tag or release a partial implementation.
- Preserve proxy transparency, asynchronous metering, the hashing contract, and all privacy invariants in `CLAUDE.md`.
- GPT owns architecture, cost correctness, security, migrations, integration, and release; `grok-4.5` with `high` reasoning may implement bounded slices.
- Every commit must be focused, buildable, tested, and safe to review. Preserve unrelated and ignored local DB, salt, and config files.
- Release only after the complete RC checklist passes and GitHub Actions, Release, and GHCR are verified.
- Remove this file and `docs/v0.5.0-execution-state.md` before creating the `v0.5.0` tag.
