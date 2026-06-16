# cc-token-exposer (`ccx`)

Zero-config tracker for Claude Pro/Max plan credit-limit windows.

Reads the same authoritative source as Claude Code's built-in `/usage` — no setup required. If `claude` works on your machine, `ccx` works.

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
5h    47% · resets in 3h35m
7d    23% · resets in 3d16h
source: live
```

JSON output for scripts:

```
$ ccx now --json
{"schema_version":1,"type":"snapshot","source":"oauth","auth":"ok",
 "snapshot":{"fetched_at":"…","five_hour":{"utilization":47,"resets_at":"…"},"seven_day":{"utilization":23,"resets_at":"…"}}}
```

### `ccx statusline`

Prints a single line for the Claude Code statusline:

```
◷ 5h ▮▮▯▯▯ 47% ↻ 3h50m · ◷ 7d ▮▯▯▯▯ 23% ↻ 3d16h · ✦ opus ▯▯▯▯▯ 5% ↻ 3d16h
```

Register in `~/.claude/settings.json`:

```json
{ "statusLine": { "type": "command", "command": "ccx statusline" } }
```

Gauges turn muted yellow at ≥60% and red above 85%. A leading `≈` marks stale cached data; `⚠ login` means credentials need attention. Set `NO_COLOR` to disable ANSI.

---

## How it works

> **⚠ Uses an unofficial API.** `ccx` depends on `GET https://api.anthropic.com/api/oauth/usage` — the same endpoint Claude Code's `/usage` uses internally, but undocumented and unsupported. It may change or disappear without notice.

`ccx` reuses Claude Code's existing OAuth token (read from `~/.claude/.credentials.json` or the macOS Keychain). The token is never persisted or printed — memory only.

Responses are cached to disk for 120 seconds, so the statusline never hammers the API. On any failure the tool degrades gracefully: stale cache → transcript fallback → error state. It never shows a blank screen.

Cache: `<os.UserCacheDir()>/cc-token-exposer/snapshot.json`

---

## License

MIT — see [LICENSE](LICENSE).
