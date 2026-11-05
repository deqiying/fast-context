# fast-context

Go CLI version of `fast-context-mcp`, feature-aligned with npm `fast-context-mcp@1.5.2`, which is still the `latest` release of that package name. Treat `npm pack fast-context-mcp@latest` as the behavior-comparison baseline; GitHub sources are not guaranteed to match published packages. Selected later improvements are adopted from the npm forks `@philau2512/fast-context-mcp@1.5.7` (home/root project guard, WSL `devin login` hint) and `@sammysnake/fast-context-mcp@1.3.2` (tool-argument JSON repair, partial-result salvage). All three packages use the same Windsurf wire protocol as this CLI.

The CLI keeps semantic search, explicit credential configuration and inspection, restricted local tools, structured output, and an embedded Agent Skill in one Go binary. The same binary can also run as a stdio MCP server (`fast-context mcp serve`) that exposes `fast_context_search` and `fast_context_doctor`; see [MCP server](#mcp-server). npm distribution adds a small JavaScript launcher, three platform packages, and a bundled ripgrep dependency.

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
| `mcp serve` | Run a stdio MCP server that exposes `fast_context_search` and `fast_context_doctor` to an MCP host. Long-running; configure it in a host instead of running it from an Agent shell. |
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

## MCP server

`fast-context mcp serve` runs the same search and doctor logic as a [Model Context Protocol](https://modelcontextprotocol.io) server over stdio. Configure it in an MCP host; do not run it from an Agent shell. It reads newline-delimited JSON-RPC messages from stdin until EOF, writes only protocol messages to stdout, and writes logs to stderr. It exits `0` on stdin EOF or SIGINT/SIGTERM, `1` on a transport or server failure, and `2` for invalid argv. Only stdio is supported; there is no HTTP or SSE transport.

JSON-style hosts such as Claude Desktop and Cursor:

```json
{
  "mcpServers": {
    "fast-context": {
      "command": "fast-context",
      "args": ["mcp", "serve"],
      "env": {
        "FAST_CONTEXT_KEY": "<key>"
      }
    }
  }
}
```

Through the npm package, use `"command": "npx"` with `"args": ["-y", "@deqiying/fast-context", "mcp", "serve"]`. The launcher passes stdin and stdout through unchanged and sets `FC_RG_PATH` exactly as it does for CLI commands. Hosts on Windows that cannot resolve `npx` directly may need `cmd /c npx ...`; this has not been verified. Codex `config.toml` (check the host documentation for exact fields):

```toml
[mcp_servers.fast-context]
command = "fast-context"
args = ["mcp", "serve"]
```

The server reads `FC_*`, `FAST_CONTEXT_DEBUG`, and credentials from the host-provided environment once at startup, using the same credential order as the CLI; restart the server after changing them. `FAST_CONTEXT_DEBUG=1` writes per-call progress and SDK session logs to stderr.

| Tool | Behavior | Network |
| --- | --- | --- |
| `fast_context_search` | Same pipeline, exclusion policy, retries, and data boundary as `search`. | Sends the query, repository map, and restricted local results to Windsurf. |
| `fast_context_doctor` | Returns compact JSON byte-identical to `fast-context doctor --format json --project <project_path>`. | None. |

`fast_context_search` keeps the parameter names of upstream `fast-context-mcp@1.5.2`:

| Parameter | Type | Default | Range |
| --- | --- | --- | --- |
| `query` | string, required | — | Must contain a non-space character |
| `project_path` | string, required | — | Absolute path to an existing directory |
| `tree_depth` | integer | `3` | `0..6` |
| `max_turns` | integer | `FC_MAX_TURNS` (`3`) | `1..5` |
| `max_results` | integer | `10` | `1..30` |
| `exclude_paths` | string[] | `[]` | Same policy as `--exclude` |
| `include_code_snippets` | boolean | `FC_INCLUDE_SNIPPETS` (`false`) | — |

`max_commands`, the stream timeout, and repo-map settings are server-level and come only from `FC_*` variables. Unlike CLI flags, out-of-range values and unknown keys are rejected by schema validation instead of being clamped. Successful results use the CLI text layout, with hints that name tool parameters and `fast_context_doctor` instead of CLI flags. Validation, credential, authentication, timeout, and payload failures set `isError: true`; "No relevant files found." is a normal result.

`fast_context_doctor` accepts only a required absolute `project_path`. A failed check (`ok=false`) is a normal result; only an empty or relative path sets `isError: true`. It never contacts Windsurf, so `ok=true` does not prove that the key is valid or the remote service is reachable.

Intentional differences from upstream `fast-context-mcp@1.5.2`:

- The server name is `fast-context`, and failed tool calls set `isError: true`.
- Input schemas declare `additionalProperties: false`.
- `extract_windsurf_key` is not provided; calling it returns an unknown-tool error. `fast_context_doctor` is new.
- Credentials follow this project's resolution order and never auto-discover Windsurf `state.vscdb`.
- On Windows, a path such as `/repo` without a drive letter is not treated as absolute.

| Upstream configuration | Migrate to |
| --- | --- |
| `npx -y fast-context-mcp` | `npx -y @deqiying/fast-context mcp serve`, or the installed `fast-context mcp serve` |
| `env.WINDSURF_API_KEY` | Still honored at priority 3; prefer `FAST_CONTEXT_KEY` |
| Automatic key reading from the Windsurf database | Set a key explicitly, or run `fast-context key extract --path <state.vscdb>` once in a terminal and configure the result |
| Prompts or workflows that call `extract_windsurf_key` | Call `fast_context_doctor` to inspect the active credential source |

On stdin EOF the server stops reading and cancels requests that are still in flight, so their responses are not written. MCP hosts keep stdin open for the whole session; scripted tests must read each response before closing stdin.

## JSON presentation

When a command selects JSON, its default physical output is one compact JSON line followed by one LF. Pass `--pretty` only for human inspection to use two-space indentation and the same trailing LF; for example:

```powershell
fast-context schema --pretty
```

JSON layout is determined only by argv, never by TTY, pipes, redirection, or the host environment. `--pretty` is supported by `search`, `key extract`, `doctor`, `skills list`, `skills show`, and `schema`; it has no effect in their text/content modes and does not select JSON implicitly. Existing default formats remain unchanged; text-only `version` and protocol-only `mcp serve` do not accept `--pretty`.

## Search pipeline

1. **Bootstrap phase** (default on): a cheap two-turn pre-pass over an L1 tree collects ripgrep patterns and hotspot directories.
2. **Hotspot repo map** (`bootstrap_hotspot`): a shallow global tree plus deeper subtrees for top directories scored by BM25F, probe grep, Git RFM, and file aggregation, fused with RRF. `--repo-map-mode classic` restores the plain adaptive tree.
3. **Multi-turn search** with smart context trimming, UTF-8-safe tool results, and a final 320 KB uncompressed protobuf gate before HTTP. Common tool-argument JSON defects (unquoted or half-quoted keys, trailing commas) are repaired locally once, outside string values, before strict command validation; only unrepairable arguments cost a corrective round.
4. **No-result retry**: when the model replies with plain text and no usable path, the reply is kept as `raw_response` and up to two narrower project roots are tried automatically.
5. **Partial-result salvage**: when the model never calls the `answer` tool, existing in-project files from a text `<ANSWER>` block, `/codebase/...` paths in its reply, or files it already read when turns run out are returned as candidates with `meta.salvaged_response=true`. Every salvaged path must map to an existing, non-excluded regular file inside the project. Turn exhaustion without any salvageable file or rg pattern remains an error.
6. **Grep keyword expansion**: collected patterns run locally to supplement missed files without another API call.
7. **Optional snippets**: `--include-snippets` adds line-numbered code under a 45 KB output budget.

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
- `mcp serve` applies the same boundary through `fast_context_search`, whose description discloses the Windsurf transmission. `fast_context_doctor` is local-only and returns only redacted key material. No MCP tool returns a credential or reads `state.vscdb`, and MCP stdout carries only JSON-RPC messages.
- `search` rejects a project root that is exactly the user home directory or a filesystem root (including the current directory when `--project` is omitted) before reading credentials or making any network request. Repositories below the home directory are unaffected.
- `--include-snippets` is off by default.
- The local executor accepts only structured `rg`, `readfile`, `tree`, `ls`, and `glob` commands. `--max-commands` is enforced locally; model output cannot raise the limit.
- Effective `--exclude` patterns are anchored to the original project root and apply to repo maps, bootstrap/hotspot scoring, retry probes, all restricted-tool reads, grep expansion, final files, and snippets.
- Remote paths are mapped through `/codebase` and checked against project-root and symlink escape.
- Valid structured remote error codes such as `invalid_argument` are preserved instead of being reduced to `UNKNOWN`; raw response bodies are never emitted.
- `RIPGREP_CONFIG_PATH` is cleared for deterministic searches.
- API keys are redacted; the npm launcher does not inspect credentials or `.env` files.
- Runtime commands never discover Windsurf/Devin `state.vscdb`. `key extract --path <state.vscdb>` is an explicit diagnostic action that copies the selected database to a temporary snapshot and normally removes it before returning; abnormal process termination can leave that snapshot behind.
- Keep `$HOME/.config/fast-context/config.json` outside repositories and use restrictive user-only permissions (`0700` directory / `0600` file on Unix).
- TLS verification is enabled by default. `FC_INSECURE_TLS=1` is an explicit troubleshooting override; do not keep it in a long-lived MCP host configuration.

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
