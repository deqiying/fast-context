# fast-context

Go CLI version of `fast-context-mcp`, feature-aligned with npm `fast-context-mcp@1.5.2`. The upstream GitHub repository is stale at v1.2.2, so use `npm pack fast-context-mcp@latest` as the behavior-comparison baseline.

The CLI keeps semantic search, explicit credential configuration and inspection, restricted local tools, structured output, and an embedded Agent Skill in one Go binary. npm distribution adds a small JavaScript launcher, three platform packages, and a bundled ripgrep dependency.

## Install

Alpha releases intentionally use `latest` as the default installation channel while the CLI is being validated. The scoped `0.1.0-alpha.0` bootstrap has passed an isolated registry install, `fast-context --version`, and `doctor` on Windows. Install the current Alpha with:

```powershell
npm install -g @deqiying/fast-context
fast-context --version
```

`@deqiying/fast-context@latest` is equivalent. Pin an exact version when reproducibility is required; `latest` may point to an Alpha until the stable-release gate is met.

Supported npm targets:

- `win32-x64`
- `linux-x64`
- `darwin-arm64`

For source development:

```powershell
go run ./cmd/fast-context --version
go run ./cmd/fast-context doctor --project . --format json
go run ./cmd/fast-context search --project . --format json "where is auth handled"
```

## Commands

| Command | Purpose |
| --- | --- |
| `search <query>` | Run AI-driven code discovery through the Windsurf Devstral protocol. |
| `key extract [--path <path>]` | Inspect the default Linux/WSL Devin CLI TOML or an explicitly selected TOML/`state.vscdb`; databases are never discovered automatically. |
| `doctor` | Check project path, ripgrep, credentials, and build metadata. |
| `skills list` | List embedded Agent Skills. |
| `skills show <skill> [--reference cli-contract]` | Return the raw embedded `SKILL.md`, an allowlisted reference, or structured JSON. |
| `schema [--format json] [--pretty] [<canonical-command-path>...]` | Return the versioned, machine-readable CLI command manifest in full or for one canonical command path. |
| `version`, `--version`, `-v` | Print the same linker-injected build metadata. |

Load the version-matched Agent Skill directly from the CLI:

```powershell
fast-context skills list --format json
fast-context skills show --format content fast-context
fast-context skills show --format content --reference cli-contract fast-context
```

`--reference` accepts the logical ID `cli-contract`, not a filesystem or embed path. Omitting it preserves the original main-Skill content/JSON shape. This makes the Skill's progressive-disclosure reference available from the same versioned binary without exposing arbitrary embedded resources.

## CLI command manifest

Use `schema` when an Agent or adapter needs an exact, machine-readable command contract. Known commands can be invoked directly; query one canonical path when its flags or bindings are needed, and read the full manifest only for first-time generic discovery or caching:

```powershell
# Full manifest: all public executable commands.
fast-context schema --format json

# Targeted manifest: exactly one canonical command definition.
fast-context schema --format json search
fast-context schema --format json key extract
```

Targets accept canonical paths only. Compatibility aliases remain executable, but callers should not generate them from the manifest. `schema` is JSON-only and static: it does not read runtime configuration, credentials, a project, the current directory, or environment values; it does not access the network, write files, or invoke `search`, `doctor`, `key extract`, or Skill-reading handlers. The manifest describes command input and coarse output/exit-code contracts; it does not register a native tool automatically. A host that needs native tool calling must explicitly consume and map the manifest.

Use known argv directly, reuse a targeted definition for the same CLI version within one task, and reserve the full manifest for generic discovery or cache refresh. If an older binary reports `schema` as unknown, load its embedded version-matched Skill with `fast-context skills show --format content fast-context`; do not guess new flags or parse `--help` as a machine contract.

## JSON presentation

When a command selects JSON, its default physical output is one compact JSON line followed by one LF. Pass `--pretty` only for human inspection to use two-space indentation and the same trailing LF; for example:

```powershell
fast-context schema --pretty
```

JSON layout is determined only by argv, never by TTY, pipes, redirection, or the host environment. `--pretty` is supported by `search`, `key extract`, `doctor`, `skills list`, `skills show`, and `schema`; it has no effect in their text/content modes and does not select JSON implicitly. Existing default formats remain unchanged, and text-only `version` does not accept `--pretty`.

