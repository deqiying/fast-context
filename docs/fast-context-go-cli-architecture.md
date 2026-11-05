# fast-context Go CLI 技术架构方案

## 1. 结论

参考 `SammySnake-d/fast-context-mcp` 改写为 Go 版 CLI 是可行的，但不是简单把 MCP server 换成命令行入口。真正复杂度集中在 Windsurf 私有 Connect-RPC/Protobuf 协议、认证链路、远端模型与本地受限工具的多轮交互，以及跨平台凭据读取。

总体复杂度建议评为中高：

| 范围 | 复杂度 | 原因 |
| --- | --- | --- |
| 只做 CLI 外壳 | 低 | MCP 层本身很薄，Go 命令行入口容易替换 |
| 实现同等搜索能力 | 中高 | 需要完整移植协议编码、认证、流式响应解析、搜索回合编排 |
| 达到可发布跨平台工具 | 高 | 需要处理 Windows/macOS/Linux 凭据、ripgrep 分发、协议漂移、错误诊断 |

推荐使用 Go 实现核心能力，并把 MCP 兼容层作为后续可选子命令，而不是 MVP 必需项。该子命令已作为 `fast-context mcp serve` 实现，见 [fast-context-mcp-serve-plan.md](fast-context-mcp-serve-plan.md)。

## 2. 参考实现快照

上游仓库：`https://github.com/SammySnake-d/fast-context-mcp`

本次评估参考的上游提交：`1fb45dab4cab009fe6ab247a6beca7008cca1b1b`（GitHub v1.2.2）

> **2026-07-05 更新**：GitHub 仓库已停更在 v1.2.2；npm 包 `fast-context-mcp` 持续演进（当前 1.5.2）。
> 本项目已对齐 npm 1.5.2 的功能：bootstrap + hotspot repo map（`internal/dirscore` BM25F/probe/RRF）、
> grep keywords 本地扩展、无结果自动重试、`--include-snippets` 代码片段输出、`classic` 下的 `--tree-depth 0` 自动深度、
> 智能上下文裁剪 + 320KB 真实 protobuf 发送前 gate、original-root excludes policy、FC_* 环境变量配置、key 截断自愈。
> 后续对齐请以 `npm pack fast-context-mcp@latest` 解包内容为基准，而不是 GitHub 仓库。

上游当前是 Node.js MCP server：

| 文件 | 作用 |
| --- | --- |
| `src/server.mjs` | MCP 工具注册，暴露 `fast_context_search` 和 `extract_windsurf_key` |
| `src/core.mjs` | 搜索主流程、Windsurf 认证、Connect-RPC 请求、响应解析、回合控制 |
| `src/executor.mjs` | 本地受限命令执行：`rg`、`readfile`、`tree`、`ls`、`glob` |
| `src/protobuf.mjs` | 手写 Protobuf 编码/解码和 Connect-RPC frame 处理 |
| `src/extract-key.mjs` | 从本地 Windsurf/Devin 配置与 SQLite 数据库提取 API key |

上游依赖包括 `@modelcontextprotocol/sdk`、`@vscode/ripgrep`、`sql.js`、`tree-node-cli` 和 `zod`。其中 MCP SDK、Zod 可以在 CLI 化后移除；其余能力需要用 Go 重新实现或替代。

## 3. 目标范围

MVP 目标是一个可独立运行的 Go CLI：

```powershell
fast-context search --project . --tree-depth 3 --max-turns 3 --max-results 10 "where is auth handled"
fast-context search --project E:\Project\GoLand\sub2api --format json "database connection pool"
fast-context key extract --path <state.vscdb>
fast-context doctor
```

MVP 必须保持的功能：

