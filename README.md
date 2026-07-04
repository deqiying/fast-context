# fast-context

Go CLI version of `SammySnake-d/fast-context-mcp`.

This repository keeps the MCP tool behavior as a standalone command-line tool:

```powershell
go run ./cmd/fast-context search "where is auth handled" --project . --tree-depth 3 --max-turns 3
go run ./cmd/fast-context search "database connection pool" --project E:\Project\GoLand\sub2api --format json
go run ./cmd/fast-context key extract
go run ./cmd/fast-context doctor
```

## Commands

| Command | Purpose |
| --- | --- |
| `search <query>` | Run AI-driven code search through the Windsurf Devstral protocol. |
| `key extract` | Read `WINDSURF_API_KEY`, Devin CLI TOML, or Windsurf/Devin `state.vscdb`. |
| `doctor` | Check project path, ripgrep availability, credential candidates, and version info. |
| `version` | Print build metadata. |

## Notes

- The local executor only supports structured `rg`, `readfile`, `tree`, `ls`, and `glob` commands.
- Remote tool paths are mapped through `/codebase` and checked against project-root escape.
- `RIPGREP_CONFIG_PATH` is cleared when running `rg` to keep results deterministic.
- TLS verification is enabled by default. Set `FC_INSECURE_TLS=1` only for explicit local troubleshooting.
- API keys are redacted in command output.

## Development

Use a writable cache directory when the default Go cache is restricted:

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'fast-context-go-build'
$env:GOMODCACHE = Join-Path $env:TEMP 'fast-context-go-mod'
$env:GOPATH = Join-Path $env:TEMP 'fast-context-go-path'
go test ./...
go build ./cmd/fast-context
```

