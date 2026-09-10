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

The same story holds for a second provider, Codex CLI's ChatGPT plan:
`--provider codex` reads `~/.codex/auth.json` read-only and queries the
endpoint Codex's own `/status` uses (`/wham/usage`), so if `codex` works on
the machine, `ccx --provider codex` works with no setup. Providers share one
engine, one wire schema and one rendering; only the credential source and the
usage client differ. The provider abstraction is deliberately bounded to plans
that share the rolling-window model (utilization % + reset time per window);
quota models that do not fit (daily request counts, monthly premium requests)
are out of scope until a schema_version 2 decision.

Positioning: local-JSONL tools (ccusage, claude-monitor) only *estimate*
limits and drift from the real lockout; linuxlewis/claude-usage uses
authoritative values but requires manually extracted browser cookies. `ccx`
gives authoritative numbers with zero setup.

**v1 scope (current, deliberately minimal): two usage commands plus
housekeeping.**

- `ccx now [--json] [--provider claude|codex|claude,codex]` — one-shot lookup
  (human or single-line JSON). With several providers each one prints as its
  own block (header line = provider name, blank-line separated), or as one
  JSON line per provider (NDJSON) with `--json`.
- `ccx statusline [--provider claude|codex|claude,codex]` — one-line output for
  the Claude Code statusline (registered via `~/.claude/settings.json` →
  `statusLine.command`). One group per provider, joined by ` │ `. A single
  configured provider renders untagged whichever it is; with several, the
  claude group stays untagged and the others carry a gray provider tag.
- `ccx refresh --provider <name>` (hidden) — the detached refresher entry
  point; flag-less `refresh` means claude.
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
cmd/ccx/main.go      composition root: builds ONE production engine per
                     provider from the provider Specs (claude.Spec(),
                     codex.Spec()) and hands commands a `providers` registry
                     (name -> providerEntry{spec, resolver}); the `resolver`
                     interface is Resolve / ResolveStdin / ResolveDetached.
                     Adding a provider is one more Spec in that list. Also
                     hosts the embedded release-signature public key
                     (signkey.go)
cmd/ccx/providers.go the registry type, comma-list parsing (`--provider`,
                     lower-cased, deduped, empty -> claude), lookup errors and
                     the statusline routing rule: a Spec with ParseStdin is
                     served via ResolveStdin (with the parsed snapshot, or nil
                     when stdin carried nothing usable), any other Spec via
                     ResolveDetached