| 功能 | Go CLI 对应设计 |
| --- | --- |
| 自然语言代码搜索 | `fast-context search <query>` |
| 指定项目路径 | `--project`，默认当前目录 |
| 控制 repo map 深度 | `--tree-depth 1..6` |
| 控制远端搜索轮数 | `--max-turns 1..5` |
| 控制返回文件数量 | `--max-results 1..30` |
| 排除大目录 | `--exclude node_modules --exclude dist` |
| 自动读取 API key | 环境变量优先，本地凭据兜底 |
| 返回文件和行号范围 | 默认 text，另提供 `--format json` |
| 诊断信息 | 输出 tree size、实际 depth、timeout、错误类型和建议 |

非 MVP 范围：

| 功能 | 建议 |
| --- | --- |
| MCP server 兼容 | 已实现为 `fast-context mcp serve`（stdio），见 [fast-context-mcp-serve-plan.md](fast-context-mcp-serve-plan.md) |
| GUI/TUI | 暂不做 |
| 自建语义索引 | 暂不做，保持 Windsurf Devstral 搜索模式 |
| 替换为其它 LLM provider | 暂不做，会改变核心产品语义 |

## 4. 总体架构

```mermaid
flowchart TD
    A["CLI command"] --> R["CLI dispatch / command contract registry"]
    R --> M["Static CLI command manifest"]
    M --> N["Versioned JSON manifest"]
    R --> B["Config and credential loader"]
    B --> C["Repo map builder"]
    C --> D["Search orchestrator"]
    D --> E["Windsurf client"]
    E --> F["Connect-RPC and Protobuf codec"]
    D --> G["Restricted tool executor"]
    G --> H["rg / readfile / tree / ls / glob"]
    E --> D
    D --> I["Result parser"]
    I --> J["Text or JSON output"]
```

推荐目录结构：

```text
cmd/fast-context/main.go
internal/cli/
internal/commandcontract/
internal/config/
internal/credentials/
internal/repomap/
internal/dirscore/
internal/executor/
internal/pathfilter/
internal/textutil/
internal/windsurf/
internal/protowire/
internal/search/
internal/output/
internal/version/
```

模块职责：

| 模块 | 职责 |
| --- | --- |
| `internal/cli` | 基于 command contract 的分派、参数解析、子命令、退出码、用户可读错误；`schema` 在运行时解析前静态返回；`mcp serve` 的信号处理与退出码映射 |
| `internal/commandcontract` | 公共 canonical path、兼容 alias、参数、argv binding、help 与 manifest 的单一事实源；`internal/cli` 负责验证 definition 与 handler binding 一一对应 |
| `internal/config` | 读取 `FC_*` env 和 flag 默认值，并严格解析用户级 JSON 配置 |
| `internal/credentials` | 按优先级解析 `FAST_CONTEXT_KEY`、本地 JSON、`WINDSURF_API_KEY` 和 Linux/WSL Devin CLI TOML；仅在显式 `--path` 时读取 `state.vscdb` |
| `internal/repomap` | 生成 `/codebase` 虚拟树，自动 depth fallback |
| `internal/dirscore` | 生成目录 profile、probe、hotspot 与 path spine 评分 |
| `internal/executor` | 受限本地命令执行和路径沙箱 |
| `internal/pathfilter` | 以初始项目根为锚点，统一约束枚举、读取、重试与最终结果 |
| `internal/textutil` | 非法 UTF-8 修复与 rune-safe byte-budget 截断 |
| `internal/windsurf` | 认证、JWT 缓存、rate limit 检查、流式请求 |
| `internal/protowire` | 无 `.proto` 场景下的 Protobuf wire 编码和 Connect frame |
| `internal/search` | 多轮搜索编排、工具调用回填、force-answer 逻辑；`DefaultOptions` 为 CLI 与 MCP 共用的运行时默认值映射 |
| `internal/output` | text/json 输出、错误诊断、敏感信息脱敏；hint 措辞按 CLI 与 MCP 两个调用面区分 |
| `internal/doctor` | 本地就绪检查与报告（项目、ripgrep、凭据来源），供 CLI `doctor` 与 MCP `fast_context_doctor` 共用 |
| `internal/mcpserver` | stdio MCP server，注册 `fast_context_search` 与 `fast_context_doctor`；唯一依赖 MCP Go SDK 的包 |

