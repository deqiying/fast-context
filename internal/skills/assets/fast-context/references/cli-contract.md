# fast-context CLI contract

## Contents

1. Installation and ownership
2. Commands
3. CLI command manifest
4. Search flags and environment
5. Structured output
6. Exit codes and errors
7. Regression checks

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
fast-context search <query> [flags, including --pretty]
fast-context key extract [flags, including --pretty]
fast-context doctor [flags, including --pretty]
fast-context skills list [--format text|json] [--pretty]
fast-context skills show <skill> [--format content|json] [--pretty]
fast-context schema [--format json] [--pretty] [<canonical-command-path>...]
fast-context version
fast-context --version
fast-context -v
```

`version`, `--version`, and `-v` print the same single line.

Without `--path`, `key extract` checks only the default Linux/WSL Devin CLI TOML. An explicit `--path` can inspect a selected TOML or `state.vscdb`; databases are never discovered automatically. The command bypasses runtime priority resolution, while `doctor` reports the effective source used by `search`.

## CLI command manifest

`fast-context schema --format json` returns the full versioned, machine-readable CLI command manifest. `fast-context schema --format json <canonical-command-path...>` returns the same envelope with exactly one command definition; for example, use `search` or `key extract`. A target must be a canonical path. Execution aliases remain supported for backwards compatibility, but the manifest does not encourage or resolve them as targets.

Use the targeted form when the intended command is known; use the full manifest only for first-time generic discovery or caching. The manifest records public command paths, aliases, input schema and argv bindings, default sources/normalization, availability and side effects, plus coarse output and exit-code contracts. Each flag binding includes a stable `order`; adapters emit flags in that order before any positional values. It does not invent a complete schema for existing search payloads, doctor maps, or credential results.

`schema` is JSON-only and static. It completes before runtime default/configuration resolution and does not read credentials, project files, the current directory, or environment values. It does not access the network, write files, or invoke `search`, `doctor`, `key extract`, or Skill-reading handlers. It is not native-tool registration: an Agent or host must deliberately consume and map the returned manifest before using it as a native tool.

## JSON presentation

When `search`, `key extract`, `doctor`, `skills list`, `skills show`, or `schema` selects JSON, stdout defaults to one compact JSON line followed by one LF. Pass `--pretty` for an explicit two-space-indented, human-readable rendering with the same trailing LF. The choice depends only on argv, not TTY, pipes, redirection, or the host environment.

`--pretty` is presentation-only: it does not change JSON fields, types, ordering, HTML-escaping policy, redaction, exit codes, or stdout/stderr routing. It is ignored when a command uses `text` or `content`, and it never implicitly changes `--format` to JSON. The existing default format for each command is unchanged. `version` is text-only and does not support `--pretty`.

JSON code blocks below use pretty layout for documentation readability; runtime JSON remains compact unless argv includes `--pretty`.

## Search flags and environment

| Flag | Meaning |
| --- | --- |
| `--project`, `-p` | Project root; defaults to the current directory. |
| `--tree-depth 0..6` | In `classic`, `0` selects project-size auto depth. In default `bootstrap_hotspot`, the global/hotspot depths come from their dedicated settings and the requested depth may raise hotspot depth. |
| `--max-turns 1..5` | Remote search rounds. |
| `--max-commands 1..20` | Locally enforced restricted-command cap for each main-search round. |
| `--max-results 1..30` | Maximum result files. |
| `--timeout <duration>` | Duration such as `30s`, or milliseconds such as `30000`. |
| `--exclude <pattern>` | Repeatable original-root-anchored boundary for repo maps, bootstrap/hotspots, retries, all five restricted tools, final files, and snippets. |
| `--include-snippets` | Include code snippets in the external request/result path. Off by default. |
| `--repo-map-mode` | `classic` or `bootstrap_hotspot`. |
| `--no-bootstrap` | Skip the bootstrap keyword and hotspot pass. |
| `--format` | `text` or `json`. |
| `--verbose` | Send progress diagnostics to stderr. |

Canonical Agent input for `--timeout` is `1s..300s`. This does not narrow existing explicit argv compatibility: `--timeout` continues to accept any value accepted by the duration parser. `FC_TIMEOUT_MS`, by contrast, is an environment-provided runtime default and is clamped to `1s..300s`.

Important environment variables:

| Variable | Meaning |
| --- | --- |
| `FAST_CONTEXT_KEY` | Highest-priority explicit fast-context credential. |
| `WINDSURF_API_KEY` | Explicit Windsurf/Devin credential. |
| `FC_RG_PATH` | Explicit ripgrep binary; takes precedence over `PATH`. |
| `FC_INSECURE_TLS=1` | Disable TLS verification for explicit local troubleshooting only. |
| `FC_MAX_TURNS`, `FC_MAX_COMMANDS`, `FC_TIMEOUT_MS` | Runtime defaults. |
| `FC_BOOTSTRAP_MAX_COMMANDS` | Locally enforced restricted-command cap for each bootstrap round. |
| `FC_REPO_MAP_MODE`, `FC_BOOTSTRAP_ENABLED` | Repository-map defaults. |
| `FC_INCLUDE_SNIPPETS` | Default snippet behavior. |
| `FAST_CONTEXT_DEBUG` | Enable progress diagnostics. |

Optional persistent configuration is a user-managed JSON file at `$HOME/.config/fast-context/config.json`:

```json
{"api_key":"your-api-key"}
```

Runtime credential priority is `FAST_CONTEXT_KEY` → local JSON `api_key` → `WINDSURF_API_KEY` → Linux/WSL Devin CLI TOML. Windsurf/Devin `state.vscdb` is not a runtime source and is never discovered automatically. The JSON file is not auto-created; invalid JSON, unknown fields, unreadable files, and trailing JSON are errors.

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

For `doctor`, `credentials.source_type` is `env` for either environment variable, `fast_context_config` for the JSON file, or `devin_cli_credentials` for the Linux/WSL TOML. An explicit `key extract --path <state.vscdb>` can still return `windsurf_state_db`; it is never an automatic `doctor` source. `credentials.key` is always redacted.

### `search --format json`

Successful output contains `files`, optional `rg_patterns`, and `meta`. Each file has a path and zero or more 1-based inclusive line ranges. Optional `meta.context_trimmed=true` means the conversation was compacted before a request or bounded recovery retry. Treat results as candidates and verify locally.

Failed JSON output contains:

```json
{
  "error": {
    "message": "...",
    "code": "invalid_argument"
  },
  "meta": {
    "error_code": "invalid_argument",
    "context_trimmed": true
  }
}
```

When the remote returns a valid structured error code, search preserves it in `error.code` (for example, `invalid_argument`); `meta.error_code` mirrors the final failure code when present. A successful recovery keeps `meta.context_trimmed=true` and leaves `meta.error_code` empty. These are existing optional fields; raw response bodies and credentials are never emitted.

## Exit codes and errors

| Code | Meaning |
| --- | --- |
| `0` | Command completed. Doctor availability is represented by JSON fields. |
| `1` | Runtime, network, authentication, rate-limit, service, or protocol failure. |
| `2` | Invalid arguments, unknown command, or unknown Skill. |

Known local error categories include `AUTH_ERROR`, `RATE_LIMITED`, `PAYLOAD_TOO_LARGE`, `TIMEOUT`, `NETWORK`, `SERVER_ERROR`, and `PROTOCOL`. Valid remote structured codes remain lowercase, such as `invalid_argument`. Only an `invalid_argument` received after a tool result may trigger one recovery retry, and only when the rebuilt protobuf is strictly smaller and within the request limit.

The JavaScript launcher forwards the Go process exit status and signal. It injects the bundled `@vscode/ripgrep` path only when the user has not set `FC_RG_PATH`.

## Regression checks

```powershell
fast-context version
fast-context --version
fast-context -v
fast-context skills list --format json
fast-context skills show fast-context --format content
fast-context schema --format json search
fast-context doctor --project . --format json
fast-context search "where is CLI command dispatch implemented" --project . --format json
```

The final search command transmits repository context externally. Run it after the `fast-context` Skill is loaded and `doctor --format json` reports `ok: true`; no separate per-search authorization is required. Use a public or dedicated fixture repository for regression checks.
