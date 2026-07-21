# fast-context CLI contract

## Contents

1. Installation and ownership
2. Commands
3. Search flags and environment
4. Structured output
5. Exit codes and errors
6. Regression checks

## Installation and ownership

The npm package is the direct install and update owner. A runtime manager such as mise may own Node/npm without owning `fast-context` itself.

```text
mise -> Node/npm -> fast-context
```

Use these checks on Windows:

```powershell
Get-Command node,npm,fast-context -All
node --version
npm --version
npm prefix --global
npm list --global --depth=0
fast-context --version
```

Install only after the user authorizes a global change:

```powershell
npm install -g @deqiying/fast-context
# Pin an exact Alpha when reproducibility is required:
npm install -g @deqiying/fast-context@0.1.0-alpha.1
```

Alpha releases intentionally use the default `latest` channel until the stable-release gate is met.

## Commands

```text
fast-context search <query> [flags]
fast-context key extract [flags]
fast-context doctor [flags]
fast-context skills list [--format text|json]
fast-context skills show <skill> [--format content|json]
fast-context version
fast-context --version
fast-context -v
```

`version`, `--version`, and `-v` print the same single line.

`key extract` reads only an explicitly selected or discovered local Devin CLI TOML/Windsurf `state.vscdb`. It bypasses runtime priority resolution; `doctor` reports the effective source used by `search`.

## Search flags and environment

| Flag | Meaning |
| --- | --- |
| `--project`, `-p` | Project root; defaults to the current directory. |
| `--tree-depth 0..6` | Repository map depth; `0` selects automatically. |
| `--max-turns 1..5` | Remote search rounds. |
| `--max-commands 1..20` | Restricted local commands per round. |
| `--max-results 1..30` | Maximum result files. |
| `--timeout <duration>` | Duration such as `30s`, or milliseconds such as `30000`. |
| `--exclude <pattern>` | Repeatable excluded path pattern. |
| `--include-snippets` | Include code snippets in the external request/result path. Off by default. |
| `--repo-map-mode` | `classic` or `bootstrap_hotspot`. |
| `--no-bootstrap` | Skip the bootstrap keyword and hotspot pass. |
| `--format` | `text` or `json`. |
| `--verbose` | Send progress diagnostics to stderr. |

Important environment variables:

| Variable | Meaning |
| --- | --- |
| `FAST_CONTEXT_KEY` | Highest-priority explicit fast-context credential. |
| `WINDSURF_API_KEY` | Explicit Windsurf/Devin credential. |
| `FC_RG_PATH` | Explicit ripgrep binary; takes precedence over `PATH`. |
| `FC_INSECURE_TLS=1` | Disable TLS verification for explicit local troubleshooting only. |
| `FC_MAX_TURNS`, `FC_MAX_COMMANDS`, `FC_TIMEOUT_MS` | Runtime defaults. |
| `FC_REPO_MAP_MODE`, `FC_BOOTSTRAP_ENABLED` | Repository-map defaults. |
| `FC_INCLUDE_SNIPPETS` | Default snippet behavior. |
| `FAST_CONTEXT_DEBUG` | Enable progress diagnostics. |

Optional persistent configuration is a user-managed JSON file at `$HOME/.config/fast-context/config.json`:

```json
{"api_key":"your-api-key"}
```

Runtime credential priority is `FAST_CONTEXT_KEY` → local JSON `api_key` → `WINDSURF_API_KEY` → local Devin CLI/Windsurf credentials. The file is not auto-created; invalid JSON, unknown fields, unreadable files, and trailing JSON are errors.

## Structured output

### `skills list --format json`

```json
{
  "ok": true,
  "skills": [
    {
      "id": "fast-context",
      "aliases": ["semantic-code-search", "code-context"],
      "capabilities": ["semantic_code_search", "code_context"],
      "description": "..."
    }
  ],
  "total": 1
}
```

### `skills show fast-context --format json`

Returns `ok`, the same `skill` definition, and the raw `SKILL.md` in `content`. `--format content` writes only the raw Markdown to stdout.

### `doctor --format json`

```json
{
  "ok": true,
  "project": {"path": "...", "exists": true},
  "ripgrep": {"ok": true, "path": "...", "source": "fc_rg_path", "error": ""},
  "credentials": {"ok": true, "source_type": "...", "key": "...redacted..."},
  "version": {"version": "...", "commit": "...", "date": "..."}
}
```

`ripgrep.source` is `fc_rg_path` or `path`. Top-level `ok` is true only when the project exists, ripgrep resolves, and credentials resolve. Doctor keeps exit code `0`; inspect the fields.

`credentials.source_type` is `env` for either environment variable, `fast_context_config` for the JSON file, or the existing local TOML/SQLite source types. `credentials.key` is always redacted.

### `search --format json`

Successful output contains `files`, optional `rg_patterns`, and `meta`. Each file has a path and zero or more 1-based inclusive line ranges. Treat results as candidates and verify locally.

Failed JSON output contains:

```json
{
  "error": {
    "message": "...",
    "code": "AUTH_ERROR"
  }
}
```

## Exit codes and errors

| Code | Meaning |
| --- | --- |
| `0` | Command completed. Doctor availability is represented by JSON fields. |
| `1` | Runtime, network, authentication, rate-limit, service, or protocol failure. |
| `2` | Invalid arguments, unknown command, or unknown Skill. |

Known error categories include `AUTH_ERROR`, `RATE_LIMITED`, `PAYLOAD_TOO_LARGE`, `TIMEOUT`, `NETWORK`, `SERVER_ERROR`, and `PROTOCOL`.

The JavaScript launcher forwards the Go process exit status and signal. It injects the bundled `@vscode/ripgrep` path only when the user has not set `FC_RG_PATH`.

## Regression checks

```powershell
fast-context version
fast-context --version
fast-context -v
fast-context skills list --format json
fast-context skills show fast-context --format content
fast-context doctor --project . --format json
fast-context search "where is CLI command dispatch implemented" --project . --format json
```

The final search command transmits repository context externally. Run it after the `fast-context` Skill is loaded and `doctor --format json` reports `ok: true`; no separate per-search authorization is required. Use a public or dedicated fixture repository for regression checks.