## 5. 核心数据流

`search` 命令执行流程：

1. 解析 CLI 参数并确定 `projectRoot`。
2. 读取 API key：按 `FAST_CONTEXT_KEY` → `$HOME/.config/fast-context/config.json` → `WINDSURF_API_KEY` → Linux/WSL Devin CLI TOML 解析；运行时不自动发现 `state.vscdb`。
3. 通过认证接口换取 JWT，并按 JWT `exp` 做内存缓存。
4. 调用 rate limit 检查接口，失败则直接返回可读错误。
5. 从初始 `projectRoot` 创建 immutable excludes policy，并在 repo map、hotspot 与 path spine 中复用。
6. 构造 system prompt、tool schema 和用户消息；`classic` 的 auto depth 与默认 `bootstrap_hotspot` 的 global/hotspot depth 分别处理。
7. 构造真实未压缩 protobuf，校验全部 string 为合法 UTF-8，并在 HTTP 前强制 `320 KiB` 上限。
8. 向 Windsurf Devstral stream 接口发起 Connect-RPC 请求并保留结构化远端错误码。
9. 严格解析 `[TOOL_CALLS]restricted_exec[ARGS]{...}`，按数字顺序验证 `command1..commandN` 并本地强制命令与字段预算。
10. 在 excludes policy 内并发执行受限命令，以完整 wrapper 和整轮 byte budget 组装 tool result。
11. 对已含 tool result 的 `invalid_argument`，仅在真实 protobuf 严格缩小且不超限时重发一次，不重复执行命令。
12. 重复 `max_turns` 轮，最后注入 force-answer prompt。
13. 解析 `<ANSWER>` XML，过滤 excluded path 后输出文件路径、行号范围、grep keywords 与可选 snippets。

## 6. Go 实现关键点

### 6.1 CLI 层

CLI 以 typed command contract registry 作为 routing、parser、help 与 manifest 的单一事实源。`schema` 必须在 runtime defaults、config、credentials、project filesystem 和网络调用之前分派，以确保能力发现不依赖本机状态。现有 CLI 保持标准库实现；不因 manifest 引入新的 parser 框架或依赖。

顶层分派顺序固定为：

```text
--version / -v
  -> root / namespace / leaf help
  -> canonical / compatibility-alias route
  -> typed argv parse and validation
  -> static schema execution
  -> command ID to handler binding
  -> runtime defaults / config / credentials / filesystem / network
```

公共 executable commands：

```text
fast-context search <query>
fast-context key extract
fast-context doctor
fast-context skills list
fast-context skills show <skill> [--reference cli-contract]
fast-context schema [--format json] [--pretty] [<canonical-command-path>...]
fast-context version
```

`skills show` 默认返回主 `SKILL.md`。可选 `--reference cli-contract` 只接受稳定逻辑 ID，并从同一二进制返回 `references/cli-contract.md`；它不接受 filesystem/embed path。未传该 option 时既有 content/JSON shape 不变，读取仍是无网络、无外部文件访问的 `embedded_resource_read`。

建议全局 flag：

| Flag | 默认值 | 说明 |
| --- | --- | --- |
| `--project` | `.` | 项目根目录 |
| `--tree-depth` | `3` | `classic` 的 repo map 深度；`0` 才启用 project-size auto。默认 optimizer 使用独立 global/hotspot depth |
| `--max-turns` | `3` | 搜索回合数 |
| `--max-commands` | `8` | 主搜索每轮本地强制的 restricted command 上限 |
| `--max-results` | `10` | 返回文件数量 |
| `--timeout` | `30s` | 单次 Windsurf stream timeout |
| `--exclude` | 空 | 可重复传入；相对初始项目根解释并覆盖本地读取与最终结果 |
| `--format` | `text` | `text` 或 `json` |
| `--pretty` | `false` | 仅影响 JSON 的物理排版；省略时 compact，显式时两空格缩进，不改变 format |
| `--verbose` | `false` | 打印进度和诊断 |

