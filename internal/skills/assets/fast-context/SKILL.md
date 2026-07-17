---
name: fast-context
description: Use when an agent needs semantic discovery for unknown local-code entrypoints, business-intent-to-code mapping, architecture or data-flow analysis, call-path tracing, impact-area discovery, or candidate-file narrowing before edits, especially when broad rg searches would be noisy. Prefer deterministic local tools for exact paths, symbols, configuration keys, packet names, or error text. Do not use for web or remote-repository research, or when the user forbids sending code context to an external service.
---

# Fast Context

Use the `fast-context` CLI to find likely local implementation files, then verify every conclusion with deterministic local reads, `rg`, builds, or tests.

## Respect the external-data boundary

Treat `fast-context search` as an external-service operation. It sends the query, a repository map, and requested restricted-tool results to Windsurf Devstral.

- Do not run it when the user forbids external transmission.
- Exclude sensitive, generated, or irrelevant directories before searching.
- Keep `--include-snippets` off by default. Enable it only when the code is safe to transmit and snippets materially reduce follow-up reads.
- Never print API keys, JWTs, npm tokens, full credential candidates, or private diagnostic paths in user-facing output.

## Resolve and verify the CLI

On Windows, locate every candidate before choosing one:

```powershell
Get-Command fast-context -All
fast-context --version
```

On macOS or Linux, use `type -a fast-context` when the shell supports it, otherwise use `command -v fast-context`, and then run `fast-context --version`.

If the CLI is missing, explain that npm directly owns the CLI lifecycle while mise may own the outer Node runtime:

```text
mise -> Node/npm -> fast-context
```

Use `npm prefix --global`, `npm list --global --depth=0`, and direct command resolution to diagnose installation. Do not treat `mise which fast-context` as decisive for an npm-global package.

Ask for authorization before the first global install. Alpha releases intentionally use the default `latest` channel, so install with `npm install -g @deqiying/fast-context`. Pin an exact version when reproducibility is required.

## Run the local preflight

Run doctor against the intended project before search:

```powershell
fast-context doctor --project "D:\path\to\repo" --format json
```

Inspect `project.exists`, `ripgrep.ok`, `ripgrep.source`, and `credentials.ok`. The command intentionally returns exit code `0` even when a check is unavailable; use the JSON fields as the source of truth.

Resolve missing credentials locally with `fast-context key extract --format json` or `WINDSURF_API_KEY`. Do not copy the complete doctor object or credential source paths into public logs.

## Choose semantic search only when it helps

Use fast-context for intent-level discovery such as:

- locating where authentication, retries, caching, or a business workflow is implemented;
- mapping a design or bug report to likely files before editing;
- tracing architecture, ownership, data flow, or a multi-file call path;
- narrowing a repository when a generic repo-wide `rg` query would be noisy.

Skip it when an exact file, symbol, configuration key, packet name, error string, or narrow directory is already known. Use local deterministic tools directly for exhaustive matching.

## Search, narrow, read, and verify

Write the semantic query primarily in English and add local-language domain terms only when useful. Start lightweight:

```powershell
fast-context search "where is user login authentication and JWT validation handled" `
  --project "D:\path\to\repo" `
  --tree-depth 0 `
  --max-results 10 `
  --format json
```

Add focused `--exclude` values for noisy directories. Narrow `--project` after a broad or empty result. Only increase `--max-turns` when another search round is likely to expose a real call path.

Treat returned files, line ranges, and `rg_patterns` as candidates, not proof. Read the indicated source and use exact `rg` references, tests, builds, or runtime evidence before editing or answering.

## Fall back without blocking the task

If search fails because of network, TLS, authentication, rate limits, protocol drift, or service availability:

1. Report the concrete error category without exposing credentials.
2. Use an installed local `codesearch` only when it already has a usable index.
3. If a new index is needed, start it in the background and do not wait for it in the current task.
4. Continue with `rg`, direct file reads, and repository-native tests when no ready semantic fallback exists.

Read [references/cli-contract.md](references/cli-contract.md) when exact flags, JSON fields, exit codes, error categories, or regression commands are needed.
