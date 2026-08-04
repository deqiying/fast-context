---
name: fast-context
description: Use when an agent needs semantic discovery for unknown local-code entrypoints, business-intent-to-code mapping, architecture or data-flow analysis, call-path tracing, impact-area discovery, or candidate-file narrowing before edits, especially when broad rg searches would be noisy. Prefer deterministic local tools for exact paths, symbols, configuration keys, packet names, or error text. Do not use for web or remote-repository research, or when the user forbids sending code context to an external service.
---

# Fast Context

Use the `fast-context` CLI to locate likely local implementation files. Treat every returned path, line range, and grep pattern as a candidate until deterministic local reads, `rg`, builds, tests, or runtime evidence verify it.

## Decide before calling

Use semantic discovery for unknown entrypoints, business-intent mapping, architecture or data-flow analysis, multi-file call paths, and impact-area narrowing.

Skip it when an exact file, symbol, configuration key, packet name, error string, or narrow directory is already known. Use local deterministic tools directly for exhaustive matching. When this skip rule applies, do not resolve, schema-query, or preflight the CLI unless the user separately asks about `fast-context` itself. Do not use it for web research or remote repositories.

## Protect the external-data boundary

Treat `fast-context search` as the external-service operation. It sends the query, a repository map, and requested restricted-tool results to Windsurf Devstral.

Once this Skill is loaded and `fast-context doctor --project <target-project> --format json` reports a successful preflight, treat that as permission to run `search`; do not request separate per-search authorization. An explicit user instruction forbidding external transmission still takes precedence.

When external transmission is explicitly forbidden, continue with local tools without resolving, schema-querying, or preflighting the CLI unless the task is specifically diagnosing `fast-context`.

- Exclude sensitive, generated, or irrelevant directories before searching. Effective excludes constrain repo maps, bootstrap/hotspot discovery, retries, restricted-tool reads, final files, and snippets.
- Keep `--include-snippets` off by default. Enable it only when the code is safe to transmit and snippets materially reduce follow-up reads.
- Never print API keys, JWTs, npm tokens, full credential candidates, private diagnostic paths, or raw remote response bodies in user-facing output.

## Resolve the CLI and its contract

On Windows, locate the actual executable and version:

```powershell
Get-Command fast-context -All
fast-context --version
```

On macOS or Linux, use `type -a fast-context` when available, otherwise use `command -v fast-context`, then run `fast-context --version`.

If the CLI is missing, explain that npm directly owns it while a runtime manager may own Node/npm:

```text
mise -> Node/npm -> fast-context
```

Use `npm prefix --global`, `npm list --global --depth=0`, and direct command resolution for diagnosis. Ask before the first global install or update. Install the default channel with `npm install -g @deqiying/fast-context`; pin an exact version only when reproducibility is required.

Use known commands directly. When argv is unfamiliar or version drift is plausible, read only the targeted schema for the relevant canonical command:

```powershell
fast-context schema --format json search
fast-context schema --format json key extract
```

Reuse the same `cli.version + manifest_version + canonical path` contract within the current task. Use the full schema, `fast-context schema --format json`, only for first-time generic discovery or an explicit cache refresh. Generate canonical paths, not compatibility aliases, and follow `input_schema`, `x-cli-binding`, `availability`, `side_effects`, `output`, and `argv_conventions`.

`schema` is JSON-only and static: it does not read runtime configuration, credentials, project files, or environment values. It does not inspect the current directory, access the network, write files, or run command handlers. It is an input contract, not native-tool registration or permission to perform side effects. Use `--help` only for human help, never as a machine contract.

If `schema` is an unknown command, regardless of any wrapper-reported exit status, load the version-matched Skill from that binary and follow it instead of guessing new flags or silently updating the CLI:

```powershell
fast-context skills show --format content fast-context
```

Treat the loaded version-matched Skill as the sole CLI workflow from that point. Do not continue with `--reference` or any other version-sensitive command that it does not advertise.

When a command selects JSON, output is compact by default: one JSON line and one trailing LF. Keep Agent and automation calls compact. Add `--pretty` only for interactive human inspection; it does not select JSON or alter text/content output.

## Run the local preflight

Run doctor against the intended project:

```powershell
fast-context doctor --project "D:\path\to\repo" --format json
```

Inspect top-level `ok` plus `project.exists`, `ripgrep.ok`, and `credentials.ok`. Doctor intentionally returns exit code `0` even when a check is unavailable; proceed only when JSON reports `ok: true`. This local preflight does not prove credential validity, network reachability, remote acceptance, or result quality.

Credentials resolve from explicit fast-context configuration, the user-managed local config, Windsurf configuration, and finally the Linux/WSL Devin CLI TOML. `state.vscdb` files are never discovered automatically. Inspect a legacy database only through an explicit `fast-context key extract --path <state.vscdb> --format json` diagnostic. Read the CLI contract reference for exact precedence and config failure semantics.

## Search, narrow, and verify

Write the semantic query primarily in English and add local-language domain terms only when useful. Start with runtime defaults and canonical option-before-positional ordering:

```powershell
fast-context search `
  --project "D:\path\to\repo" `
  --max-results 10 `
  --format json `
  "where is user login authentication and JWT validation implemented"
```

- Add repeated canonical `--exclude` values for sensitive, generated, or noisy paths.
- Do not set `--tree-depth`, `--max-turns`, or `--max-commands` preemptively. Tune them from the targeted schema only after a concrete scope, result, or budget problem appears.
- Narrow `--project` before increasing search rounds when results are broad or empty.
- Read every returned source range and use exact `rg` references, tests, builds, or runtime checks before editing or answering.

## Fall back without blocking the task

If search fails because of network, TLS, authentication, rate limits, protocol drift, or service availability:

1. Report the concrete category and preserve a recognized remote code such as `invalid_argument` without exposing credentials or raw response bodies.
2. Use an installed local `codesearch` only when it already has a usable index.
3. If a new index is needed, start it in the background and do not wait for it in the current task.
4. Continue with `rg`, direct file reads, and repository-native tests when no ready semantic fallback exists.

## Load exact details only when needed

Read [references/cli-contract.md](references/cli-contract.md) for schema-to-argv mapping, structured output fields, credential precedence, exit codes, error categories, or regression commands. When this Skill came from the same binary, or targeted `schema skills show` advertises `--reference`, read the version-matched reference with:

```powershell
fast-context skills show --format content --reference cli-contract fast-context
```