#### CLI command manifest

`fast-context schema --format json` 生成完整、版本化的 CLI command manifest；`fast-context schema --format json <canonical-command-path...>` 只返回一个与完整清单中深度一致的 command definition。target 只接受 canonical path；历史 alias 继续由执行路由兼容，但不用于 Agent 生成。

Manifest 的 input schema 描述 canonical Agent 输入，另以 `x-cli-*` 扩展记录 argv binding、环境默认来源、normalization 和既有兼容语义。它只声明粗粒度 output contract，避免为现有 search result、doctor map 和 credential result 伪造不稳定的完整 output schema。`schema` 自身只支持 JSON。

任一命令选择 JSON 时，运行时默认写入单行 compact JSON 和一个结尾 LF；`--pretty` 才请求两空格缩进和同样的结尾 LF。该 presentation 选择只由 argv 决定，不随 TTY、pipe、重定向或宿主环境变化。`search`、`key extract`、`doctor`、`skills list`、`skills show` 与 `schema` 支持该 flag；它在 text/content 模式无效果且不隐式选择 JSON，既有 default format 不变，text-only `version` 不支持该 flag。正常 JSON 保持 Go 默认 HTML escaping，schema 继续使用 `SetEscapeHTML(false)`；schema runtime 默认 compact，而其 golden 显式使用 pretty 以保留可审阅性。

该路径是静态只读能力发现：不读取 runtime config、credentials、project/CWD 或环境变量值，不访问网络、不写文件，也不触发 `search`、`doctor`、`key extract` 或 embedded Skill handler。它不会把命令自动注册为宿主 native tool；需要 native tool calling 的宿主必须显式消费并映射 manifest。这个静态 manifest 也不替代 search 的外传授权、doctor preflight 或返回候选后的本地复核。

`--timeout` 的 canonical Agent 输入范围为 `1s..300s`。`FC_TIMEOUT_MS` 作为环境运行时默认在此范围内 clamp；显式 `--timeout` 保持既有 duration parser 兼容行为，不因 manifest 被 clamp 或收紧。

### 6.2 Protobuf 与 Connect-RPC

这是移植中最重要的模块。

上游没有 `.proto` 文件，使用手写 encoder 按字段号写 Protobuf wire format。Go 版有两种选择：

| 方案 | 评价 |
| --- | --- |
| 继续手写 varint/tag/length-delimited | 依赖最少，可完全贴合上游，但测试要求高 |
| 使用 `google.golang.org/protobuf/encoding/protowire` | 仍然不需要 `.proto`，但可减少低级编码错误 |

推荐使用 `protowire`，并为每个请求生成 golden bytes 测试。

必须复刻的协议点：

| 协议点 | 说明 |
| --- | --- |
| Auth endpoint | `https://server.self-serve.windsurf.com/exa.auth_pb.AuthService/GetUserJwt` |
| API endpoint | `https://server.self-serve.windsurf.com/exa.api_server_pb.ApiServerService/GetDevstralStream` |
| Unary content type | `application/proto` |
| Streaming content type | `application/connect+proto` |
| Connect frame | 1 字节 flags + 4 字节 big endian length + payload |
| 压缩 | 请求/响应需要处理 gzip |
| 响应解析 | 从 frame 中提取字符串，再解析 `[TOOL_CALLS]...` |

Go 模块建议：

```go
type Encoder struct {
    buf []byte
}

func (e *Encoder) String(field protowire.Number, value string)
func (e *Encoder) Bytes(field protowire.Number, value []byte)
func (e *Encoder) Message(field protowire.Number, msg []byte)

func EncodeConnectFrame(proto []byte, gzip bool) ([]byte, error)
func DecodeConnectFrames(raw []byte) ([][]byte, error)
```

### 6.3 Windsurf client

`internal/windsurf` 负责所有远端交互，避免协议细节泄漏到搜索编排层。

