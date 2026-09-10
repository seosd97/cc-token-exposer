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
  `brew upgrade`. A dev build (unparseable version) counts as older than
  any release, so it always updates. This is a *binary* update; it never
  touches OAuth credentials (unrelated to invariant #3's "no self refresh").

Removed from v1 after working implementations existed (preserved in git
history, planned to return in v2): `ccx watch` (NDJSON polling stream),
threshold notifications (`internal/notify`), history logging
(`internal/history`), credential injection (`creds/injected.go`),
`usage.Backoff`.

## Architecture

```
cmd/ccx/main.go      composition root: builds the ONE production engine and
                     injects it into commands as a `resolver` interface;
                     also composes the processRefresher (detached `ccx refresh`
                     spawner) and the embedded release-signature public key
cmd/ccx/now.go       rendering + exit-code policy
cmd/ccx/statusline.go  statusline formatting + stdin rate_limits parsing
                     (rate_limits -> schema.Snapshot -> engine.ResolveStdin).
                     Stdin is read only when it is not a terminal, so a manual
                     run never blocks. Colors are alert-only: none below 60%,
                     muted yellow from 60%, muted red above 85%; a stale line
                     renders all gray; NO_COLOR yields plain text.
cmd/ccx/refresh.go   hidden `ccx refresh` (internal): runs the ladder once,
                     discards the State — the detached statusline refresher
cmd/ccx/update.go    self-update command (thin) over internal/selfupdate
cmd/ccx-sign/        release-time checksums signer (stdlib ed25519); never
                     shipped (goreleaser builds only ./cmd/ccx)
internal/
  selfupdate/  GitHub-release self-update: Latest (releases/latest) ->
               FetchAsset -> VerifySignedChecksums (checksums.txt.sig is an
               ed25519 signature over checksums.txt by the embedded public
               key) -> VerifyChecksum (checksums.txt) -> ExtractBinary
               (tar.gz) -> Apply (atomic rename over os.Executable). Stdlib
               only; injectable http client + apiBase + platform for tests.
               Archive names follow goreleaser's `ccx_<os>_<arch>.tar.gz`
               template; checksums.txt lines are `<sha256>  <name>`.
  engine/      THE BRAIN. Resolve(ctx) and ResolveStdin(ctx, snap) ->
               *schema.State, runs the degrade ladder. Depends only on 6
               consumer-side interfaces it defines:
               CredResolver / Fetcher / Cache / TranscriptProbe / Clock /
               Refresher.
  schema/      The wire contract (State, schema_version=1). Leaf package,
               imports nothing internal. The PUBLIC CONTRACT is the JSON
               emitted by `now --json`, not the Go types (internal/ blocks
               external import by design). Snapshot is the ONE snapshot type
               end-to-end: usage decodes straight into it and engine carries
               it unmodified. The legacy seven_day_opus/fable compat aliases
               are NOT struct fields — Snapshot.MarshalJSON injects them from
               ScopedLimits at serialize time (new models stay under
               scoped_limits only).
  usage/       oauth/usage HTTP client + Reconcile consistency guard +
               Overlay gap-fill merge. Produces/operates on schema.Snapshot
               directly (no duplicated window types). Fetch returns
               usage.FetchedSnapshot{Snapshot, ScopedProbed, Drift}; decode is
               the only producer that sets ScopedProbed=true (marks "the API
               answered about scoped limits"), which the engine uses to judge
               stdin completeness. decode also records wire-drift indicators
               in Drift when the response shape deviates from what the
               pipeline expects (empty payload; a top-level window or active
               scoped entry missing resets_at; a model-scoped entry missing
               display_name; a model-scoped entry under an unknown kind).
               Indicators never block decoding — usable data still surfaces —
               and the engine carries them onto State.drift and persists them
               in the cache entry, so a silent endpoint format change is
               detectable in `now` output and `--json` instead of degrading
               into wrong numbers. The statusline deliberately does NOT
               render drift markers (it mirrors Claude Code's own values,
               same rule as the suspect guard); the marker is visible on the
               ladder path.
  creds/       credential acquisition: file > macOS keychain shell-out.
  cache/       flock-protected, atomic-write disk cache of opaque JSON.
               Entry = {fetched_at, attempted_at?, scoped_probed, drift?,
               payload}.
               ClaimRefresh(now, backoff) atomically claims a refresh slot
               (check-and-set of attempted_at under the write lock, keyed off
               the later of fetched_at/attempted_at), returning whether the
               caller may spawn a refresher. Load stat-checks the file before
               taking the flock, so a cold start with no cache file is a miss
               without locking; readEntryLocked/writeEntryLocked run only
               under a held flock (the Locked suffix is the contract).
  transcript/  last-resort fallback: parses limit-hit messages from
               ~/.claude/projects/**/*.jsonl (read-only, best-effort).
               FindTranscripts keeps only the k newest files via a bounded
               heap; ScanLatest scans newest-first and stops once a file's
               mtime can no longer hold a newer hit; ScanFile reads the last
               512KB first, falling back to a full scan. Hits whose reset has
               already passed are discarded.
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
            scoped model included — and each covered window carries a
            still-future reset time. CC's stdin projection omits scoped models
            outside its allowlist (typically Fable) and can pipe null, missing
            or already-elapsed `resets_at`, so a cache-known model missing
            from stdin — or any cached window whose reset stdin doesn't carry
            forward — is a gap that re-opens the ≤1/TTL refresh. No
            cache at all, or only a snapshot never produced by an API decode
            (`usage.Snapshot.ScopedProbed` — set only by `usage.decode`), is
            also incomplete: the first tick bootstraps one seeded refresh that
            establishes the reference set. A plan proven scoped-less
            (`ScopedProbed`, no scoped keys) — or proven without a given
            window, since coverage is keyed off what the cache carries —
            terminates the loop instead of polling forever.
3. refresh detached: when incomplete, `Cache.ClaimRefresh(now, ttl)` atomically
            claims a refresh slot (check-and-set of attempted_at under the write
            flock, keyed off the later of fetched_at/attempted_at). On success
            `Refresher.Spawn` starts a detached `ccx refresh` subprocess
            (Setsid, stdio to /dev/null) that runs the ladder
            (resolveToken → fetchWithToken → Reconcile → storeCache) and exits;
            the parent serves the overlay immediately — the statusline path
            never blocks on the network. A spawn failure falls back to one
            bounded synchronous refresh (5s budget). Because the claim is atomic,
            concurrent ticks/sessions dedupe to ≤1 spawn per TTL; a failed
            refresh inside the child still counts as an attempt (the claim
            already stamped attempted_at), so a persistently failing endpoint
            yields ≤1 spawn per TTL instead of one per tick.
 4. serve   `usage.Overlay(stdin, cache)` — per window stdin's utilization
            wins, gaps fill from cache; within one window the LATER reset time
            wins (reset boundaries only move forward between cycles), so a
            stdin window with a zero/missing or elapsed `resets_at` borrows
            the cache's still-future reset and keeps its countdown. Borrowed
            data counts as a cache contribution for the `stale`/≈ marker.
            `ExtraUsage` always comes from the fresh side — served with
            `source: "stdin"`. The `stale`/≈ marker keys off data age
            (`fetched_at`), not attempt recency: while a detached refresh is in
            flight the line honestly shows ≈; after it lands, the next tick
            serves the healed cache. No suspect guard runs here — the statusline
            mirrors Claude Code's own values; the ≥30pt-drop guard and its
            `(suspect)` marker live on the ladder (`now`), where the marker is
            visible.

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
   so the per-machine bound holds. The statusline path additionally dedupes
   fetches across concurrent ticks/sessions: `ClaimRefresh` is an atomic
   check-and-set under the write flock, so exactly one spawner wins per TTL even
   when several sessions cross the boundary at once — the old
   check→fetch→store race is closed on this path. Caveat: `now` (the manual
   ladder) deliberately ignores attempted_at, so a few one-shot `ccx now`
   invocations crossing the TTL at once can each fetch (bounded by how many
   `now`s a user actually runs; self-healing on the next store).
2. **Token hygiene.** The OAuth token is read-only and in-memory only. It must
   never appear in logs, error messages, the cache file, test fixtures, or
   `String()` output (creds redacts). The cache stores only
   `{fetched_at, attempted_at?, scoped_probed, drift?, payload}` where
   payload is a token-free usage snapshot, `attempted_at` is an optional
   refresh-attempt timestamp, and `drift` is an optional list of wire-shape
   anomaly indicators (never token material).
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
   the additive value `"stdin"` (statusline rate_limits path). `drift`
   (an optional `[]string` of wire-shape anomaly indicators) was added
   additively; it is omitted when the last live fetch matched expectations.
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
  epoch seconds, or milliseconds when large) into a usage.Snapshot and fed
  to `engine.ResolveStdin`. Never depend on it. Confirmed shape (CC
  2.1.217): `five_hour` / `seven_day` /
  `seven_day_oauth_apps` / `seven_day_opus` / `seven_day_sonnet` windows,
  `model_scoped: [{display_name, utilization|null, resets_at ISO|null}]`
  (projected by CC from the server limits[] overage-included-models
  allowlist, present only when non-empty), and `extra_usage`. Any window's
  `resets_at` may be null or missing — CC refreshes its projection only on
  its own usage fetches, so it can lag across window resets; the parser keeps
  the utilization with a zero reset and the engine backfills/heals it. The parser maps
  model_scoped by display_name into ScopedLimits (seven_day_opus only
  backfills Opus); sonnet/oauth_apps/extra_usage are ignored, consistent
  with the API path.
- Local limit-hit signal: when a limit is hit, Claude Code writes a synthetic
  transcript message (`isApiErrorMessage: true`; its content is a plain
  string or an array of typed text blocks; text like
  "You've hit your session limit · resets 4:50pm (Asia/Seoul)"). The
  transcript probe parses the reset time (am/pm, explicit IANA tz honored,
  rolled forward to the future).

## Comment policy

**No comments.** The code must stand alone: names, types, structure,
subtests, and test failure messages carry the meaning; design rationale,
invariants, contracts, and external constraints live in this file
(AGENT.md), not in code. A comment that merely describes what code does —
package docs, godocs, inline notes, section separators, labels on magic
values, test scenario notes — is deleted on sight, in production code and
tests alike. The repo is swept clean; keep it that way.

The only `//` lines allowed are compiler and tool directives (`//go:build`,
`//go:embed`, `//go:generate`, `//nolint`). When something genuinely needs
explaining, do one of these instead of writing a comment:

