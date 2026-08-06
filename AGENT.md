# AGENT.md — cc-token-exposer (`ccx`)

Canonical context for AI agents and contributors. The `docs/` directory is
local-only working material and is NOT committed; this file is the single
in-repo source of truth for design decisions and constraints.

## What this is

`ccx` is a zero-config CLI that tracks Claude Pro/Max plan credit-limit
windows (5-hour session, 7-day, plus per-model weekly limits like Opus and
Fable) using the **same authoritative
source as Claude Code's built-in `/usage`**: the OAuth usage endpoint. It
reuses Claude Code's existing OAuth token read-only, so if `claude` works on
the machine, `ccx` works with no setup.

Positioning: local-JSONL tools (ccusage, claude-monitor) only *estimate*
limits and drift from the real lockout; linuxlewis/claude-usage uses
authoritative values but requires manually extracted browser cookies. `ccx`
gives authoritative numbers with zero setup.

**v1 scope (current, deliberately minimal): two usage commands plus
housekeeping.**

- `ccx now [--json]` — one-shot lookup (human or single-line JSON)
- `ccx statusline` — one-line output for the Claude Code statusline
  (registered via `~/.claude/settings.json` → `statusLine.command`)
- `ccx version` — print the injected build version
- `ccx update [--check]` — self-update to the latest GitHub release
  (download the matching release archive, verify its SHA-256 against
  `checksums.txt`, atomically replace the running binary). `--check` only
  reports whether a newer version exists. Homebrew installs defer to
  `brew upgrade`. This is a *binary* update; it never touches OAuth
  credentials (unrelated to invariant #3's "no self refresh").

Removed from v1 after working implementations existed (preserved in git
history, planned to return in v2): `ccx watch` (NDJSON polling stream),
threshold notifications (`internal/notify`), history logging
(`internal/history`), credential injection (`creds/injected.go`),
`usage.Backoff`.

## Architecture

```
cmd/ccx/main.go      composition root: builds the ONE production engine and
                     injects it into commands as a `resolver` interface
cmd/ccx/now.go       rendering + exit-code policy
cmd/ccx/statusline.go  statusline formatting + stdin rate_limits parsing
                     (rate_limits -> usage.Snapshot -> engine.ResolveStdin)
cmd/ccx/update.go    self-update command (thin) over internal/selfupdate
internal/
  selfupdate/  GitHub-release self-update: Latest (releases/latest) ->
               FetchAsset -> VerifyChecksum (checksums.txt) -> ExtractBinary
               (tar.gz) -> Apply (atomic rename over os.Executable). Stdlib
               only; injectable http client + apiBase + platform for tests.
  engine/      THE BRAIN. Resolve(ctx) and ResolveStdin(ctx, snap) ->
               *schema.State, runs the degrade ladder. Depends only on 5
               consumer-side interfaces it defines:
               CredResolver / Fetcher / Cache / TranscriptProbe / Clock.
  schema/      The wire contract (State, schema_version=1). Leaf package,
               imports nothing internal. The PUBLIC CONTRACT is the JSON
               emitted by `now --json`, not the Go types (internal/ blocks
               external import by design). ScopedLimits (map[string]*Window)
               is additive over schema_version=1; seven_day_opus/fable remain
               as backward-compat aliases.
  usage/       oauth/usage HTTP client + Reconcile consistency guard +
               Overlay gap-fill merge. decode populates Snapshot.ScopedLimits
               dynamically from all weekly_scoped entries in limits[]; no
               model name is hardcoded. decode is also the only producer that
               sets Snapshot.ScopedProbed (marks "the API answered about
               scoped limits"), which the engine uses to judge stdin
               completeness.
  creds/       credential acquisition: file > macOS keychain shell-out.
  cache/       flock-protected, atomic-write disk cache of opaque JSON.
               Store records {fetched_at, payload}; Touch(attempted_at)
               stamps a refresh attempt without touching payload/fetched_at.
  transcript/  last-resort fallback: parses limit-hit messages from
               ~/.claude/projects/**/*.jsonl (read-only, best-effort).
```

Engine's degrade ladder (Resolve never fails — it always returns a State
carrying the best known truth plus its freshness):

1. Cache fresh (within TTL 120s) → serve from disk, zero API calls.
2. Live fetch OK → `usage.Reconcile(prev, fresh)` → store cache → serve.
3. 429 / 5xx / network → serve stale cache (`stale: true`, `stale_age`).
4. 401 → re-read credentials once, retry once if the token changed;
   otherwise `auth: "expired"` (still with stale data if available).
5. No cache at all → transcript limit-hit probe → else error State.

ResolveStdin (statusline only) is a four-stage pipeline. Design principle: the
disk cache is the reference set of windows the plan reports; the bounded
refresh exists to HEAL GAPS in what is served, never to keep the cache warm —
a cached window refreshes only when the served line depends on it and stdin
does not carry it.