## Search pipeline

1. **Bootstrap phase** (default on): a cheap two-turn pre-pass over an L1 tree collects ripgrep patterns and hotspot directories.
2. **Hotspot repo map** (`bootstrap_hotspot`): a shallow global tree plus deeper subtrees for top directories scored by BM25F, probe grep, Git RFM, and file aggregation, fused with RRF. `--repo-map-mode classic` restores the plain adaptive tree.
3. **Multi-turn search** with smart context trimming, UTF-8-safe tool results, and a final 320 KB uncompressed protobuf gate before HTTP.
4. **No-result retry**: up to two narrower project roots are tried automatically when no path can be parsed.
5. **Grep keyword expansion**: collected patterns run locally to supplement missed files without another API call.
6. **Optional snippets**: `--include-snippets` adds line-numbered code under a 45 KB output budget.

`--tree-depth 0` selects project-size auto depth only in `classic` mode: fewer than 500 entries uses 4, up to 5000 uses 3, and larger trees use 2. In the default `bootstrap_hotspot` mode, the global map uses `FC_BOOTSTRAP_TREE_DEPTH`, selected hotspot subtrees use `FC_HOTSPOT_TREE_DEPTH`, and a requested depth may raise the hotspot depth. JSON `meta.tree_depth` is the global-map depth; `meta.hotspot_depth` is the hotspot depth.

## Configuration

Flags override runtime defaults. Integer flag values and their environment-backed defaults are clamped to their documented safe ranges. `FC_TIMEOUT_MS` is an environment default clamped to `1s..300s`; an explicit `--timeout` retains the historical compatibility behavior of accepting any value the duration parser accepts, rather than being clamped to that range. The command manifest exposes `1s..300s` as the canonical Agent input range without narrowing explicit legacy argv compatibility.

The CLI also reads an optional user-level JSON file at `$HOME/.config/fast-context/config.json`:

```json
{
  "api_key": "your-api-key"
}
```

The file is never created or modified by the CLI. It accepts only the documented fields; invalid JSON, unknown fields, unreadable files, and trailing JSON documents are reported as errors. An absent file is treated as unset. Credentials are resolved in this order:

1. `FAST_CONTEXT_KEY`
2. `api_key` in `$HOME/.config/fast-context/config.json`
3. `WINDSURF_API_KEY`
4. Linux/WSL Devin CLI `~/.local/share/devin/credentials.toml`

Blank values do not claim a priority slot. `FAST_CONTEXT_KEY` and the local config fail fast when they look like a truncated `devin-session-token`. A truncated `WINDSURF_API_KEY` may recover only from the Linux/WSL Devin CLI TOML; otherwise it fails locally before any network request. Windsurf/Devin `state.vscdb` files are never discovered automatically.

| Env | Default | Meaning |
| --- | --- | --- |
| `FC_MAX_TURNS` | 3 | Search rounds (1–5) |
| `FC_MAX_COMMANDS` | 8 | Restricted commands per round (1–20) |
| `FC_TIMEOUT_MS` | 30000 | Stream timeout in milliseconds |
| `FC_REPO_MAP_MODE` | `bootstrap_hotspot` | `classic` disables the optimizer |
| `FC_BOOTSTRAP_ENABLED` | `true` | Bootstrap pre-pass |
| `FC_BOOTSTRAP_TREE_DEPTH` | 1 | Bootstrap mini-map depth |
| `FC_BOOTSTRAP_MAX_TURNS` | 2 | Bootstrap rounds |
| `FC_BOOTSTRAP_MAX_COMMANDS` | 6 | Bootstrap commands per round |
| `FC_HOTSPOT_TOP_K` | 4 | Hotspot subtree count |
| `FC_HOTSPOT_TREE_DEPTH` | 2 | Hotspot subtree depth |
| `FC_HOTSPOT_MAX_BYTES` | 122880 | Repository-map budget |
| `FC_INCLUDE_SNIPPETS` | `false` | Default snippet behavior |
| `FC_RESULT_MAX_LINES` | 50 | Restricted-tool result line cap |
| `FC_LINE_MAX_CHARS` | 250 | Per-line UTF-8 byte cap; truncation never splits a rune |
| `FC_RG_PATH` | — | Explicit ripgrep binary path |
| `FAST_CONTEXT_DEBUG` | — | `1` or `true` prints progress to stderr |
| `FAST_CONTEXT_KEY` | — | Explicit fast-context key; highest credential priority |
| `WINDSURF_API_KEY` | — | Manual key; truncated values may recover from the Linux/WSL Devin CLI TOML only |
| `FC_INSECURE_TLS` | — | `1` disables TLS verification for local troubleshooting only |

