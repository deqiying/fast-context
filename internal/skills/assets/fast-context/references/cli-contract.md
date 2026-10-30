# fast-context CLI contract

## Contents

1. Installation, ownership, and version drift
2. Public commands
3. Consume the command manifest
4. Read embedded Skill resources
5. JSON presentation
6. Preflight and credentials
7. Structured output
8. Exit codes, errors, and fallback
9. Regression checks

## Installation, ownership, and version drift

The npm package directly owns CLI installation and updates. A runtime manager may own the outer Node/npm runtime without owning `fast-context`:

```text
mise -> Node/npm -> fast-context
```

Resolve all candidates before choosing one.

On Windows:

```powershell
Get-Command node,npm,fast-context -All
node --version
npm --version
npm prefix --global
npm list --global --depth=0
fast-context --version
```

On macOS or Linux, use `type -a fast-context` or `command -v fast-context`, then run `fast-context --version`.

Ask before the first global install or update:

```powershell
npm install -g @deqiying/fast-context
```

Pin an exact package version when reproducibility is required. Do not treat `mise which fast-context` as decisive for an npm-global package.

Probe capabilities rather than hard-coding an unreleased minimum version. If `schema` is unknown, regardless of any wrapper-reported exit status, load the version-matched embedded Skill:

```powershell
fast-context skills show --format content fast-context
```

Treat that loaded Skill as the sole version-sensitive workflow. Do not attempt `--reference` or other newer commands unless it advertises them. Do not parse `--help` as a machine contract, guess newer flags, or silently update the installation.

## Public commands

```text
fast-context search <query> [flags, including --pretty]
fast-context key extract [flags, including --pretty]
fast-context doctor [flags, including --pretty]
fast-context skills list [--format text|json] [--pretty]
fast-context skills show <skill> [--reference cli-contract] [--format content|json] [--pretty]
fast-context schema [--format json] [--pretty] [<canonical-command-path>...]
fast-context version
fast-context --version
fast-context -v
```

`version`, `--version`, and `-v` print the same single line.

## Consume the command manifest

Use known commands directly. Query one targeted definition when parameters, bindings, availability, side effects, output, or version compatibility are uncertain:

```powershell
fast-context schema --format json search
fast-context schema --format json key extract
fast-context schema --format json skills show
```

Use `fast-context schema --format json` only for first-time generic discovery or an explicit cache refresh. Reuse a targeted definition for the same `cli.version + manifest_version + canonical path` within the current task.

Validate and map a targeted manifest in this order:

1. Require `ok=true`, the expected `manifest_version`, `scope.mode=command`, and the requested `scope.path`.
2. Generate `commands[0].path`; do not generate compatibility `aliases`.
3. Validate inputs against `input_schema.required`, types, enums, bounds, patterns, and `additionalProperties=false`.
4. Emit options in `x-cli-binding.order`, followed by positionals.
5. Repeat a repeatable array flag in input order.
6. Encode booleans as described by their binding. Preserve environment-backed absent/false distinctions.
7. Follow `argv_conventions` for scalar values, empty or leading-dash flag values, and end-of-options before a leading-dash positional.
8. Execute `availability.check_command` with its input bindings and evaluate `json_pointer` before the target workflow.
9. Review `side_effects`; the manifest describes effects but does not authorize them.
10. Interpret `output.default_format`, `formats`, `redaction`, and `exit_codes` without inventing a more detailed output schema.

`schema` is JSON-only and static. It completes before runtime default/configuration resolution and does not read credentials, project files, the current directory, or environment values. It does not access the network, write files, or invoke search, doctor, credential, or Skill-reading handlers. It is not native-tool registration: a host must deliberately consume and map it.

## Read embedded Skill resources

Read the main version-matched Skill as raw Markdown:

```powershell
fast-context skills show --format content fast-context
```

Read this reference through its allowlisted logical ID:

```powershell
fast-context skills show --format content --reference cli-contract fast-context
```

`--reference` accepts `cli-contract`, not a filesystem or embed path. Content mode writes only raw Markdown. JSON mode returns `ok`, `skill`, `content`, and `resource: "references/cli-contract.md"` for the reference. Omitting `--reference` preserves the original main-Skill output shape.

## JSON presentation

When `search`, `key extract`, `doctor`, `skills list`, `skills show`, or `schema` selects JSON, stdout defaults to one compact JSON line followed by one LF. Keep this default for Agents and automation.

Add `--pretty` only for explicit human inspection. It uses two-space indentation and the same trailing LF. Presentation depends only on argv, not TTY, pipes, redirection, platform, or the host environment.

`--pretty` does not select JSON or change fields, types, ordering, HTML escaping, redaction, exit codes, or stdout/stderr routing. It has no effect in text/content modes. Existing default formats remain unchanged, and text-only `version` does not accept it.