1. parse   `rate_limits` → `usage.Snapshot` (tolerant: `used_percentage` or
           `utilization`; resets_at as ISO or epoch; `model_scoped` keyed by
           `display_name`; `utilization: null` entries dropped — unknown ≠ 0;
           `seven_day_opus` backfills Opus only). No usable window → nil → the
           ladder handles the tick.
2. gate    a refresh runs only when BOTH hold: (a) no attempt within the TTL —
           keyed off the *later* of the cache's `fetched_at` and
           `attempted_at`, NOT data staleness; and (b) stdin is incomplete.
           Completeness is cache-relative, not shape-absolute: stdin is
           complete when it covers every window the cache carries — every
           scoped model included. CC's stdin projection omits scoped models
           outside its allowlist (typically Fable), so a cache-known model
           missing from stdin is a gap that re-opens the ≤1/TTL refresh. No
           cache at all, or only a snapshot never produced by an API decode
           (`usage.Snapshot.ScopedProbed` — set only by `usage.decode`), is
           also incomplete: the first tick bootstraps one seeded refresh that
           establishes the reference set. A plan proven scoped-less
           (`ScopedProbed`, no scoped keys) terminates the loop instead of
           polling forever.
3. refresh bounded: `resolveToken` → `fetchWithToken` (5s budget) →
           `Reconcile` → `storeCache`. Failure records the attempt via
           `Cache.Touch` (`payload`/`fetched_at` untouched), so a persistently
           failing endpoint still yields ≤1 attempt per TTL instead of a fetch
           on every statusline tick.
4. serve   `usage.Overlay(stdin, cache)` — per window stdin wins, gaps fill
           from cache, `ExtraUsage` always from the fresh side — served with
           `source: "stdin"`. The `stale`/≈ marker keys off data age
           (`fetched_at`), not attempt recency: a failed refresh shows ≈
           honestly while the next attempt stays throttled. No suspect guard
           runs here — the statusline mirrors Claude Code's own values; the
           ≥30pt-drop guard and its `(suspect)` marker live on the ladder
           (`now`), where the marker is visible.

## Invariants — do not break these

1. **Cache-first.** Claude Code invokes the statusline command every few
   seconds. Within the cache TTL ccx must NEVER touch the API; the disk cache
   (+ flock) caps request volume at ~1 per TTL across ALL invocations.
   Breaking this gets the user rate-limited (the endpoint 429s aggressively).
   The stdin rate_limits path keeps that cap: it does at most one bounded
   refresh per TTL, gated on the *later* of the cache's `fetched_at` and
`attempted_at`. A failed refresh records its attempt (`Cache.Touch`) so a
failing/429ing endpoint does NOT re-fetch on every statusline tick; a stdin
snapshot covering every cache-known window means zero calls, while a
cache-known scoped model missing from stdin (e.g. Fable) costs ≤1 call per
TTL. A plan proven to have no scoped limits (`ScopedProbed`, no scoped keys)
terminates the loop instead of polling forever. Both paths share the flock'd cache,
   so the per-machine bound holds. Caveat: the check→fetch→store sequence is not
   locked end-to-end, so several concurrent sessions crossing the TTL boundary
   at once can each fetch before the first store lands (bounded by session
   count, self-healing on the next store); the flock serializes writes, not
   fetch de-duplication.
2. **Token hygiene.** The OAuth token is read-only and in-memory only. It must
   never appear in logs, error messages, the cache file, test fixtures, or
   `String()` output (creds redacts). The cache stores only
   `{fetched_at, attempted_at?, payload}` where payload is a token-free usage
   snapshot and `attempted_at` is an optional refresh-attempt timestamp.
3. **No self refresh.** Never run an OAuth refresh grant. Refresh tokens may
   rotate; consuming one can invalidate Claude Code's stored refresh token and
   break the user's login. On expiry: re-read `~/.claude/.credentials.json`
   (Claude Code refreshes it itself), else surface `auth: "expired"`.
4. **Required request headers.** `Authorization: Bearer <token>`,
   `anthropic-beta: oauth-2025-04-20`, and `User-Agent: claude-code/<ver>`.
   Without the claude-code User-Agent the endpoint applies a far stricter
   429 bucket.
5. **Never blank-screen.** Every failure degrades to "last known truth +
   freshness marker" (`≈` prefix for stale, `⚠ login` for auth, `⛔` for
   limit-hit). statusline must never exit non-zero or print nothing.
6. **Wire stability.** `schema.State` (schema_version=1) is the public
   contract. Changes must be additive (new optional fields); breaking changes
   bump the version. `utilization` is float64 on the wire (the real API sends
   fractionals); round only at display time. `scoped_limits`
   (map[string]*Window) was added additively; `seven_day_opus` /
   `seven_day_fable` remain as aliases for backward compat. `source` gained
   the additive value `"stdin"` (statusline rate_limits path).
