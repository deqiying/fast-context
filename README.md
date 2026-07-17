# fast-context

Go CLI version of `fast-context-mcp`, feature-aligned with npm `fast-context-mcp@1.5.2`. The upstream GitHub repository is stale at v1.2.2, so use `npm pack fast-context-mcp@latest` as the behavior-comparison baseline.

The CLI keeps semantic search, credential discovery, restricted local tools, structured output, and an embedded Agent Skill in one Go binary. npm distribution adds a small JavaScript launcher, three platform packages, and a bundled ripgrep dependency.

## Install

The scoped `0.1.0-alpha.0` bootstrap is published for the entry package and all three platform packages. An isolated registry install of the `next` channel, `fast-context --version`, and `doctor` has passed on Windows. Install the prerelease channel with:

```powershell
npm install -g @deqiying/fast-context@next
fast-context --version
```

Do not use `@latest` as the stable channel yet. The registry currently also has `latest` pointing to the bootstrap prerelease; that tag must be removed before stable-release guidance is enabled.

Supported npm targets:

- `win32-x64`
- `linux-x64`
- `darwin-arm64`

For source development:

```powershell
go run ./cmd/fast-context --version
go run ./cmd/fast-context doctor --project . --format json
go run ./cmd/fast-context search "where is auth handled" --project . --tree-depth 0 --format json
```

## Commands

| Command | Purpose |
| --- | --- |
| `search <query>` | Run AI-driven code discovery through the Windsurf Devstral protocol. |
| `key extract` | Read `WINDSURF_API_KEY`, Devin CLI TOML, or Windsurf/Devin `state.vscdb`. |
| `doctor` | Check project path, ripgrep, credentials, and build metadata. |
| `skills list` | List embedded Agent Skills. |
| `skills show <skill>` | Return the raw embedded `SKILL.md` or structured JSON. |
| `version`, `--version`, `-v` | Print the same linker-injected build metadata. |

Load the version-matched Agent Skill directly from the CLI:

```powershell
fast-context skills list --format json
fast-context skills show fast-context --format content
```

## Search pipeline

1. **Bootstrap phase** (default on): a cheap two-turn pre-pass over an L1 tree collects ripgrep patterns and hotspot directories.
2. **Hotspot repo map** (`bootstrap_hotspot`): a shallow global tree plus deeper subtrees for top directories scored by BM25F, probe grep, Git RFM, and file aggregation, fused with RRF. `--repo-map-mode classic` restores the plain adaptive tree.
3. **Multi-turn search** with smart context trimming and a 320 KB preflight trim.
4. **No-result retry**: up to two narrower project roots are tried automatically when no path can be parsed.
5. **Grep keyword expansion**: collected patterns run locally to supplement missed files without another API call.
6. **Optional snippets**: `--include-snippets` adds line-numbered code under a 45 KB output budget.

`--tree-depth 0` selects depth from project size: fewer than 500 entries uses 4, up to 5000 uses 3, and larger trees use 2.

## Configuration

Flags override environment variables; both are clamped to safe ranges.

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
| `FC_LINE_MAX_CHARS` | 250 | Per-line character cap |
| `FC_RG_PATH` | — | Explicit ripgrep binary path |
| `FAST_CONTEXT_DEBUG` | — | `1` or `true` prints progress to stderr |
| `WINDSURF_API_KEY` | — | Manual key; truncated values fall back to local extraction |
| `FC_INSECURE_TLS` | — | `1` disables TLS verification for local troubleshooting only |

The npm launcher sets `FC_RG_PATH` from `@vscode/ripgrep` only when the user has not already set it.

## Security and data boundary

- `search` sends the query, repository map, and requested restricted-tool results to Windsurf. Do not use it when external transmission is not authorized.
- `--include-snippets` is off by default.
- The local executor accepts only structured `rg`, `readfile`, `tree`, `ls`, and `glob` commands.
- Remote paths are mapped through `/codebase` and checked against project-root and symlink escape.
- `RIPGREP_CONFIG_PATH` is cleared for deterministic searches.
- API keys are redacted; the npm launcher does not inspect credentials or `.env` files.
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

`.deploy/version` is the single release version input. After all tests and external ownership checks pass, `.deploy/release-version.ps1` updates package versions, stages only version files, creates a local release commit and tag, and does not push or publish.

## npm publishing boundary

The registry rejected the unscoped `fast-context` name because it is too similar to the existing `fastcontext` package, so the entry package is `@deqiying/fast-context`. The four scoped `0.1.0-alpha.0` packages have completed the manual bootstrap and are installable through `next`.

The published bootstrap binary reports a dirty-worktree commit, and npm versions are immutable. Treat `alpha.0` only as package-name bootstrap evidence: do not create a retroactive Git tag that would falsely imply source alignment. Configure the same `.github/workflows/release.yml` Trusted Publisher for all four packages, remove the accidental prerelease `latest` tags with explicit owner approval, and publish a clean `0.1.0-alpha.1` through OIDC to establish package, Git tag, GitHub Release, and binary-version alignment.

The GitHub workflow uses GitHub-hosted runners, Node 24, npm 11, job-scoped `id-token: write`, immutable package versions, pack audits, and SHA256 checksums. It skips an already published identical version rather than attempting to overwrite it.

## License and origin

MIT licensed. This implementation preserves the upstream `fast-context-mcp` MIT notice and adds the current project copyright in [LICENSE](LICENSE).
