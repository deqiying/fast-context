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
fast-context search "where is authentication handled" --project . --format json
```

`search` sends the query, a repository map, and requested restricted-tool results to Windsurf. Do not use it for private code when external transmission is not authorized. Snippets are disabled by default.

## License and origin

MIT licensed. This Go implementation is derived from the behavior of [`fast-context-mcp`](https://github.com/SammySnake-d/fast-context-mcp), whose MIT notice is preserved in the repository license.