7. **Transcript files are read-only.** Never write under `~/.claude/`.

## Data source notes

- Endpoint: `GET https://api.anthropic.com/api/oauth/usage` — **unofficial**,
  may change or vanish. Response carries legacy top-level `five_hour` /
  `seven_day` / `seven_day_opus` windows, each `{utilization: 0-100 float,
  resets_at}`, plus `extra_usage`.
- **Per-model weekly limits live in a newer `limits[]` array**, not in
  top-level fields. Each entry is `{kind, group, percent, resets_at, scope,
  is_active}`; scoped-model limits are `kind: "weekly_scoped"` keyed by
  `scope.model.display_name` (e.g. "Opus", "Fable", "Sonnet"). `usage.decode`
  lifts **all** `weekly_scoped` entries into `Snapshot.ScopedLimits`
  (`map[string]*Window`) dynamically — no model name is hardcoded. The legacy
  top-level `seven_day_opus` is used as a fallback when `limits[]` is absent
  (older API/CC versions). The wire contract (`schema.Snapshot`) keeps
  `seven_day_opus` / `seven_day_fable` as additive aliases of
  `ScopedLimits["Opus"]` / `ScopedLimits["Fable"]` for backward compat with
  existing `--json` consumers; new models appear only in `scoped_limits`.
  As of 2026-07 the live response also sends `seven_day_sonnet`,
  `seven_day_cowork`, `seven_day_omelette` (all null) and several codename
  fields (`tangelo`, `iguana_necktie`, etc.) — all ignored.
- Token location: `~/.claude/.credentials.json` (`claudeAiOauth.accessToken`,
  `expiresAt` epoch-ms); some machines store it in the macOS Keychain item
  "Claude Code-credentials" instead (creds falls back to `security` CLI).