cmd/ccx/cacheadapter.go  adapts internal/cache to the engine's Cache port
                     (a cache miss is a nil entry, not an error) and names
                     the per-provider cache file (`snapshot.json` stays
                     claude's for backward compat, `<name>.json` otherwise)
cmd/ccx/now.go       rendering + exit-code policy; resolves the selected
                     providers in parallel, prints blocks or NDJSON
cmd/ccx/statusline.go  statusline formatting + raw stdin read. Stdin is read
                     only when it is not a terminal, so a manual run never
                     blocks; the raw document is handed to each Spec's
                     ParseStdin (Claude wire knowledge lives in the provider
                     package, not here). Providers are resolved concurrently,
                     as `now` does, so a worst-case pair of sync fallbacks
                     costs one 5s budget. Colors are alert-only: none below
                     60%, muted yellow from 60%, muted red above 85%; a stale
                     line renders all gray; NO_COLOR yields plain text.
                     Groups are joined by " │ "; when more than one provider
                     is configured the non-claude groups get a gray name tag
                     (keyed off the configured list, not the rendered
                     groups, so a group that drops out for a tick never makes
                     the tag flicker), a single provider is never tagged,
                     ≈ / ⚠ login markers are per group, a no-plan group
                     (`auth: "no_plan"`) renders as a gray "no plan" marker
                     so `--provider codex` alone stays meaningful on an
                     API-key account, empty groups are dropped, unknown names
                     are skipped (never non-zero), and "⚠ ccx" appears only
                     when every group is empty.
cmd/ccx/refresh.go   hidden `ccx refresh --provider <name>` (internal): runs
                     that provider's ladder once, discards the State — the
                     detached statusline refresher. refresh_unix.go holds
                     processRefresher, the detached child spawner (an empty
                     provider name is a spawn error, never a silent Claude
                     refresh)
cmd/ccx/update.go    self-update command (thin) over internal/selfupdate
cmd/ccx-sign/        release-time checksums signer (stdlib ed25519); never
                     shipped (goreleaser builds only ./cmd/ccx)
internal/
  schema/      The wire contract (State, schema_version=1). Leaf package,
               imports nothing internal. The PUBLIC CONTRACT is the JSON
               emitted by `now --json`, not the Go types (internal/ blocks
               external import by design). Snapshot is the ONE snapshot type
               end-to-end: the provider decoders produce it and engine
               carries it unmodified. The legacy seven_day_opus/fable compat
               aliases are NOT struct fields — Snapshot.MarshalJSON injects
               them from ScopedLimits at serialize time (new models stay
               under scoped_limits only).
  provider/    The contract between a provider and the engine; imports only
               schema. Holds every type a provider produces or consumes:
               Credentials (AccessToken, AccountID, ExpiresAt; String()
               redacts) + Source + Resolver (the credential chain: a
               source's own error other than ErrNotFound/ErrNotAvailable
               surfaces verbatim as the auth-missing reason) + NoPlanError
               (a source's verdict that the account has no plan windows,
               e.g. a Codex API-key login: the engine serves it as an
               `auth: "no_plan"` error State carrying the reason, never as
               a login hint),
               FetchedSnapshot{Snapshot, ScopedProbed, Drift}, the error
               family every client returns (ErrAuth / RateLimitError /
               ErrTransient), the shared wire helpers ParseTolerantTime
               (RFC3339 | epoch s | epoch ms) and ParseRetryAfter, the
               provider-side ports (CredResolver / Fetcher /
               TranscriptProbe / StdinParser) and Spec{Name, LoginCommand,
               Creds, Fetcher, Transcript, ParseStdin} — one value per
               provider, built by the provider package, consumed by
               engine.New (name stamp + login hints + ports) and by the CLI
               (ParseStdin routing).
  provider/claude/  Everything Claude-specific, exported as Spec(). The
               oauth/usage HTTP Client: Fetch returns
               provider.FetchedSnapshot; decode is the only Claude producer
               that sets ScopedProbed=true (marks "the API answered about
               scoped limits"), which the engine uses to judge stdin
               completeness. decode also records wire-drift indicators in
               Drift when the response shape deviates from what the
               pipeline expects (empty payload; a top-level window or active
               scoped entry missing resets_at; a top-level window or a
               model-scoped entry whose utilization/percent is null, which
               is dropped as unknown rather than read as 0 — a null percent
               on an entry marked is_active: false is dropped without an
               indicator; a model-scoped entry missing display_name; a model-scoped entry under an
               unknown kind, which still surfaces but never overrides a
               weekly_scoped entry of the same display_name). Indicators
               never block decoding — data whose meaning is known still
               surfaces — and the engine carries them onto State.drift and persists
               them in the cache entry, so a silent endpoint format change is
               detectable in `now` output and `--json` instead of degrading
               into wrong numbers. The statusline deliberately does NOT
               render drift markers (it mirrors Claude Code's own values,
               same rule as the suspect guard). The credential sources:
               file > macOS keychain shell-out, chained by
               DefaultCredentials(). The statusline stdin parser
               ParseStatuslineStdin (rate_limits -> schema.Snapshot; the
               Spec's ParseStdin) — tolerant: `used_percentage` or
               `utilization`; resets_at as ISO or epoch; `model_scoped`
               keyed by `display_name`; `utilization: null` entries dropped
               (unknown ≠ 0); `seven_day_opus` backfills Opus only.
               live_test.go is the env-gated smoke (`CCX_LIVE_TOKEN`).
  provider/codex/  The Codex provider, exported as Spec() (no Transcript, no
               ParseStdin — stdin rate_limits describe the Claude plan):
               AuthSource (provider.Source over `~/.codex/auth.json`,
               CODEX_HOME honored; an API-key login yields NoPlanError)
               and Client (provider.Fetcher over
               `/wham/usage`). Decode maps windows by length, lifts
               per-model buckets into ScopedLimits and records drift
               indicators; testdata/ holds a redacted live capture that pins
               the decode as a golden test; live_test.go is the env-gated
               smoke (`CCX_LIVE_CODEX_TOKEN` + `CCX_LIVE_CODEX_ACCOUNT`).
  engine/      THE BRAIN. Resolve(ctx), ResolveStdin(ctx, snap) and
               ResolveDetached(ctx) -> *schema.State. One `resolve(ctx,
               mode)` runs the degrade ladder with its refresh step in one of
               two modes: inline (fetch on this call — `now` and the refresh
               child) or detached (`detachedRefresh`: claim a slot, spawn the
               refresher, and only if the spawn fails — a nil Refresher
               counts as a failed spawn — fall back to one bounded
               synchronous fetch whose own error, a 401 included, feeds the
               ladder exactly like an inline fetch; the claim is skipped
               outright when the entry just loaded shows a fetch or attempt
               within the TTL, so denied ticks take no lock). ResolveStdin
               is overlay plus the
               same detachedRefresh when stdin is incomplete; a nil stdin
               delegates to the detached ladder, so a statusline tick never
               fetches inline. The snapshot policies live
               here too: reconcile (the ≥30pt-drop suspect guard) and
               overlay (stdin-over-cache merge), both unexported — the
               engine is their only consumer. Consumes a provider.Spec for
               the provider-side ports (an unnamed Spec leaves `provider`
               empty and makes login hints command-less; production always
               passes a named Spec) and defines its own runtime ports: Cache
               (Load returns a nil entry on a miss; StoreLimitHit persists
               the transcript probe result) / Refresher / Clock. The
               transcript probe runs ONLY on the inline ladder (`now`, the
               refresh child); the detached ladder serves the persisted
               `limit_hit` instead, so a statusline tick never walks the
               transcript tree. Imports only provider and schema.
  cache/       flock-protected, atomic-write disk cache of opaque JSON, one
               file per provider (`snapshot.json` stays claude's for
               backward compat; NewNamed(name) adds `<name>.json` beside it,
               each with its own lock).
               Entry = {fetched_at, attempted_at?, scoped_probed, drift?,
               limit_hit?, payload}. DefaultPathFor falls back to
               `os.TempDir()/cc-token-exposer-<uid>/` when the user cache
               dir cannot be resolved, so cache-first holds even without
               HOME.
               Update(fn) is the locked read-modify-write primitive: it
               takes the write flock, hands fn the current Entry (zero on a
               miss or on an undecodable file), and writes it back only when
               fn says so; ClaimRefresh and the limit-hit merge are thin
               callers. Store replaces the entry wholesale under the same
               lock without reading first, so it also repairs a corrupt
               file. Every lock and load first Lstat's the cache directory
               and refuses a symlink or a directory owned by another user
               (ErrUnsafeDir), which closes the pre-created-/tmp attack on
               the TempDir fallback. ClaimRefresh(now, backoff)
               atomically claims a refresh slot (check-and-set of
               attempted_at, keyed off the later of fetched_at/attempted_at),
               returning whether the caller may spawn a refresher; a denied
               claim costs no write. Load stat-checks the file before taking
               the flock, so a cold start with no cache file is a miss
               without locking; readEntryLocked/writeEntryLocked run only
               under a held flock (the Locked suffix is the contract). An
               entry that fails to decode is reported by Load as ErrCorrupt
               and treated by Update as a miss, so the next Store or
               ClaimRefresh rewrites the file instead of leaving the cache
               broken; genuine read errors (permissions) still surface.
  transcript/  last-resort fallback: parses limit-hit messages from
               ~/.claude/projects/**/*.jsonl (read-only, best-effort).
               FindTranscripts keeps only the k newest files via a bounded
               heap; ScanLatest scans newest-first and stops once a file's
               mtime can no longer hold a newer hit; ScanFile reads the last
               512KB first, falling back to a full scan. Hits whose reset has
               already passed are discarded. Wired in as claude.Spec()'s
               Transcript.
  selfupdate/  GitHub-release self-update: Latest (releases/latest) ->
               FetchAsset -> VerifySignedChecksums (checksums.txt.sig is an
               ed25519 signature over checksums.txt by the embedded public
               key) -> VerifyChecksum (checksums.txt) -> ExtractBinary
               (tar.gz) -> Apply (atomic rename over os.Executable). Stdlib
               only; injectable http client + apiBase + platform for tests.
               Archive names follow goreleaser's `ccx_<os>_<arch>.tar.gz`
               template; checksums.txt lines are `<sha256>  <name>`.
```

Engine's degrade ladder (Resolve never fails — it always returns a State
carrying the best known truth plus its freshness):

1. Cache fresh (within TTL 120s) → serve from disk, zero API calls.
2. Live fetch OK → `engine.reconcile(prev, fresh)` → store cache → serve.
3. 429 / 5xx / network → serve stale cache (`stale: true`, `stale_age`).
4. 401 → re-read credentials once, retry once if the token changed and
   serve the retry's own outcome (a transient failure on the retry is
   step 3, not an expired token); otherwise `auth: "expired"` (still with
   stale data if available).
5. No cache at all → transcript limit-hit probe (inline ladder only; the
   detached ladder reads the persisted `limit_hit` instead) → else error
   State.

ResolveDetached (statusline, providers without a stdin projection) is the
ladder with the synchronous fetch replaced by the detached refresh: a fresh
cache is served as-is (zero IO); otherwise credentials are checked exactly like
the ladder (missing/expired short-circuit to the same degraded States with no
spawn), then `Cache.ClaimRefresh` + `Refresher.Spawn` heal the cache for the
NEXT tick while the stale cache is served (≈), or — on a cold start with no
cache — an auth-ok error State ("usage refresh in progress; no cache yet")
that the statusline renders as an empty group. A spawn failure falls back to
the bounded synchronous refresh. `now` never uses it (the manual ladder may
fetch inline).

ResolveStdin (statusline only) is a four-stage pipeline. Design principle: the
disk cache is the reference set of windows the plan reports; the bounded
refresh exists to HEAL GAPS in what is served, never to keep the cache warm —
a cached window refreshes only when the served line depends on it and stdin
does not carry it.

1. parse   `rate_limits` → `schema.Snapshot` (tolerant: `used_percentage` or
           `utilization`; resets_at as ISO or epoch; `model_scoped` keyed by
           `display_name`; `utilization: null` entries dropped — unknown ≠ 0;
           `seven_day_opus` backfills Opus only). No usable window → nil → the
           detached ladder handles the tick (never an inline fetch).
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
            (`provider.FetchedSnapshot.ScopedProbed` — set only by the provider decoders), is
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
            (resolveToken → fetchWithToken → reconcile → storeCache) and exits;
            the parent serves the overlay immediately — the statusline path
            never blocks on the network. A spawn failure falls back to one
            bounded synchronous refresh (5s budget). Because the claim is atomic,
            concurrent ticks/sessions dedupe to ≤1 spawn per TTL; a failed
            refresh inside the child still counts as an attempt (the claim
            already stamped attempted_at), so a persistently failing endpoint
            yields ≤1 spawn per TTL instead of one per tick.
 4. serve   `engine.overlay(stdin, cache, now)` — per window stdin's
            utilization wins and gaps fill from cache, with two reset rules:
            a stdin window with a zero/missing `resets_at` borrows the
            cache's later, still-live reset and keeps its countdown, while a
            stdin window whose `resets_at` has already ELAPSED belongs to a
            closed cycle and yields wholesale (utilization included) to a
            cache window whose reset is still in the future AND later by
            more than the one-minute jitter tolerance — the healed value,
            never a hybrid of old utilization and new countdown. Within the
            tolerance the two resets describe the same boundary seen through
            server-side jitter, so only the reset is borrowed. A later cache
            reset against a still-live stdin reset is borrowed only, and a
            cache reset that has itself elapsed is never borrowed. Borrowed
            or substituted data counts as a cache contribution for the
            `stale`/≈ marker.
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
`attempted_at`. The attempt is recorded by the atomic `Cache.ClaimRefresh`
claim before the fetch runs, so a failing/429ing endpoint does NOT re-fetch
on every statusline tick; a stdin
snapshot covering every cache-known window means zero calls, while a
cache-known scoped model missing from stdin (e.g. Fable) costs ≤1 call per
TTL. A plan proven to have no scoped limits (`ScopedProbed`, no scoped keys)
terminates the loop instead of polling forever. Both paths share the flock'd cache,
   so the per-machine bound holds. Each provider has its own cache file and
   lock, so the bound is per provider per machine; the Codex endpoint's 429
   policy is unknown and gets the same TTL/cache-first treatment. The statusline path additionally dedupes
   fetches across concurrent ticks/sessions: `ClaimRefresh` is an atomic
   check-and-set under the write flock, so exactly one spawner wins per TTL even
   when several sessions cross the boundary at once — the old
   check→fetch→store race is closed on this path. Caveat: `now` (the manual
   ladder) deliberately ignores attempted_at, so a few one-shot `ccx now`
   invocations crossing the TTL at once can each fetch (bounded by how many
   `now`s a user actually runs; self-healing on the next store).
2. **Token hygiene.** The OAuth token is read-only and in-memory only. It must
   never appear in logs, error messages, the cache file, test fixtures, or
   `String()` output (creds redacts). The Codex account id, user id and
   e-mail (present in `auth.json` and in the `/wham/usage` response) are
   treated the same way: `Credentials.String()` omits AccountID, the golden
   fixture is redacted, and neither reaches the cache. The cache stores only
   `{fetched_at, attempted_at?, scoped_probed, drift?, limit_hit?, payload}`
   where payload is a token-free usage snapshot, `attempted_at` is an
   optional refresh-attempt timestamp, `drift` is an optional list of
   wire-shape anomaly indicators and `limit_hit` is the transcript probe's
   last verdict (a reset time and a fixed message; never token material).
3. **No self refresh.** Never run an OAuth refresh grant. Refresh tokens may
   rotate; consuming one can invalidate Claude Code's stored refresh token and
   break the user's login. On expiry: re-read `~/.claude/.credentials.json`
   (Claude Code refreshes it itself), else surface `auth: "expired"`. The
   same rule covers Codex: never call `auth.openai.com/oauth/token`; re-read
   `~/.codex/auth.json` (Codex refreshes it on its own runs) or surface
   `auth: "expired"` with the `codex login` hint.
4. **Required request headers.** Claude: `Authorization: Bearer <token>`,
   `anthropic-beta: oauth-2025-04-20`, and `User-Agent: claude-code/<ver>`.
   Without the claude-code User-Agent the endpoint applies a far stricter
   429 bucket. The `<ver>` constants (claude-code, codex_cli_rs) track the
   last CLI version whose response shape was confirmed live; bump them
   when recapturing. claude-code/2.1.217 was confirmed live on 2026-09-10
   (`TestLiveSmoke`, no drift). Codex: `Authorization: Bearer <access_token>`,
   `ChatGPT-Account-Id: <account_id>`, `originator: codex_cli_rs`,
   `User-Agent: codex_cli_rs/<ver>`. The live check on 2026-09-10 returned
   200 without originator/User-Agent, so those two mirror Codex by
   convention rather than by necessity; keep them anyway so ccx is
   indistinguishable from the CLI it piggybacks on.
5. **Never blank-screen.** Every failure degrades to "last known truth +
   freshness marker" (`≈` prefix for stale, `⚠ login` for auth, `⛔` for
   limit-hit, `no plan` for a login without plan limits). statusline must
   never exit non-zero or print nothing.
6. **Wire stability.** `schema.State` (schema_version=1) is the public
   contract. Changes must be additive (new optional fields); breaking changes
   bump the version. `utilization` is float64 on the wire (the real API sends
   fractionals); round only at display time. `scoped_limits`
   (map[string]*Window) was added additively; `seven_day_opus` /
   `seven_day_fable` remain as aliases for backward compat. `source` gained
   the additive value `"stdin"` (statusline rate_limits path). `auth` gained
   the additive value `"no_plan"` (credentials present, but the account has
   no plan windows; the reason travels in `error`). `drift`
   (an optional `[]string` of wire-shape anomaly indicators) was added
   additively; it is omitted when the last live fetch matched expectations.
   A window's `resets_at` is omitted (`omitzero`) when the reset time is
   unknown instead of serializing a zero-date sentinel; consumers must
   treat an absent `resets_at` as unknown.
7. **Transcript files are read-only.** Never write under `~/.claude/` or
   `~/.codex/`.

## Data source notes

- Endpoint: `GET https://api.anthropic.com/api/oauth/usage` — **unofficial**,
  may change or vanish. Response carries legacy top-level `five_hour` /
  `seven_day` / `seven_day_opus` windows, each `{utilization: 0-100 float,
  resets_at}`, plus `extra_usage`.
- **Per-model weekly limits live in a newer `limits[]` array**, not in
  top-level fields. Each entry is `{kind, group, percent, resets_at, scope,
  is_active}`; scoped-model limits are `kind: "weekly_scoped"` keyed by
  `scope.model.display_name` (e.g. "Opus", "Fable", "Sonnet"). `claude.decode`
  lifts **all** `weekly_scoped` entries into `Snapshot.ScopedLimits`
  (`map[string]*Window`) dynamically — no model name is hardcoded. The legacy
  top-level `seven_day_opus` is used as a fallback when `limits[]` is absent
  (older API/CC versions). The wire contract (`schema.Snapshot`) keeps
  `seven_day_opus` / `seven_day_fable` as additive aliases of
  `ScopedLimits["Opus"]` / `ScopedLimits["Fable"]` for backward compat with
  existing `--json` consumers; new models appear only in `scoped_limits`.
  As of 2026-07 the live response also sends `seven_day_sonnet`,
  `seven_day_cowork`, `seven_day_omelette` (all null) and several codename
  fields (`tangelo`, `iguana_necktie`, etc.) — all ignored. As of
  2026-09-10 `limits[]` carried only the active scoped model (Fable):
  inactive models are absent from the array, not null-percent entries, and
  `extra_usage` was an empty object.
- Token location: `~/.claude/.credentials.json` (`claudeAiOauth.accessToken`,
  `expiresAt` epoch-ms); some machines store it in the macOS Keychain item
  "Claude Code-credentials" instead (creds falls back to `security` CLI).
- Known server bug (anthropic/claude-code#52497): the weekly counter can drop
  implausibly mid-cycle. `engine.reconcile` keeps the previous value and sets
  `suspect: true` when utilization falls ≥30 points within an unchanged
  reset cycle.
- statusline stdin: Claude Code pipes session JSON. A `rate_limits` field
  appears intermittently across versions (#40094); when present it is parsed
  tolerantly (`used_percentage` or `utilization`; resets_at as ISO string or
  epoch seconds, or milliseconds when large) into a schema.Snapshot and fed
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
- Codex endpoint: `GET https://chatgpt.com/backend-api/wham/usage` —
  **unofficial**, the call behind Codex CLI's `/status` and `/usage` (the
  base URL is Codex's `chatgpt_base_url` default; ccx hardcodes it). Shape
  confirmed live on 2026-09-10 (codex-cli 0.153.4, pro plan) and pinned by
  `internal/provider/codex/testdata/usage_2026-09-10.json`:
  `rate_limit: {allowed, limit_reached, primary_window, secondary_window}`
  where a window is `{used_percent, limit_window_seconds,
  reset_after_seconds, reset_at (epoch s)}`; `additional_rate_limits[]` of
  `{limit_name, metered_feature, normal_model_slug, rate_limit}` — per-model
  pools (e.g. "GPT-5.3-Codex-Spark") that carry BOTH a 5h and a weekly
  window, present even at 0%; plus `plan_type`, `credits`, `spend_control`,
  `model_usage`, `rate_limit_reset_credits`, `code_review_rate_limit`,
  `promo`, and the account's `user_id` / `account_id` / `email` (all
  ignored; the last three are PII). `primary_window` is NOT always the 5h
  window: an idle plan reports the weekly window alone as primary with
  `secondary_window: null` (217 of ~1,000 local session-log samples, and the
  live capture), so decode maps by `limit_window_seconds` (18000 →
  `five_hour`, 604800 → `seven_day`); an unknown length falls back to the
  position and records drift. `reset_at` wins over `reset_after_seconds`
  (relative to the fetch clock). Bucket windows map their WEEKLY window into
  `scoped_limits[limit_name]`, mirroring Claude's per-model weekly limits; a
  bucket's 5h window is not represented (one window per scoped key).
  The Codex protocol shape seen in `~/.codex/sessions/**/*.jsonl`
  `token_count` events (`primary/secondary` with `window_minutes` and
  `resets_at`) is accepted by the same decoder for tolerance, but the session
  logs are not read (a Codex rollout probe is a deferred option; Codex is
  migrating those jsonl rollouts to sqlite via `migrate-rollouts`).
- Codex credentials: `~/.codex/auth.json` (0600; `CODEX_HOME` relocates it):
  `{auth_mode: "chatgpt"|"apikey", OPENAI_API_KEY, tokens: {access_token,
  refresh_token, id_token, account_id}, last_refresh}`. The access token is
  a JWT (≈10-day lifetime) whose `exp` claim is the pre-check expiry and
  whose `https://api.openai.com/auth.chatgpt_account_id` claim backs up a
  missing `tokens.account_id`; the id_token expires hourly and is ignored.
  `auth_mode: "apikey"` (or an API key without tokens) means Codex is on
  usage-based billing with no plan windows — surfaced as a
  `provider.NoPlanError` ("codex: logged in with an API key; plan limits do
  not apply"): `now` prints it as an error State with `auth: "no_plan"`
  (exit 1) and the statusline renders a gray `codex no plan` marker, instead
  of a misleading login hint.
- Codex TUI: its own `tui.status_line` has `five-hour-limit` /
  `weekly-limit` items and no external-command hook, so ccx's Codex value is
  in Claude Code's statusline (Codex driven from inside Claude Code via the
  codex plugin) and in `ccx now`, not inside Codex.
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
  sanctioned live path is `TestLiveSmoke` (`internal/provider/claude/live_test.go`): it
  is skipped unless `CCX_LIVE_TOKEN` is set, makes exactly one API call, and
  logs the decoded windows plus any drift indicators, so regular CI and local
  `go test ./...` never touch the endpoint. Run it deliberately with the
  access token from `~/.claude/.credentials.json`:
  `CCX_LIVE_TOKEN=<token> go test -run TestLiveSmoke -v ./internal/provider/claude/`.
  The Codex counterpart is `TestLiveSmokeCodex` (`internal/provider/codex/live_test.go`,
  skipped unless both `CCX_LIVE_CODEX_TOKEN` and `CCX_LIVE_CODEX_ACCOUNT` are
  set — `tokens.access_token` / `tokens.account_id` from `~/.codex/auth.json`):
  `CCX_LIVE_CODEX_TOKEN=<token> CCX_LIVE_CODEX_ACCOUNT=<id> go test -run
  TestLiveSmokeCodex -v ./internal/provider/codex/`. Codex also has a redacted golden
  fixture (`internal/provider/codex/testdata/`) pinned by a decode test; when the live
  shape changes, recapture with the curl in the Codex data-source notes,
  redact `user_id` / `account_id` / `email`, and update the golden test.
  A dispatch-only GitHub workflow and Claude golden fixtures are deferred
  (see Roadmap).
- Commands take a `resolver` interface; test command behavior (rendering,
  exit codes, flag handling) with a fake resolver returning canned States —
  see `cmd/ccx/commands_test.go`.
- Ports live where the contract is: the engine defines its runtime ports
  (Cache / Refresher / Clock) and consumes the provider-side ports
  (CredResolver / Fetcher / TranscriptProbe / StdinParser) from
  `internal/provider`, the contract package every provider implements.
  Provider packages export a `Spec()` and no interfaces of their own.
  Keep it that way.
- Dependencies are minimal by policy: cobra + gofrs/flock + stdlib. Adding a
  dependency needs a strong reason.
- Local install for dogfooding: `go install ./cmd/ccx` → `~/go/bin/ccx`; the
  user's statusline points at that path.

## Exit codes

- `now`: 0 on a snapshot State; 1 on an error State (message already printed
  to stdout, `errSilentExit` suppresses duplicate stderr output). A
  data-bearing State with broken auth still exits 0, but its footer carries
  `⚠ no credentials` / `⚠ token expired`. With several
  providers every block prints and the exit code is 1 if ANY of them is an
  error State. An unknown `--provider` name is a usage error (1, message on
  stderr, nothing on stdout).
- `statusline`: always 0 — it must never break the statusline. Unknown
  provider names are skipped, not reported. An account without plan limits
  (Codex API-key login) renders a `no plan` marker, not `⚠ login`.
- `refresh`: exactly one provider; unknown name or a list is an error (only
  the detached child ever sees it).

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
- **Persisted auth verdict (deferred, from the 2026-09 review):** the
  detached ladder still resolves credentials in the parent on every stale
  tick (a `security` shell-out on keychain-only Macs while a refresh is in
  flight or the endpoint is failing), and the stdin ladder never reads them
  and so can never render `⚠ login`, spawning one failing child per TTL
  instead. The general fix mirrors the transcript probe: the refresh child
  persists its auth verdict (ok / missing / expired + reason) into the cache
  entry, both statusline ladders render `⚠ login` from that, and the parent
  takes the claim before any credential IO. Not done yet because it widens
  the cache contract (invariant #2) and the ≤1/TTL child cost is bounded.
- **Statusline routing inside the engine (deferred):** an
  `engine.ResolveStatusline(ctx, doc)` applying its own Spec.ParseStdin
  would let the CLI registry go back to `map[string]resolver`, drop
  `providerEntry`, and put the routing rule beside the ladders it selects.
- **Shared HTTP client skeleton + drift vocabulary (deferred):** the two
  provider clients duplicate ~80 lines (options, Do, drain, status switch)
  and spell the same drift condition differently (`five_hour missing
  resets_at` vs `primary_window missing reset`); a `provider.Do` helper and
  shared indicator constructors are the natural home when the Backoff
  feature lands.
- **Live-smoke workflow + golden fixtures (deferred):** a GitHub workflow
  (`workflow_dispatch` only, never scheduled) that runs `TestLiveSmoke` with a
  `CCX_LIVE_TOKEN` repo secret, plus golden fixtures of the captured live shape
  under `internal/provider/claude/testdata/` pinned by a golden test, so the
  decode/reconcile/overlay pipeline stays tied to reality. Deliberately split
  out of the drift-indicator change (PR #10); until they land, the live check
  is the local, env-gated `TestLiveSmoke` only.

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
  snapshot payload. The snapshot stays pure data; `provider.FetchedSnapshot`
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
  removed along with a duplicate 120s constant. `provider.RateLimitError` /
  `ParseRetryAfter` deliberately remain despite having no v1 behavioral
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
  "working fine". `claude.decode` now records drift indicators on
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
  (`internal/provider/claude/live_test.go`, skipped unless `CCX_LIVE_TOKEN` is set, one
  API call per run) is the sanctioned way to check the real shape; a
  dispatch-only workflow and golden fixtures are deferred (see Roadmap).
- provider abstraction is bounded and lives at the seams, not in a new layer:
  the engine was already provider-neutral behind its ports, so adding Codex
  meant (a) widening `Fetcher` to take `*creds.Credentials` (tenant header),
  (b) an `engine.Provider{Name, LoginCommand}` descriptor that stamps
  `State.provider` (additive; omitted only on States no engine produced) and
  builds login hints, (c) one engine + one cache file per provider, and (d) a
  `providers` map in the composition root selected by `--provider`. Claude's
  packages keep their names (`creds`/`usage`/`transcript`) and also hold the
  shared types; `internal/codex` holds only what differs. A generic
  "windows[]" schema was rejected: Claude and Codex share the rolling
  5h/7d + per-model-weekly model exactly, and quota models that don't fit
  (Gemini daily requests, Copilot monthly premium requests) would force a
  schema_version bump for no current consumer.
- Codex windows map by length, never by position: session logs and the live
  capture both show `primary_window` carrying the weekly window alone when
  the 5h window is idle. Positional mapping would have rendered a week's
  usage as the 5h gauge. Unknown lengths still surface (positional fallback +
  drift) rather than being dropped.
- Codex per-model buckets become scoped_limits, not drift: the plan
  (docs/CODEX_SUPPORT.md D8) said "don't map, flag as drift", but the live
  shape shows `additional_rate_limits` is a steady-state per-model pool with
  5h + weekly windows present even at 0% — flagging it would print `drift:`
  on every `now`. Mapping the weekly window under `limit_name` matches
  Claude's per-model weekly limits semantically and reuses every consumer.
  Known cost: the statusline renders the pool even at 0% with a long label
  (`✧ gpt-5.3-codex-spark ▯▯▯▯▯ 0%`); hiding 0% scoped rows or shortening
  labels is an open display decision that would also touch Claude's rows.
- statusline groups: the claude group is untagged so the default output stays
  byte-identical (regression tests unchanged), other groups get a gray name
  tag only when more than one provider is configured — a lone provider is
  self-evident, so `--provider codex` alone renders exactly like claude
  alone; the tag keys off the configured list rather than the rendered
  groups so a cold-start or transient empty group never makes it flicker —
  and stdin rate_limits are routed only to claude because they describe
  the Claude plan. Non-claude providers use ResolveDetached so a second
  provider never adds network latency to a tick — the same "detached, never
  synchronous" rule the stdin path follows.
- the Codex live capture is the source of truth for its decoder: unlike
  Claude, the Codex client was written from a real response (2026-09-10),
  redacted and checked in as a golden fixture, because the shape had only
  been inferred from binary strings until then. The capture also showed the
  response carries the account's e-mail/ids, which is why redaction is part
  of the recapture procedure, and that `originator`/`User-Agent` are not
  enforced (200 without them) — mirrored anyway.
- ParseTolerantTime / ParseRetryAfter moved into the shared package (then
  `usage`, now `provider`) because the Codex
  decoder needed the same epoch-or-RFC3339 tolerance the stdin parser had;
  one implementation, tested once, used by both providers.
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
- the provider contract is its own package and Claude no longer hosts the
  shared types (layout refactor, 2026-09): `internal/usage` and
  `internal/creds` had become a grab-bag — the Claude HTTP client, the
  credential chain, the provider-neutral error/snapshot types and the
  engine's merge policies all lived under Claude's names, so `internal/codex`
  imported Claude's packages to reach the shared contract; the stdin
  `rate_limits` parser (Claude wire knowledge) lived in `cmd/ccx` and
  duplicated the Opus-backfill rule of the API decoder; the cache adapter
  lived inside `engine`, which therefore imported `internal/cache` against
  the "engine depends only on its ports" rule; and `main.go` hand-wired each
  provider. Now `internal/provider` holds the contract (Credentials / Source
  / Resolver, FetchedSnapshot, ErrAuth / RateLimitError / ErrTransient,
  ParseTolerantTime / ParseRetryAfter, the provider-side ports and `Spec`),
  `provider/claude` and `provider/codex` hold only what differs and each
  exports `Spec()`, the stdin parser moved into `provider/claude` as the
  Spec's ParseStdin so the statusline routes by `ParseStdin != nil` instead
  of by provider name, `reconcile`/`overlay` moved into `engine` as
  unexported policies (the engine is their only consumer), the three engine
  entry points share one `resolve(ctx, mode)` plus one `detachedRefresh`
  helper instead of three copies of the ladder prefix and the
  claim→spawn→fallback block, the engine's Cache port reports a miss as a
  nil entry (the never-consumed ErrNoCache sentinel is gone), the cache
  adapter moved to the composition root, `cache.Update(fn)` became the
  single locked read-modify-write primitive behind Store and ClaimRefresh,
  and `main.go` builds every engine from a `[]provider.Spec`. Behavior is
  unchanged: every pre-existing test passes with only package and
  constructor renames. This supersedes the "Claude's packages keep their
  names and also hold the shared types" note above.
- overlay treats an ELAPSED stdin reset as a closed cycle: the stdin-over-cache
  merge used to let stdin's utilization always win and only borrow the LATER
  reset, so after a window boundary a lagging Claude Code projection (80%,
  reset ten minutes ago) glued its old utilization onto the cache's new
  countdown (3%, reset in 4h50m) and rendered "80% ↻ 4h50m" — while the
  heal fetch that had just retrieved the 3% was thrown away. `overlay` now
  takes `now`: a stdin window whose reset has elapsed yields wholesale to a
  cache window whose reset is still in the future; a zero/missing stdin reset
  still only borrows the reset (the utilization is current, CC just did not
  project the boundary); a later cache reset against a still-live stdin
  reset is borrowed only, because server-side reset jitter must not let an
  older cache value override a live projection.
- nil stdin is served by the detached ladder: when Claude Code pipes no usable
  `rate_limits`, `ResolveStdin(nil)` used to fall back to the inline ladder,
  the one remaining place a statusline tick could block on the network for
  up to the client timeout. It now delegates to the detached ladder, so the
  statusline never fetches inline in any state; the cost is one tick of
  "⚠ ccx" on a cold start, the same trade-off the stdin path already
  accepted.
- the transcript probe is a throttled, persisted data source, not a per-tick
  fallback: the probe walked `~/.claude/projects` (216 jsonl files / 168MB on
  the dev machine, 22MB of tail reads per probe) on every degraded
  statusline tick, and on the auth-broken statusline its result was never
  even rendered (`⚠ login` wins). Now only the inline ladder (`now`, the
  refresh child) probes, and it persists the result — nil included, to clear
  a stale hit — through the engine's `StoreLimitHit` port into the cache
  entry's `limit_hit` (opaque JSON in `cache.Entry`, decoded by the adapter
  in cmd/ccx). The detached ladder serves the persisted hit when its reset is
  still in the future and otherwise ignores it; a successful fetch replaces
  the entry and so clears it. The probe therefore runs at most once per
  refresh claim on the statusline path.
- the Claude decoder no longer decodes into the public schema types (closes
  issue #11): `apiResponse` decoded straight into `schema.Window` with a
  `time.Time` reset, so a single `resets_at` arriving as an epoch number or
  an unexpected string failed the whole response — the one silent-degrade
  path the drift indicators could not see. The decoder now uses local wire
  structs with `json.RawMessage` resets parsed by `ParseTolerantTime` (the
  shape codex already had) and adds two indicators, `<label> resets_at is
  numeric` and `<label> resets_at unparseable`, next to the existing
  `missing resets_at`; usable data always surfaces and the public schema is
  decoupled from the endpoint shape.
- `resets_at` is omitted when unknown: `schema.Window.ResetsAt` carried
  `json:"resets_at"` on a non-pointer `time.Time`, so a reset-less stdin
  window reached `now --json` as `"0001-01-01T00:00:00Z"`. The tag is now
  `omitzero`; absent means unknown. Additive, schema_version stays 1.
- no in-process credential re-read on expiry: `resolveToken` used to call
  `Resolve()` a second time microseconds after the first when the token was
  expired, which could only return the same bytes (and doubled the keychain
  shell-out on macOS). The re-read that matters is the one after a 401 in
  `fetchWithToken`, which stays; across runs every invocation reads the
  credential file afresh, which is how Claude Code's own refresh is picked
  up.
- `now` names a broken login even while showing last-known data: a
  transcript or stale-cache State with `auth: missing/expired` rendered only
  the data and `source:` line, so a user whose token had vanished saw a
  plausible screen and exit 0. The footer now appends `⚠ no credentials` /
  `⚠ token expired`; the exit code stays 0 because the State does carry
  data.
- the cache never silently disappears: when `os.UserCacheDir()` failed (no
  HOME / XDG_CACHE_HOME) the composition root wired a nil cache, so `now`
  fetched on every call and the codex statusline group stayed empty with no
  diagnostic. `cache.DefaultPathFor` now falls back to
  `os.TempDir()/cc-token-exposer-<uid>/` and `NewNamed` cannot fail.
- the engine has no default provider: `New` used to substitute the Claude
  name and login command for an unnamed Spec, the one place the engine knew
  a provider name. It no longer does; an unnamed Spec yields an empty
  `provider` and command-less login hints, production always passes a named
  Spec, and the engine tests set the name in their helpers.
- the cache repairs itself instead of staying broken: routing `Store` through
  `cache.Update` (layout refactor) made `Store`, `ClaimRefresh` and
  `StoreLimitHit` return the decode error of an undecodable entry, so a
  0-byte file (crash during a rename without fsync), a foreign-shape entry
  (downgrade) or a hand edit left `now` fetching live on every call — the
  store error is deliberately discarded, so nothing said so — and the
  statusline never claiming, never spawning and never falling back. Before
  the refactor `Store` overwrote blindly, so the first successful fetch
  repaired the file. `Update` now treats `ErrCorrupt` like a miss (an empty
  Entry) while genuine read errors still surface; `Load` reports
  `ErrCorrupt` so callers can tell corruption from a miss. Pinned by
  `TestUpdateRepairsCorruptEntry` / `TestClaimRefreshRepairsCorruptEntry`.
- the rotated-token retry's outcome is what gets served: after a 401,
  `fetchWithToken` used to return the ORIGINAL 401 whenever the retry with a
  rotated token failed, so a network blip on the retry rendered
  `auth: "expired"` and `⚠ token expired` although the new token was fine.
  The retry's own error is returned now; a transient failure degrades to
  stale cache with `auth: "ok"` like any other transient failure.
- a nil Refresher is a failed spawn: the detached ladder returned early when
  no Refresher was wired, so such an engine never refreshed and never fell
  back — silently. The claim is now taken regardless and a missing Refresher
  takes the same bounded synchronous fallback a failed spawn does;
  production always wires `processRefresher`, so this only hardens the port
  contract.
- null is unknown on the API path too: `apiWindow.Utilization` and
  `limitEntry.Percent` were plain float64, so a `null` decoded as 0 with no
  drift indicator — the last silent-degrade path after issue #11 — while
  the stdin parser had always dropped `utilization: null` as unknown. Both
  are pointers now: a null top-level window (or legacy `seven_day_opus`) is
  dropped and flagged `<label> utilization is null`, a null-percent scoped
  entry is never lifted and is flagged `scoped limits "<name>" percent is
  null` unless it carries `is_active: false` — an inactive model has nothing
  to heal, so a null there is a plausible steady state rather than drift,
  the same reading the reset check applies to inactive entries. A null on an
  active or unmarked entry stays an indicator. The live smoke on 2026-09-10
  decoded the current shape with no indicator at all: `limits[]` carried
  only the active scoped model (Fable 15%), so inactive models are absent
  from the array rather than null-percent entries.
- a known scoped kind wins a display_name collision: `decodeScopedLimits`
  keyed the map by display_name only, so an entry of an unknown kind (e.g. a
  future `daily_scoped`) for a model that also has a `weekly_scoped` entry
  overwrote it or not depending on array order. `weekly_scoped` now wins
  regardless of order; unknown kinds still surface (with their drift
  indicator) when no weekly entry exists for the name, so a kind rename
  degrades to a marked value rather than a vanished model.
- no-plan credentials are their own auth status: a Codex API-key login was
  surfaced as `auth: "missing"` with the reason in `error`, which `now`
  rendered honestly but the statusline rendered as `codex ⚠ login` forever —
  the user IS logged in. Serving it as an auth-ok error State was tried
  first, but then the statusline could not tell "no plan" from "refresh in
  progress; no cache yet" and dropped the group, so `--provider codex` alone
  on an API-key account collapsed into the generic `⚠ ccx`. The credential
  source now returns `provider.NoPlanError`, the engine short-circuits to
  `errorState(AuthNoPlan, reason)` before any cache or spawn (a fresh cache
  from an earlier plan login is still served until its TTL elapses — the
  same ≤TTL lag every credential change has, and checking credentials
  before a fresh serve would cost a file read plus a keychain shell-out on
  every tick), the statusline renders a gray `<name> no plan` marker in
  every configuration, and `now` prints the reason with exit 1.
  `auth: "no_plan"` is an additive enum value, the same kind of change as
  `source: "stdin"`.
- overlay tolerates reset jitter and never borrows a dead reset: the
  elapsed-cycle rule (stdin reset in the past, cache reset in the future →
  cache window wholesale) fired even when the two resets were the same
  boundary seen through sub-second server jitter (observed: 133 ms between
  two fetches), so for the seconds between them the line showed the older
  cache utilization instead of the live projection — exactly the override
  the jitter rationale forbids. The wholesale rule now requires the cache
  reset to be later by more than a one-minute tolerance (real cycles are
  hours apart); inside it only the reset is borrowed. A cache reset that has
  itself elapsed is never borrowed anymore either: it produced a ≈ marker
  and a past `resets_at` on the wire for nothing.
- the detached sync fallback reports its own 401: `boundedRefresh` swallowed
  the fetch error, so when the spawn failed and the fallback got a 401 the
  detached ladder served stale data with `auth: "ok"` (or "refresh in
  progress") while `now` on the same machine said expired. The error now
  flows back through `refresh` and the ladder's ErrAuth branch applies to
  both modes.
- the refresh claim is pre-gated by the entry already in hand: on every tick
  where stdin is incomplete (the common case: CC's projection omits Fable)
  `detachedRefresh` took the exclusive flock and re-read the file only to be
  denied. `refreshDue` now checks the loaded `fetched_at`/`attempted_at`
  against the TTL first; the locked check-and-set stays the authority for
  the race, so the ≤1/TTL bound is unchanged and denied ticks are IO free.
- `Store` writes blind again: routing it through `Update` made every fetch
  read and decode the entry it was about to discard, under the write flock.
  `Update` stays the primitive for the two real read-modify-writes
  (ClaimRefresh, the limit-hit merge); a wholesale replace does not need it,
  and a blind write is also what repairs a corrupt file.
- the cache directory must be ours: `os.TempDir()` on Linux is the shared
  `/tmp`, and `MkdirAll` accepts a pre-existing directory or symlink owned by
  someone else, so another local user could pre-create
  `/tmp/cc-token-exposer-<uid>` and read the (token-free) usage numbers or
  plant a snapshot ccx would render. Every lock and load now `Lstat`s the
  directory and refuses a symlink or a foreign owner (`ErrUnsafeDir`); mode
  bits are deliberately not checked so a user who relaxed their own cache
  dir keeps a working cache.
- one liveness predicate for a limit hit: engine, statusline and adapter
  each decided whether a persisted hit still counts; `schema.LimitHit.Active`
  is that rule now (a hit without a reset stays active, an elapsed one is
  not rendered anywhere), and the adapter ignores a degenerate `limit_hit`
  (`null`, `{}`) without a `detected_at`, which used to render `⛔ limit`
  until the next successful fetch.
- no-op persistence takes no lock: `StoreLimitHit(nil)` with nothing
  persisted used to create the cache directory and a `<name>.json.lock` for
  a provider that was never set up (`ccx now --provider codex` without
  Codex); the engine skips the call when neither side has a hit.
- the statusline resolves providers concurrently, like `now`: the claude and
  codex groups share nothing but the process, and two sync fallbacks in
  series (10s) sat uncomfortably close to the 12s tick budget.
- processRefresher has no default provider: an empty name used to spawn
  `ccx refresh --provider claude`, i.e. an unnamed engine claimed its own
  slot and refreshed Claude's cache. It is a spawn error now, which the
  engine treats like any failed spawn (bounded inline refresh).
