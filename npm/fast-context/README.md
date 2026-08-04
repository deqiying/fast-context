# fast-context

`fast-context` is a Go CLI for semantic discovery in local codebases. It uses Windsurf Devstral to narrow unknown entrypoints, then expects the caller to verify returned files and line ranges with local deterministic tools.

## Install

Alpha releases are intentionally available through the default installation channel:

```sh
npm install -g @deqiying/fast-context
fast-context --version
```

`@deqiying/fast-context@latest` is equivalent. Pin an exact version when reproducibility is required; `latest` may point to an Alpha until the stable-release gate is met.

Supported npm platforms:

- Windows x64
- Linux x64
- macOS arm64

The launcher installs platform-specific Go binaries as optional dependencies and uses `@vscode/ripgrep`. A user-provided `FC_RG_PATH` always takes precedence.

## Agent workflow

```sh
fast-context doctor --project . --format json
fast-context skills show fast-context --format content
# Read the exact contract for a known command before constructing unfamiliar argv.
fast-context schema --format json search
fast-context search "where is authentication handled" --project . --format json
```

`search` sends the query, a repository map, and requested restricted-tool results to Windsurf. Do not use it for private code when external transmission is not authorized. Snippets are disabled by default.

`schema --format json` returns the full versioned CLI command manifest; `schema --format json <canonical-command-path...>` returns one definition, such as `search` or `key extract`. Prefer the targeted form when the command is known, and use the full form only for first-time discovery or caching. It accepts canonical paths only, is JSON-only and static, and does not read configuration, credentials, project/CWD, or environment values; it does not access the network, write files, or run normal command handlers. The manifest is not native-tool registration: a host must explicitly map it if native tools are required.

## Local credentials

For a persistent user-level key, create `$HOME/.config/fast-context/config.json` yourself:

```json
{
  "api_key": "your-api-key"
}
```

The CLI does not create or rewrite this file. Runtime credentials are resolved in this order: `FAST_CONTEXT_KEY`, the JSON `api_key`, `WINDSURF_API_KEY`, then the Linux/WSL Devin CLI `~/.local/share/devin/credentials.toml`. Windsurf/Devin `state.vscdb` files are never discovered automatically. Use `fast-context doctor --format json` to inspect the effective source; use `fast-context key extract --path <state.vscdb>` only when you explicitly intend to inspect a legacy database. Never commit the file or print a complete key in logs.

`FC_TIMEOUT_MS` is clamped as a `1s..300s` runtime default. An explicit `--timeout` keeps the CLI's historical duration-parser compatibility and is not clamped to that environment range; the manifest's canonical Agent range is intentionally `1s..300s`.

## License and origin

MIT licensed. This Go implementation is derived from the behavior of [`fast-context-mcp`](https://github.com/SammySnake-d/fast-context-mcp), whose MIT notice is preserved in the repository license.