建议接口：

```go
type Client interface {
    FetchJWT(ctx context.Context, apiKey string, timeout time.Duration) (string, error)
    CheckRateLimit(ctx context.Context, apiKey, jwt string, timeout time.Duration) (bool, error)
    RequestSize(apiKey, jwt string, messages []Message, toolDefs string) (int, error)
    Stream(ctx context.Context, apiKey, jwt string, messages []Message, toolDefs string, timeout time.Duration) ([]byte, error)
    ParseResponse(data []byte) (string, *ToolCall, error)
}
```

错误需要分类：

| 错误码 | 判定 |
| --- | --- |
| `AUTH_ERROR` | HTTP 401/403 或 JWT 获取失败 |
| `RATE_LIMITED` | HTTP 429 或 rate limit 检查失败 |
| `PAYLOAD_TOO_LARGE` | HTTP 413 或服务端 payload 相关错误 |
| `TIMEOUT` | context deadline / client timeout |
| `NETWORK` | DNS、TLS、连接失败 |
| `SERVER_ERROR` | HTTP 5xx 或无合法远端 code 的 HTTP 4xx |
| `PROTOCOL` | frame/protobuf/response parse 失败 |
| 合法小写远端 code | HTTP 4xx 或 Connect error 中满足格式约束的结构化 code，例如 `invalid_argument` |

`RequestSize` 与 `Stream` 必须复用同一个 request builder。`Stream` 在 Connect frame 与 HTTP request 构造前，以真实未压缩 protobuf bytes 强制 `320 KiB` 上限；超限时本地返回 `PAYLOAD_TOO_LARGE`，不得发网。

HTTP error body 与 Connect error frame 共用受限 parser。HTTP 401/403、429、413、5xx 的本地分类优先；其他 HTTP 4xx 和 Connect error 可保留合法远端 code。body 与 message 必须限长并清理控制字符，不能记录或返回完整 raw body。

unary 响应压缩由 Go `http.Transport` 负责：调用方不得手动设置 `Accept-Encoding: gzip`，否则标准库不会透明解压 gzip 响应，压缩字节会被误交给 protobuf 解析器。`Content-Encoding: gzip` 仅在请求体确实压缩时设置；streaming path 继续使用自己的 Connect frame 压缩契约。

上游 Node 代码会在 TLS 失败后自动禁用证书校验。Go 版不建议默认复制这个行为，建议改为显式 `FC_INSECURE_TLS=1` 才允许关闭校验，并在输出里明确提示。

### 6.4 本地受限工具执行

CLI 的安全边界应与上游一致：远端模型不能执行任意 shell，只能请求结构化命令。

支持命令：

| 命令 | Go 实现 |
| --- | --- |
| `rg` | 调用 ripgrep 二进制 |
| `readfile` | Go 读取 UTF-8 文本，按 1-based inclusive 行号返回 |
| `tree` | Go 自己遍历目录并格式化树 |
| `ls` | Go `os.ReadDir` |
| `glob` | `doublestar` 或自实现递归 glob |

路径安全要求：

1. 远端只能看到 `/codebase` 虚拟路径。
2. 所有路径都必须映射回 `projectRoot` 内部。
3. `..`、绝对路径逃逸、symlink 逃逸需要拒绝或规范化后检查。
4. `rg` 必须使用参数数组执行，禁止拼接 shell 命令。
5. `RIPGREP_CONFIG_PATH` 应清空，避免用户本地 rg 配置污染结果。
6. effective excludes 以初始项目根为锚点，覆盖 repo map、dirscore/path spine、bootstrap/main executor、retry、auto-grep、final files 与 snippets。
7. `--max-commands` 和 Bootstrap 对应上限必须由本地验证执行；schema 只是模型提示。
8. 本地文件与命令结果在 protobuf 前必须规范化为合法 UTF-8，所有 byte 截断都不能拆分 rune。

ripgrep 分发建议：