Schema JSON keeps HTML characters unescaped; other JSON paths preserve Go's default HTML escaping.

## Preflight and credentials

Run the local availability check before search:

```powershell
fast-context doctor --project . --format json
```

Top-level `ok` is true only when the project exists, ripgrep resolves, and credentials resolve. Doctor intentionally keeps exit code `0` when a check is unavailable, so inspect JSON rather than exit status alone. A successful local check does not prove credential validity, network reachability, remote acceptance, or result quality.

Runtime credential priority is:

1. `FAST_CONTEXT_KEY`
2. `api_key` in `$HOME/.config/fast-context/config.json`
3. `WINDSURF_API_KEY`
4. Linux/WSL Devin CLI TOML

The optional config is user-managed and strict:

```json
{"api_key":"your-api-key"}
```

The CLI never creates or rewrites it. Invalid JSON, unknown fields, unreadable files, trailing JSON, or a truncated explicit fast-context token are errors rather than silent fallback.

Windsurf/Devin `state.vscdb` is never an automatic runtime source. Without `--path`, `key extract` checks only the default Linux/WSL Devin CLI TOML. Inspect a selected TOML or legacy database explicitly:

```powershell
fast-context key extract --path <state.vscdb> --format json
```

This diagnostic bypasses runtime priority resolution. It may create a temporary SQLite snapshot and normally removes it before returning. Never expose returned credential material, candidate paths, or private diagnostic paths.

## Structured output

### `skills list --format json`

Returns `ok`, `skills`, and `total`. Each Skill definition contains `id`, `aliases`, `capabilities`, and `description`.

### `skills show --format json`

Without `--reference`, returns `ok`, the Skill definition, and raw `SKILL.md` in `content`. With `--reference cli-contract`, it additionally returns `resource` and places this Markdown in `content`.

Use `--format content` when an Agent needs to load Markdown directly; JSON escaping adds no value in that workflow.

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

`ripgrep.source` is `fc_rg_path` or `path`. `credentials.source_type` is `env`, `fast_context_config`, or `devin_cli_credentials`; an explicit legacy database extraction may return `windsurf_state_db`. Credential values are redacted.

### `search --format json`

Successful output contains `files`, optional `rg_patterns`, and `meta`. Each file contains a path and zero or more 1-based inclusive line ranges. Optional `meta.context_trimmed=true` reports bounded request compaction or recovery. Optional `meta.salvaged_response=true` means the remote model never called its final `answer` tool; the files were recovered from its reply or from files it had already read, and may be incomplete. Treat all results as candidates and verify them locally.

`search` exits with code `1` before any credential read or network request when the effective project root is exactly the user home directory or a filesystem root. Pass a specific repository with `--project` instead.

Failed JSON output contains `error.message`, `error.code`, and optional `meta.error_code` / `meta.context_trimmed`. Valid remote structured codes remain lowercase, such as `invalid_argument`; local categories remain uppercase.

## Exit codes, errors, and fallback

| Code | Meaning |
| --- | --- |
| `0` | Command completed; doctor JSON may still report `ok=false`. |
| `1` | Runtime, network, authentication, rate-limit, service, protocol, embedded-resource, or local-output failure. |
| `2` | Invalid arguments, unknown command, unknown Skill, or disallowed reference. |

Known local error categories include `AUTH_ERROR`, `RATE_LIMITED`, `PAYLOAD_TOO_LARGE`, `TIMEOUT`, `NETWORK`, `SERVER_ERROR`, and `PROTOCOL`. Preserve valid remote codes such as `invalid_argument`. Do not expose raw response bodies or credentials.

Only a qualifying `invalid_argument` after a tool result may trigger the CLI's bounded recovery retry. Agents should not add their own blind retry loop.

When search cannot run:

1. Report the concrete safe error category.
2. Use an installed local `codesearch` only when a usable index already exists.
3. Start a new index in the background rather than blocking the current task.
4. Continue with `rg`, direct reads, tests, and repository-native tools.

The JavaScript launcher forwards the Go child exit status and signal. It injects bundled ripgrep only when `FC_RG_PATH` is unset.

## Regression checks

```powershell
fast-context version
fast-context --version
fast-context -v
fast-context skills list --format json
fast-context skills show --format content fast-context
fast-context skills show --format content --reference cli-contract fast-context
fast-context schema --format json search
fast-context doctor --project . --format json
fast-context search --project . --format json "where is CLI command dispatch implemented"
```

The final command transmits repository context. Run it only after this Skill is loaded and doctor JSON reports `ok=true`; no separate per-search authorization is required. Use a public or dedicated fixture for regression checks.
