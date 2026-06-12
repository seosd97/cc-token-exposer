# AGENT.md — cc-token-exposer (`ccx`)

Canonical context for AI agents and contributors. The `docs/` directory is
local-only working material and is NOT committed; this file is the single
in-repo source of truth for design decisions and constraints.

## What this is

`ccx` is a zero-config CLI that tracks Claude Pro/Max plan credit-limit
windows (5-hour session, 7-day, 7-day Opus) using the **same authoritative
source as Claude Code's built-in `/usage`**: the OAuth usage endpoint. It
reuses Claude Code's existing OAuth token read-only, so if `claude` works on
the machine, `ccx` works with no setup.

Positioning: local-JSONL tools (ccusage, claude-monitor) only *estimate*
limits and drift from the real lockout; linuxlewis/claude-usage uses
authoritative values but requires manually extracted browser cookies. `ccx`
gives authoritative numbers with zero setup.

**v1 scope (current, deliberately minimal): exactly two commands.**

- `ccx now [--json]` — one-shot lookup (human or single-line JSON)
- `ccx statusline` — one-line output for the Claude Code statusline
  (registered via `~/.claude/settings.json` → `statusLine.command`)

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
cmd/ccx/statusline.go  statusline formatting + opportunistic stdin rate_limits
internal/
  engine/      THE BRAIN. Resolve(ctx) -> *schema.State, runs the degrade
               ladder. Depends only on 5 consumer-side interfaces it defines:
               CredResolver / Fetcher / Cache / TranscriptProbe / Clock.
  schema/      The wire contract (State, schema_version=1). Leaf package,
               imports nothing internal. The PUBLIC CONTRACT is the JSON
               emitted by `now --json`, not the Go types (internal/ blocks
               external import by design).
  usage/       oauth/usage HTTP client + Reconcile consistency guard.
  creds/       credential acquisition: file > macOS keychain shell-out.
  cache/       flock-protected, atomic-write disk cache of opaque JSON.
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

## Invariants — do not break these

1. **Cache-first.** Claude Code invokes the statusline command every few
   seconds. Within the cache TTL ccx must NEVER touch the API; the disk cache
   (+ flock) caps request volume at ~1 per TTL across ALL invocations.
   Breaking this gets the user rate-limited (the endpoint 429s aggressively).
2. **Token hygiene.** The OAuth token is read-only and in-memory only. It must
   never appear in logs, error messages, the cache file, test fixtures, or
   `String()` output (creds redacts). The cache stores only
   `{fetched_at, payload}` where payload is a token-free usage snapshot.
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
   fractionals); round only at display time.
7. **Transcript files are read-only.** Never write under `~/.claude/`.

## Data source notes

- Endpoint: `GET https://api.anthropic.com/api/oauth/usage` — **unofficial**,
  may change or vanish. Response: `five_hour` / `seven_day` /
  `seven_day_opus` windows, each `{utilization: 0-100 float, resets_at}`,
  plus `extra_usage`.
- Token location: `~/.claude/.credentials.json` (`claudeAiOauth.accessToken`,
  `expiresAt` epoch-ms); some machines store it in the macOS Keychain item
  "Claude Code-credentials" instead (creds falls back to `security` CLI).
- Known server bug (anthropic/claude-code#52497): the weekly counter can drop
  implausibly mid-cycle. `usage.Reconcile` keeps the previous value and sets
  `suspect: true` when utilization falls ≥30 points within an unchanged
  reset cycle.
- statusline stdin: Claude Code pipes session JSON. A `rate_limits` field
  appears intermittently across versions (#40094); when present it is used
  directly (no cache/API I/O), parsed tolerantly (`used_percentage` or
  `utilization`; resets_at as ISO string or epoch). Never depend on it.
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