| 方案 | 优点 | 缺点 |
| --- | --- | --- |
| 依赖系统 `rg` | 实现最快 | 不满足“无系统依赖”的上游体验 |
| 发布包内携带 per-platform `rg` | 行为最接近上游 | release 流程更复杂 |
| 纯 Go 搜索替代 | 单文件发布简单 | regex、ignore、性能和输出行为难完全一致 |

推荐 MVP 先查找内置/旁路 `rg`，找不到再查 PATH；发布阶段用 GoReleaser 打包 per-platform ripgrep。

### 6.5 凭据读取

运行时凭据优先级为：

1. `FAST_CONTEXT_KEY`
2. `$HOME/.config/fast-context/config.json` 的 `api_key`
3. `WINDSURF_API_KEY`
4. 仅 Linux/WSL：Devin CLI `~/.local/share/devin/credentials.toml`

固定 JSON 配置只接受 `api_key` 字段，使用 `encoding/json` 的 `DisallowUnknownFields` 严格解析。文件不存在或空 `api_key` 继续低优先级解析；读取失败、损坏 JSON、未知字段和尾随 JSON 文档直接报错。`FAST_CONTEXT_KEY` 与 JSON key 疑似被 `$` 截断时直接报错；截断的 `WINDSURF_API_KEY` 只允许从 Linux/WSL Devin CLI TOML 恢复，无可用 TOML 时在网络请求前失败。

Windsurf/Devin `state.vscdb` 不属于运行时自动凭据来源。只有用户明确执行 `fast-context key extract --path <state.vscdb>` 时才进入 SQLite 读取路径；`doctor.credentials.candidates` 不得列出数据库默认路径。

显式 SQLite 读取实现：

| 方案 | 评价 |
| --- | --- |
| `modernc.org/sqlite` | 纯 Go，无 CGO，跨平台发布简单，推荐 |
| `github.com/mattn/go-sqlite3` | 成熟但需要 CGO，Windows 交叉编译麻烦 |

显式读取 `state.vscdb` 时查询：

```sql
SELECT value FROM ItemTable WHERE key = 'windsurfAuthStatus'
```

再从 JSON 字段中取 `apiKey`。为避免数据库锁冲突，CLI 先复制到临时文件再只读打开，并在正常返回和可恢复错误路径清理快照；进程异常终止仍可能留下临时文件。

### 6.6 搜索编排

`internal/search` 负责把远端模型和本地工具串起来。

建议核心类型：

```go
type Options struct {
    Query       string
    ProjectRoot string
    TreeDepth   int
    MaxTurns    int
    MaxCommands int
    MaxResults  int
    Timeout     time.Duration
    Exclude     []string
}

type Result struct {
    Files      []ResultFile `json:"files"`
    RgPatterns []string     `json:"rg_patterns,omitempty"`
    Meta       Meta         `json:"meta"`
}

type ResultFile struct {
    Path   string      `json:"path"`
    Ranges []LineRange `json:"ranges"`
}
```

需要保留的行为：

1. 初始消息包含 problem statement 和 repo map。
2. 每轮远端只能调用一次 `restricted_exec`，但其中可包含多个 `commandN`；本地只接受 `command1..commandN`，严格拒绝未知字段、越界编号和超预算字段。
3. validated commands 按数字顺序并发执行；执行、hints、state 与写回历史的 `ToolArgsJSON` 必须来自同一批数据。
4. assistant thinking、serialized tool args 和单轮 tool results 都有内部 byte budget；tool-result wrapper 必须完整且 UTF-8 合法。
5. 如果远端没有生成有效命令，可补偿最多 2 轮。
6. 对已含 tool result 的 `invalid_argument` 最多恢复一次；候选请求必须用真实 protobuf 证明严格变小且不超限。
7. 最后一轮注入 force-answer prompt。
8. 解析 `<ANSWER>` XML，忽略越界、excluded 或不存在文件，返回真实路径。
9. 输出去重后的 grep keywords。