- Encode a contract in the name or the type (`readEntryLocked` for "caller
  holds the flock").
- Put a test's scenario in its name, a subtest name, or the failure message.
- Record the rationale, invariant, or wire fact in the relevant section of
  this file.

## Development

```sh
go build ./...
go test -race ./...   # ALL tests are deterministic: httptest for HTTP,
                      # injected Clock for time, fake resolver for commands.
go vet ./... && gofmt -l .
```

- **Never call the real API from tests or casual verification.** Live checks
  are manual, rare, and deliberate (protect the 429 budget). The one
  sanctioned live path is `TestLiveSmoke` (`internal/usage/live_test.go`): it
  is skipped unless `CCX_LIVE_TOKEN` is set, makes exactly one API call, and
  logs the decoded windows plus any drift indicators, so regular CI and local
  `go test ./...` never touch the endpoint. Run it deliberately with the
  access token from `~/.claude/.credentials.json`:
  `CCX_LIVE_TOKEN=<token> go test -run TestLiveSmoke -v ./internal/usage/`.
  A dispatch-only GitHub workflow and golden fixtures of the captured shape
  are deferred (see Roadmap).
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
- **Signing key handoff (do once before the first release):** the ed25519
  private key for release signing was generated locally and handed to the owner
  at `…/opencode/ccx-signing.key` (0600) — move it into the GitHub Actions
  secret `CCX_SIGNING_KEY` (raw base64, no newline) and then delete it; it must
  never be committed. The matching public key is embedded in
  `cmd/ccx/signkey.go`. The release workflow signs `checksums.txt` after
  goreleaser uploads it and uploads `checksums.txt.sig`. To rotate: generate a
  new keypair, update `signkey.go`, cut a release that users install manually,
  and document that old binaries stop self-updating until reinstalled.
- **Live-smoke workflow + golden fixtures (deferred):** a GitHub workflow
  (`workflow_dispatch` only, never scheduled) that runs `TestLiveSmoke` with a
  `CCX_LIVE_TOKEN` repo secret, plus golden fixtures of the captured live shape
  under `internal/usage/testdata/` pinned by a golden test, so the
  decode/reconcile/overlay pipeline stays tied to reality. Deliberately split
  out of the drift-indicator change (PR #10); until they land, the live check
  is the local, env-gated `TestLiveSmoke` only.
- **Decode-failure drift (issue #11):** a response that fails to decode at
  all (e.g. `resets_at` arriving as an epoch number) surfaces as a transient
  error and a stale serve with no `drift` entry — the one silent-degrade
  path the indicators cannot see. Plan: tolerant decode first (accept epoch
  resets like the stdin parser does and flag the fallback as drift), and
  surface decode errors as drift only if that proves insufficient.

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
  the exact 429 the tool exists to avoid. So a refresh writes an `attempted_at`
  stamp that throttles the next attempt for a TTL, while the `stale`/≈ marker
  stays keyed to data age (`fetched_at`) so the UI is still honest.
  `now`/Resolve deliberately ignores `attempted_at` (manual one-shot; a single
  fetch is fine). Today the stamp and the gate live in one atomic operation:
  `cache.ClaimRefresh` (check-and-set under the write flock) both decides a
  slot is due AND records it, replacing the old synchronous
  refresh-failure-`Touch` flow.
- statusline refresh is detached, never synchronous: the statusline path must
  not block the render on the network (Claude Code invokes it every few
  seconds; a slow endpoint would stall or kill the tick). When stdin is
  incomplete and a claim succeeds, the engine spawns a detached `ccx refresh`
  (Setsid, stdio /dev/null) that runs the ladder and stores the cache for the
  NEXT tick; the parent serves the overlay immediately. The child is started
  with `exec.Command`, not `exec.CommandContext`: the tick's timeout context
  is cancelled as soon as the statusline command returns, and a context-bound
  child would be killed with it mid-fetch. Consequences:
  (a) new installs surface allowlist-filtered scoped models one tick late
  (a one-tick cold-start cost), (b) a failed refresh still leaves an
  `attempted_at` claim so re-spawning stays ≤1/TTL, (c) the old
  check→fetch→store race was an acknowledged caveat for concurrent tickets —
  the atomic claim closes it. A spawn failure (non-Unix, resource limits)
  falls back to one bounded synchronous refresh (5s budget) so gaps still heal.
- ScopedProbed is cache metadata, not snapshot data: the flag that "the API (as
  opposed to a stdin projection) answered about scoped limits" is provenance,
  and it now lives on the cache Entry (`scoped_probed`) instead of inside the
  snapshot payload. The snapshot stays pure data; `usage.FetchedSnapshot`
  carries the flag out of decode at fetch time, and the cache carries it across
  the disk.
- one Snapshot type end-to-end: `usage.Snapshot`/`usage.Window`/`Usage`
  duplicates were removed and the whole pipeline (decode, Reconcile, Overlay,
  engine, stdin parser) operates on `schema.Snapshot`. The hand-written engine
  mapping layer disappeared. The legacy `seven_day_opus`/`seven_day_fable`
  compat aliases are now emitted by `schema.Snapshot.MarshalJSON` from
  ScopedLimits instead of struct fields, so new models never need a new field
  and internal code can't accidentally diverge aliases from scoped_limits.
- transcript probe reads are bounded: `FindTranscripts` keeps only the k newest
  files via a bounded heap (no full-tree collect+sort); `ScanLatest` stops as
  soon as a file's mtime can no longer hold a hit newer than the one found
  (a hit's timestamp never exceeds its file's mtime); `ScanFile` reads the last
  512KB first and falls back to a full scan. Probe also discards hits whose
  reset has already passed — a past limit is not an active limit. The probe is
  still last-resort-only, so the constants are tuned for "make it fast when the
  ladder bottoms out", not for correctness-critical paths.
- release integrity is anchored by a signature, not by same-release checksums:
  checksums.txt and archives come from the same GitHub release, so a tampered
  release could rewrite both. Release now uploads `checksums.txt.sig` — an
  ed25519 signature over checksums.txt by `cmd/ccx-sign` (stdlib ed25519, no
  dependency footprint) — and `ccx update` verifies it against the public key
  embedded in the binary before trusting any checksum. The private key is kept
  ONLY in the GitHub Actions secret `CCX_SIGNING_KEY` (see the key handoff note
  under Publish prep); a lost/leaked key forces a new keypair + new release and
  reinstalls for existing users. Old binaries without embedded keys simply
  error on the missing signature asset rather than skip verification.
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
  from stdin re-opens the ≤1/TTL refresh.   `ScopedProbed` (API decode only)
  lets a genuinely scoped-less plan terminate the loop, and a missing cache
  bootstraps exactly one refresh so fresh installs surface allowlist-filtered
  models at all. The stdin parser still drops `utilization: null`
  model_scoped entries (null = unknown, not 0); the refresh path then fetches
  the real value instead of rendering a guessed 0.
- completeness is reset-time-aware, and Overlay merges resets (missing 5h
  countdown fix): presence-only completeness + wholesale-stdin Overlay meant a
  piped `five_hour` whose `resets_at` was null/missing or already elapsed
  (CC's projection is refreshed only by CC's own usage fetches, so it lags
  across window resets and sends null resets for inactive scoped models)
  rendered the window with NO countdown — and never healed, because the gate
  judged the window covered while Overlay ignored the cache's valid reset.
  Now a cached window counts as covered only with a stdin window carrying a
  still-future reset (a reset-less one re-opens the ≤1/TTL refresh, bounded
  exactly like the scoped-model gap), and `Overlay` merges per window: fresh
  utilization wins, reset time takes the LATER value (boundaries only move
  forward between cycles), backfilling the zero/elapsed stdin reset from the
  cache — counted as a cache contribution, so the ≈ marker stays honest.
  Coverage keyed off the cache also means a window the cache doesn't carry
  (API never reported it, e.g. an idle plan's `five_hour`) needs no healing,
  closing the theoretical ≤1/TTL spawn loop such plans would otherwise open.
- wire drift is surfaced, not silently absorbed (drift indicators): the
  endpoint is unofficial and its shape evolves (new codename fields, null
  windows, new limits[] kinds), and a silent format change used to degrade
  the pipeline into serving plausible-but-wrong numbers with every layer
  "working fine". `usage.decode` now records drift indicators on
  `FetchedSnapshot.Drift` — empty payload; a top-level window or an active
  scoped entry missing `resets_at`; a model-scoped entry missing
  `display_name`; a model-scoped entry under an unknown `kind`. Indicators
  never block decoding (usable data still surfaces), the engine carries them
  onto `State.drift` (additive, schema_version stays 1) and persists them in
  the cache entry, so a fresh-cache or stale-cache serve still reports the
  last-seen drift; a clean fetch stores no drift and so clears it. Two
  deliberate non-indicators: the empty-payload check keys off usable data,
  not raw field presence (group-scoped `limits[]` entries and an empty
  `extra_usage` object carry no window, so a response with only those still
  counts as empty), and the legacy `seven_day_opus` reset check fires only
  when the Opus backfill actually consumed the field (an explicit `limits[]`
  Opus entry shadows it). A scoped entry's missing `resets_at` counts only
  while its percent is nonzero, since inactive scoped models legitimately
  carry null resets; the top-level windows are expected to always carry one.
  Visibility follows the suspect-guard rule: `now` (human `drift:` line +
  `--json` field) shows the markers; the statusline deliberately does not,
  because it mirrors Claude Code's own values. `TestLiveSmoke`
  (`internal/usage/live_test.go`, skipped unless `CCX_LIVE_TOKEN` is set, one
  API call per run) is the sanctioned way to check the real shape; a
  dispatch-only workflow and golden fixtures are deferred (see Roadmap).
- comments are gone entirely, not merely discouraged: the policy's exceptions
  (package one-liners, short helper godocs, test scenario notes) kept
  attracting descriptive comments that duplicated names and drifted from the
  code. The repo was swept with a go/scanner-based strip that keeps only
  compiler/tool directives, the two flock-precondition helpers became
  `readEntryLocked`/`writeEntryLocked` so the contract lives in the name, and
  every rationale the stripped comments carried now lives in this file (color
  thresholds, stdin terminal skip, cache stat pre-check, goreleaser naming,
  dev-build update rule, transcript content shapes). Tests express their
  scenario through names and failure messages.