- Known server bug (anthropic/claude-code#52497): the weekly counter can drop
  implausibly mid-cycle. `usage.Reconcile` keeps the previous value and sets
  `suspect: true` when utilization falls ≥30 points within an unchanged
  reset cycle.
- statusline stdin: Claude Code pipes session JSON. A `rate_limits` field
  appears intermittently across versions (#40094); when present it is parsed
  tolerantly (`used_percentage` or `utilization`; resets_at as ISO string or
  epoch) into a usage.Snapshot and fed to `engine.ResolveStdin`. Never depend
  on it. Confirmed shape (CC 2.1.217): `five_hour` / `seven_day` /
  `seven_day_oauth_apps` / `seven_day_opus` / `seven_day_sonnet` windows,
  `model_scoped: [{display_name, utilization|null, resets_at ISO|null}]`
  (projected by CC from the server limits[] overage-included-models
  allowlist, present only when non-empty), and `extra_usage`. The parser maps
  model_scoped by display_name into ScopedLimits (seven_day_opus only
  backfills Opus); sonnet/oauth_apps/extra_usage are ignored, consistent
  with the API path.
- Local limit-hit signal: when a limit is hit, Claude Code writes a synthetic
  transcript message (`isApiErrorMessage: true`, text like
  "You've hit your session limit · resets 4:50pm (Asia/Seoul)"). The
  transcript probe parses the reset time (am/pm, explicit IANA tz honored,
  rolled forward to the future).

## Comment policy

**Comments are forbidden.** The code must be readable enough to stand alone;
design rationale, invariants, and external constraints live in this file
(AGENT.md), not in code.

Two narrow exceptions:

- A one-line package comment on each package.
- A concise godoc (1–2 lines) on shared util/helper functions whose behavior
  is not fully visible in the signature — parsers, formatters, and the like
  (e.g. `parseRetryAfter`, `ParseReset`, `humanizeDuration`). Domain and flow
  functions, types, fields, constants, and errors get no comment.

Test files may keep short scenario notes (they document expected behavior).

## Development

```sh
go build ./...
go test -race ./...   # ALL tests are deterministic: httptest for HTTP,
                      # injected Clock for time, fake resolver for commands.
go vet ./... && gofmt -l .
```

- **Never call the real API from tests or casual verification.** Live checks
  are manual, rare, and deliberate (protect the 429 budget).
- Commands take a `resolver` interface; test command behavior (rendering,
  exit codes, flag handling) with a fake resolver returning canned States —
  see `cmd/ccx/commands_test.go`.
- Interfaces are defined at the consumer (engine defines its ports; usage
  does not export an interface). Keep it that way.
- Dependencies are minimal by policy: cobra + gofrs/flock + stdlib. Adding a
  dependency needs a strong reason.
- Local install for dogfooding: `go install ./cmd/ccx` → `~/go/bin/ccx`; the
  user's statusline points at that path.

## Exit codes

- `now`: 0 on a snapshot State; 1 on an error State (message already printed
  to stdout, `errSilentExit` suppresses duplicate stderr output).
- `statusline`: always 0 — it must never break the statusline.

## Roadmap & deferred decisions

- **v1.1 (deferred):** burn-rate prediction (additive `prediction` field on
  State) + reintroduce history with an append-on-fresh-fetch rule (no daemon
  needed — at most one line per TTL).
- **v2 (blocked on toolchain):** macOS menubar app (SwiftUI MenuBarExtra) that
  subscribes to a revived `ccx watch --json` NDJSON stream as a subprocess.
  Swift scaffold + UI design doc are preserved in a git stash
  ("M2 WIP: Swift scaffold + UI design"). Blocked because this machine's
  CommandLineTools are corrupted (SwiftBridging modulemap duplicate +
  PackageDescription dylib mismatch); repair needs sudo CLT reinstall or
  full Xcode.
- **Publish prep (decided, not yet done):** repo will go public with
  `go install` distribution. The Go module now lives at the repo root
  (module `github.com/seosd97/cc-token-exposer`), so plain `v0.1.0` tags work.
  Before tagging: verify the GitHub account matches the module path (`github.com/seosd97/cc-token-exposer`), then
  tag `v0.1.0`. CI (.github/workflows/ci.yml) already exists.

## Decision log (abridged)

- Data source: OAuth usage endpoint over local-JSONL estimation (accuracy)
  and over web session cookies (zero-setup). Hybrid fallbacks retained.
- Go core + (future) Swift shell; core is a standalone product, the shell is
  a consumer of its JSON output. cgo linking rejected (build complexity).
- v1 minimalism: anything without a current consumer was cut even when
  already implemented — watch/notify/history removal commits explain each.
- `utilization` int → float64 after a live API test caught fractional values
  (also needed for future burn-rate math).
- Engine returns State-only (no error): every failure is a degraded State.
- stdin path cache freshness: when Claude Code reliably pipes rate_limits the
  ladder rarely runs. ResolveStdin does a bounded refresh (≤1/TTL) only when
  stdin is incomplete, so the overall API cap holds and older-CC /
  allowlist-filtered deployments self-heal instead of showing perpetually-stale
  ≈ windows.
- stdin refresh throttle keys off attempts, not data age: the refresh gate must
  use the last *attempt* time, not just cache staleness. Keying off staleness
  alone means a failed refresh (429 / network down) leaves the cache stale, so
  every statusline tick — auto-fired every few seconds — re-fetches, amplifying
  the exact 429 the tool exists to avoid. So a failed refresh writes an
  `attempted_at` stamp (`Cache.Touch`) that throttles the next attempt for a
  TTL, while the `stale`/≈ marker stays keyed to data age (`fetched_at`) so the
  UI is still honest. `now`/Resolve deliberately ignores `attempted_at` (manual
  one-shot; a single fetch is fine) and Touch never moves `fetched_at`, so the
  ladder path is unaffected.
- stdin path suspect guard: intentionally absent. The statusline mirrors
  Claude Code's own displayed values; the ≥30pt-drop guard (`Reconcile`)
  applies only to fetched/ladder data and renders its `(suspect)` marker in
  `now`, where the marker is visible. Guarding on the statusline would make
  ccx diverge from CC's UI while hiding the reason.
- cache is a pure opaque store; its vestigial TTL machinery (`WithTTL`,
  `Fresh`, `TTL()`, `DefaultTTL`, `Entry.Age`) had zero production consumers —
  TTL judgment lives solely in the engine (`engine.DefaultTTL`) — and was
  removed along with a duplicate 120s constant. `usage.RateLimitError` /
  `parseRetryAfter` deliberately remain despite having no v1 behavioral
  consumer (the ladder treats 429 like any transient failure): they are the
  tested seeds for the deferred Backoff feature and carry no complexity the
  429 budget story doesn't already assume.
- stdin completeness is cache-relative, not shape-absolute (Fable-freeze fix):
  the original `stdinComplete` counted "any scoped model present" as complete,
  so when CC's stdin projection omitted a model outside its allowlist
  (typically Fable) while piping another (e.g. Opus), the bounded refresh
  never ran and `Overlay` served Fable frozen from the cache forever —
  including stale prev-cycle values after a reset. Completeness now means
  stdin covers every window the cache carries; a cache-known model missing
  from stdin re-opens the ≤1/TTL refresh. `ScopedProbed` (API decode only)
  lets a genuinely scoped-less plan terminate the loop, and a missing cache
  bootstraps exactly one refresh so fresh installs surface allowlist-filtered
  models at all. The stdin parser still drops `utilization: null`
  model_scoped entries (null = unknown, not 0); the refresh path then fetches
  the real value instead of rendering a guessed 0.