### 6.7 输出格式

默认 text 输出应贴近上游：

```text
Found 3 relevant files.

  [1/3] E:\Project\example\internal\auth\service.go (L12-80, L130-160)
  [2/3] E:\Project\example\internal\auth\middleware.go (L1-90)
  [3/3] E:\Project\example\cmd\server\main.go (L40-75)

grep keywords: AuthService, RequireAuth, token

[config] tree_depth=3, tree_size=12.5KB, max_turns=3, max_results=10, timeout_ms=30000
```

JSON 输出用于脚本和 agent 集成：

```json
{
  "files": [
    {
      "path": "E:\\Project\\example\\internal\\auth\\service.go",
      "ranges": [{"start": 12, "end": 80}]
    }
  ],
  "rg_patterns": ["AuthService"],
  "meta": {
    "tree_depth": 3,
    "tree_size_kb": 12.5,
    "max_turns": 3,
    "max_results": 10,
    "context_trimmed": true
  }
}
```

`meta.context_trimmed` 与 `meta.error_code` 是既有可选字段。恢复成功时只保留 `context_trimmed=true`；最终失败时 `error.code` 与 `meta.error_code` 可为合法小写远端 code，例如 `invalid_argument`。

## 7. 依赖建议

| 用途 | 推荐依赖 | 说明 |
| --- | --- | --- |
| CLI | `spf13/cobra` | 多子命令更省心；也可先用标准库 |
| Protobuf wire | `google.golang.org/protobuf/encoding/protowire` | 无 `.proto` 也能安全写 wire format |
| SQLite | `modernc.org/sqlite` | 纯 Go，仅用于显式 `key extract --path <state.vscdb>`，适合跨平台发布 |
| TOML | `github.com/pelletier/go-toml/v2` | 读取 Devin CLI credentials |
| Glob | `github.com/bmatcuk/doublestar/v4` | 支持 `**` |
| XML | 标准库 `encoding/xml` | 解析 `<ANSWER>` |
| HTTP/gzip/binary | 标准库 | 足够实现 Connect frame |

如果目标是极简依赖，可以先不用 `cobra` 和 TOML 库：CLI 用标准库，TOML API key 用正则读取。显式 SQLite 诊断和 glob 仍建议使用成熟库。

## 8. 测试与验证策略

必须有离线测试，不能每次依赖真实 Windsurf 服务。

| 测试类型 | 覆盖内容 |
| --- | --- |
| Golden bytes | Protobuf 字段编码、Connect frame 编解码 |
| Credential fixtures | 三平台自动来源不含 `state.vscdb`、Linux/WSL TOML、显式 SQLite、缺失字段、空 key、临时快照清理 |
| Executor temp repo | `rg`、`readfile`、`tree`、`ls`、`glob` 和路径逃逸 |
| Response parser | `[TOOL_CALLS]`、错误 JSON、`<ANSWER>` XML |
| Search orchestrator | mock client 返回 tool call 和 final answer |
| CLI e2e | `fast-context search --format json` 的退出码和输出 schema |

建议为测试增加内部 endpoint override，例如：

```text
FC_TEST_AUTH_BASE=http://127.0.0.1:...
FC_TEST_API_BASE=http://127.0.0.1:...
```

这两个变量只在测试构建或明确 `--allow-test-endpoints` 时启用，避免生产误连。

## 9. 里程碑

### M1：离线 CLI 骨架和本地 executor

交付：

| 交付物 | 验证 |
| --- | --- |
| `fast-context search` 参数解析 | `fast-context search --help` |
| repo map 生成 | 大目录自动 depth fallback |
| `readfile/tree/ls/glob` | temp repo 单测 |
| `rg` 调用 | 有匹配、无匹配、错误路径单测 |

### M2：凭据读取和诊断

交付：

| 交付物 | 验证 |
| --- | --- |
| `fast-context key extract` | 默认 TOML 与显式 SQLite fixture 单测 |
| `fast-context doctor` | 输出 key 来源、rg 状态和候选路径，且不包含自动数据库路径 |
| 敏感信息脱敏 | 输出只显示前后片段 |

