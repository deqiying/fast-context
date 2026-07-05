# fast-context

Go CLI version of `fast-context-mcp` (feature-aligned with npm **1.5.2**; the
GitHub repo of the upstream is stale at v1.2.2 — use `npm pack fast-context-mcp@latest`
as the comparison baseline).

This repository keeps the MCP tool behavior as a standalone command-line tool:

```powershell
go run ./cmd/fast-context search "where is auth handled" --project . --tree-depth 0 --include-snippets
go run ./cmd/fast-context search "database connection pool" --project E:\Project\GoLand\sub2api --format json
go run ./cmd/fast-context key extract
go run ./cmd/fast-context doctor
```

## Commands

| Command | Purpose |
| --- | --- |
| `search <query>` | Run AI-driven code search through the Windsurf Devstral protocol. |
| `key extract` | Read `WINDSURF_API_KEY`, Devin CLI TOML, or Windsurf/Devin `state.vscdb`. |
| `doctor` | Check project path, ripgrep availability, credential candidates, and version info. |
| `version` | Print build metadata. |

## Search pipeline (ported from upstream 1.5.2)

1. **Bootstrap phase** (default on): a cheap 2-turn pre-pass over an L1 tree
   collects rg patterns and hotspot directories.
2. **Hotspot repo map** (`bootstrap_hotspot` mode): shallow global tree plus
   deeper subtrees for top-K directories scored by BM25F + probe grep +
   git RFM + file aggregation, fused with RRF; includes a path-spine section.
   `--repo-map-mode classic` restores the plain adaptive tree.
3. **Multi-turn search** with smart context trimming (progress summary instead
   of naive head/tail) and a 320KB preflight trim.
4. **No-result auto retry**: when the model returns nothing parseable, up to 2
   narrower project roots are retried automatically (`[retry]` notes in output).
5. **Grep keyword expansion**: collected rg patterns run locally to supplement
   missed files (zero API calls, `[grep expanded]` label).
6. **Snippets** (`--include-snippets`): line-numbered fenced code blocks under
   a 45KB budget, in both text and JSON output.

`--tree-depth 0` picks the depth automatically from project size
(<500 entries → 4, ≤5000 → 3, >5000 → 2).

## Configuration

Flags win over environment variables; both are clamped to safe ranges.

| Env | Default | Meaning |
| --- | --- | --- |
| `FC_MAX_TURNS` | 3 | Search rounds (1-5) |
| `FC_MAX_COMMANDS` | 8 | Commands per round (1-20) |
| `FC_TIMEOUT_MS` | 30000 | Stream timeout (1000-300000) |
| `FC_REPO_MAP_MODE` | bootstrap_hotspot | `classic` disables the optimizer |
| `FC_BOOTSTRAP_ENABLED` | true | Bootstrap pre-pass on/off |
| `FC_BOOTSTRAP_TREE_DEPTH` | 1 | Bootstrap mini-map depth (1-3) |
| `FC_BOOTSTRAP_MAX_TURNS` | 2 | Bootstrap rounds (1-3) |
| `FC_BOOTSTRAP_MAX_COMMANDS` | 6 | Bootstrap commands per round (1-8) |
| `FC_HOTSPOT_TOP_K` | 4 | Hotspot subtree count (0-8) |
| `FC_HOTSPOT_TREE_DEPTH` | 2 | Hotspot subtree depth (1-4) |
| `FC_HOTSPOT_MAX_BYTES` | 122880 | Repo map budget (16KB-256KB) |
| `FC_INCLUDE_SNIPPETS` | false | Default for `--include-snippets` |
| `FC_RESULT_MAX_LINES` | 50 | Tool result line cap (1-500) |
| `FC_LINE_MAX_CHARS` | 250 | Tool result per-line cap (20-10000) |
| `FC_RG_PATH` | — | Explicit ripgrep binary path |
| `FAST_CONTEXT_DEBUG` | — | `1`/`true` prints progress to stderr |
| `WINDSURF_API_KEY` | — | Manual key; auto-falls back to local extraction if it looks `$`-truncated |
| `FC_INSECURE_TLS` | — | `1` disables TLS verification (local troubleshooting only) |

## Notes

- The local executor only supports structured `rg`, `readfile`, `tree`, `ls`, and `glob` commands.
- Remote tool paths are mapped through `/codebase` and checked against project-root escape.
- `rg` arguments use a `--` separator, so patterns starting with `-` are safe;
  bare exclude globs also match nested paths (`dist` → `dist` + `**/dist`).
- Default excludes (node_modules, .git, dist, …) are always merged into the
  repo map / bootstrap / grep-expansion exclude list.
- `RIPGREP_CONFIG_PATH` is cleared when running `rg` to keep results deterministic.
- TLS verification is enabled by default. Set `FC_INSECURE_TLS=1` only for explicit local troubleshooting.
- API keys are redacted in command output.
- Deliberate deviation from upstream: the bootstrap phase is skipped in
  `classic` mode (upstream runs it and discards the hints).

## Development

Use a writable cache directory when the default Go cache is restricted:

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'fast-context-go-build'
$env:GOMODCACHE = Join-Path $env:TEMP 'fast-context-go-mod'
$env:GOPATH = Join-Path $env:TEMP 'fast-context-go-path'
go test ./...
go build ./cmd/fast-context
```