The npm launcher sets `FC_RG_PATH` from `@vscode/ripgrep` only when the user has not already set it.

## Security and data boundary

- `search` sends the query, repository map, and requested restricted-tool results to Windsurf. Do not use it when external transmission is not authorized.
- `--include-snippets` is off by default.
- The local executor accepts only structured `rg`, `readfile`, `tree`, `ls`, and `glob` commands. `--max-commands` is enforced locally; model output cannot raise the limit.
- Effective `--exclude` patterns are anchored to the original project root and apply to repo maps, bootstrap/hotspot scoring, retry probes, all restricted-tool reads, grep expansion, final files, and snippets.
- Remote paths are mapped through `/codebase` and checked against project-root and symlink escape.
- Valid structured remote error codes such as `invalid_argument` are preserved instead of being reduced to `UNKNOWN`; raw response bodies are never emitted.
- `RIPGREP_CONFIG_PATH` is cleared for deterministic searches.
- API keys are redacted; the npm launcher does not inspect credentials or `.env` files.
- Runtime commands never discover Windsurf/Devin `state.vscdb`. `key extract --path <state.vscdb>` is an explicit diagnostic action that copies the selected database to a temporary snapshot and normally removes it before returning; abnormal process termination can leave that snapshot behind.
- Keep `$HOME/.config/fast-context/config.json` outside repositories and use restrictive user-only permissions (`0700` directory / `0600` file on Unix).
- TLS verification is enabled by default. `FC_INSECURE_TLS=1` is an explicit troubleshooting override.

## Development and verification

Use a writable cache when the default Go cache is restricted:

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'fast-context-go-build'
go test ./...
go vet ./...
node npm/fast-context/test/launcher.test.js
```

Validate the embedded Skill with Codex's `skill-creator` validator, then build all npm targets, create isolated staging packages, audit their file lists, install the current-platform tarballs, and verify bundled ripgrep:

```powershell
python C:\path\to\skill-creator\scripts\quick_validate.py internal\skills\assets\fast-context
pwsh ./scripts/package-npm.ps1
```

`scripts/package-npm.ps1` writes ignored artifacts under `dist/`; it never runs `npm publish`.

`.deploy/version` is the single release version source. After all tests and external ownership checks pass, prepare a new version directly with:

```powershell
pwsh ./.deploy/release-version.ps1 0.1.0-alpha.2
```

The positional argument updates `.deploy/version`, synchronizes all package versions, stages only version files, and creates a local release commit and tag. Omit the argument to use a version already written to `.deploy/version`. The script never pushes or publishes.

## npm publishing boundary

The registry rejected the unscoped `fast-context` name because it is too similar to the existing `fastcontext` package, so the entry package is `@deqiying/fast-context`. The four scoped `0.1.0-alpha.0` packages have completed the manual bootstrap and are installable through the default `latest` channel.

The published bootstrap binary reports a dirty-worktree commit, and npm versions are immutable. Treat `alpha.0` only as package-name bootstrap evidence: do not create a retroactive Git tag that would falsely imply source alignment. The same `.github/workflows/release.yml` Trusted Publisher is configured for all four packages. Publishing a clean `0.1.0-alpha.1` through OIDC replaces the default Alpha and establishes package, Git tag, GitHub Release, and binary-version alignment.

The GitHub workflow uses GitHub-hosted runners, Node 24, npm 11, job-scoped `id-token: write`, immutable package versions, pack audits, and SHA256 checksums. It skips an already published identical version rather than attempting to overwrite it.

## License and origin

MIT licensed. This implementation preserves the upstream `fast-context-mcp` MIT notice and adds the current project copyright in [LICENSE](LICENSE).