### M3：Windsurf 协议客户端

交付：

| 交付物 | 验证 |
| --- | --- |
| JWT 获取 | mock server + 可选真实账号 smoke test |
| rate limit 检查 | mock server |
| stream 请求 | Connect frame golden test |
| 响应解析 | tool call 和 final answer fixture |

### M4：完整搜索闭环

交付：

| 交付物 | 验证 |
| --- | --- |
| 多轮 search orchestrator | mock client e2e |
| text/json 输出 | snapshot test |
| 错误分类和 hint | timeout、401、413、429 fixture |
| 真实项目 smoke test | 使用小型 repo 查询 2-3 个已知问题 |

### M5：跨平台发布

交付：

| 交付物 | 验证 |
| --- | --- |
| Windows/macOS/Linux 二进制 | GitHub Actions matrix |
| ripgrep 分发 | 每个平台 `doctor` 通过 |
| README 和安装说明 | 本地从 release 包安装验证 |

## 10. 工作量评估

以熟悉 Go、熟悉 HTTP/Protobuf wire format 的工程师估算：

| 阶段 | 乐观 | 稳妥 |
| --- | --- | --- |
| M1 本地 CLI/executor | 1-2 天 | 2-3 天 |
| M2 凭据读取 | 0.5-1 天 | 1-2 天 |
| M3 协议客户端 | 2-3 天 | 4-6 天 |
| M4 搜索闭环 | 1-2 天 | 2-4 天 |
| M5 发布与跨平台验证 | 1-2 天 | 3-5 天 |

可用 MVP：约 1-2 周。

稳定可发布版本：约 2-4 周。

最大不确定性不是 Go 代码量，而是 Windsurf 私有协议是否稳定，以及真实服务返回格式是否与 fixture 长期一致。

## 11. 风险与应对

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| Windsurf 私有协议变更 | 工具突然不可用 | Golden test + 真实 smoke test + 清晰错误分类 |
| 字段号或 metadata 不完整 | 认证或搜索失败 | 初版严格对齐上游字段，避免提前“清理” |
| Linux/WSL Devin CLI TOML 路径变化 | 自动回退失败 | `doctor` 输出候选路径，保留三个显式运行时来源 |
| 显式 SQLite 被锁或格式变化 | `key extract --path` 失败 | 复制快照读取并返回来源错误；不影响运行时自动凭据解析 |
| ripgrep 分发复杂 | 用户安装体验变差 | MVP 允许 PATH，发布版内置 per-platform rg |
| 远端模型输出不规范 | search loop 中断 | 保留补偿轮、JSON 容错、强制 answer prompt |
| 安全边界被绕过 | 本地文件泄露或命令注入 | 路径沙箱、结构化参数、禁止 shell 拼接、敏感信息脱敏 |
| 法务/服务条款不确定 | 发布风险 | README 明确非官方工具，保留 MIT notice，评估是否适合公开分发 |

## 12. 推荐实施决策

建议按以下原则落地：

1. 先做 Go CLI 核心库，不先做 MCP server。
2. 协议层逐字段对齐上游，不在第一版做重构式“优化”。
3. 本地工具行为优先贴近上游，尤其是 `rg` 输出、line range 和 tree size fallback。
4. 所有真实网络调用都包一层接口，测试使用 mock server。
5. 输出必须同时支持人读 text 和机器读 JSON。
6. 对 API key、JWT、数据库路径、HTTP header 中的敏感信息统一脱敏。

MCP 能力已在同一套 `internal/search` 上以下列子命令恢复：

```text
fast-context mcp serve
```

CLI 与 MCP 复用同一套协议客户端、executor、输出格式化与 doctor 检查，没有形成第二套实现。设计、合同与验证结果见 [fast-context-mcp-serve-plan.md](fast-context-mcp-serve-plan.md)。
