# cc-token-exposer (`ccx`)

Zero-config tracker for Claude Pro/Max plan credit-limit windows — and, with `--provider codex`, for Codex CLI's ChatGPT plan.

Reads the same authoritative source as Claude Code's built-in `/usage` (and Codex CLI's `/status`) — no setup required. If `claude` works on your machine, `ccx` works; if `codex` works, `ccx --provider codex` works.

---

## Install

### Homebrew

```sh
brew install seosd97/tap/ccx
```

### Go

```sh
go install github.com/seosd97/cc-token-exposer/cmd/ccx@latest
```

### From source

```sh
git clone https://github.com/seosd97/cc-token-exposer
cd cc-token-exposer
go build -o ccx ./cmd/ccx
```

Prebuilt binaries for macOS and Linux are also attached to each [release](https://github.com/seosd97/cc-token-exposer/releases).

---

## Usage

### `ccx now`

```
$ ccx now
5h     47% · resets in 3h35m
7d     23% · resets in 3d16h
Fable  94% · resets in 2d15h
source: live
```

Per-model weekly limits (Opus, Fable) appear as their own rows when the plan
reports them; they are omitted when unused.

JSON output for scripts:

```
$ ccx now --json
{"schema_version":1,"provider":"claude","type":"snapshot","source":"oauth","stale":false,"auth":"ok",
 "snapshot":{"fetched_at":"…","five_hour":{"utilization":47,"resets_at":"…"},"seven_day":{"utilization":23,"resets_at":"…"},
  "scoped_limits":{"Fable":{"utilization":94,"resets_at":"…"},"Opus":{"utilization":12,"resets_at":"…"}}}}
```

Codex CLI (ChatGPT plan) is a second provider with the same output shape:

```
$ ccx now --provider codex
5h     31% · resets in 2h10m
7d     18% · resets in 5d2h
GPT-5.3-Codex-Spark   0% · resets in 7d0h
source: live
```

Codex's per-model pools (here the Spark model) appear as their own rows, just
like Claude's per-model limits. Several providers print one block each, or one
JSON line each with `--json`:

```
$ ccx now --provider claude,codex
claude
5h     47% · resets in 3h35m
7d     23% · resets in 3d16h
source: cache

codex
7d     18% · resets in 5d2h
source: cache
```

When the endpoint's response shape drifts (a window losing its `resets_at`, a
renamed field, a new limits kind, …), `now` prints a `drift:` line and the
JSON state carries a `drift` array — a silent format change becomes visible
instead of quietly degrading into wrong numbers.

### `ccx statusline`

Prints a single line for the Claude Code statusline:

```
◷ 5h ▮▮▯▯▯ 47% ↻ 3h50m · ◷ 7d ▮▯▯▯▯ 23% ↻ 3d16h · ✧ fable ▮▮▮▮▮ 94% ↻ 2d15h
```

Register in `~/.claude/settings.json`:

```json
{ "statusLine": { "type": "command", "command": "ccx statusline" } }
```

To see the Codex plan next to it (handy when Codex runs from inside Claude Code), add `--provider claude,codex`:

```
◷ 5h ▮▮▯▯▯ 47% ↻ 3h50m · ◷ 7d ▮▯▯▯▯ 23% ↻ 3d16h │ codex ◷ 7d ▮▯▯▯▯ 18% ↻ 5d2h
```

Gauges turn muted yellow at ≥60% and red above 85%. A leading `≈` marks stale cached data; `⚠ login` means credentials need attention (per group). Set `NO_COLOR` to disable ANSI. The Codex group never waits on the network: it is served from its own cache and healed by a detached background refresh.

### `ccx update`

Self-update to the latest GitHub release:

```
$ ccx update
current: v0.1.0
latest:  v0.2.0
downloading ccx_darwin_arm64.tar.gz ...
verifying checksum ... ok
replacing /Users/you/go/bin/ccx ...
updated v0.1.0 -> v0.2.0
```

It checks the latest release, downloads the binary for your OS/arch, verifies the release's ed25519 signature on `checksums.txt` (anchored to a public key embedded in the binary), verifies the archive's SHA-256, and atomically replaces the running executable. Use `--check` to only report whether a newer version exists. Homebrew installs are left to `brew upgrade ccx`.

---

## How it works

> **⚠ Uses unofficial APIs.** `ccx` depends on `GET https://api.anthropic.com/api/oauth/usage` — the same endpoint Claude Code's `/usage` uses internally — and, for `--provider codex`, on `GET https://chatgpt.com/backend-api/wham/usage`, the call behind Codex CLI's `/status`. Both are undocumented and unsupported and may change or disappear without notice; `now` prints a `drift:` line when a response no longer looks the way ccx expects.

`ccx` reuses the CLI's existing OAuth token — Claude Code's from `~/.claude/.credentials.json` or the macOS Keychain, Codex CLI's from `~/.codex/auth.json` (ChatGPT login; an API-key login has no plan limits and says so). Tokens are never persisted or printed — memory only — and ccx never runs a token refresh of its own, so it cannot break your login.

Responses are cached to disk for 120 seconds, so the statusline never hammers the API. The statusline path never blocks on the network either: when the cache needs refreshing it spawns a detached background refresh and renders immediately, so gaps (like a per-model window Claude Code doesn't pipe) heal on the next tick. On any failure the tool degrades gracefully: stale cache → transcript fallback → error state. It never shows a blank screen.

Cache: `<os.UserCacheDir()>/cc-token-exposer/snapshot.json` (Claude) and `codex.json` (Codex), one file and lock per provider.

---

## License

MIT — see [LICENSE](LICENSE).
