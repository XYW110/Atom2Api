# Atom2Api

[English](./README.md) | 简体中文

Atom2Api 将 AtomGit Coding Plan 账号统一转换为可供外部应用调用的 OpenAI 兼容接口，并提供账号、额度、密钥、模型路由和 Token 用量仪表盘。

## 安全警告

> [!WARNING]
> 本项目仅适合在可信网络环境中使用。部署或使用前，请充分评估以下安全风险：

- **默认使用明文 HTTP**：Atom2Api 本身不提供 TLS。客户端与服务之间的提示词、代码、响应和 API Key 均可能以明文传输；默认监听地址 `:8080` 还可能使服务暴露给同一网络中的其他设备。非纯本机使用时，必须配置 HTTPS 反向代理、防火墙和严格的访问范围。
- **默认管理密码不安全**：首次启动的管理密码为 `atom2api`。OpenAI 兼容接口虽要求使用已创建的 API Key，但任何能访问服务且知道默认密码的人都可能进入管理控制台。首次登录后必须立即修改密码，并妥善保管已签发的 API Key。
- **本地数据包含敏感信息**：OAuth token 使用 AES-256-GCM 加密保存，API Key 仅保存 SHA-256 摘要；但 `config.json` 仍以明文保存管理密码、`encryption_key` 及可选的 `signer_token`。同时取得 `config.json` 和 `data/` 的进程或用户可以解密 OAuth 凭据，请严格限制这些文件的读取权限并一同安全备份。
- **审计日志会保存请求内容**：`<data_path>.usage.ndjson` 以明文记录请求与响应正文，其中可能包含提示词、源代码、个人信息或其它秘密。请限制日志访问、控制备份范围，并按数据保留要求及时清理。
- **依赖非官方兼容实现**：本项目通过社区独立记录的签名兼容实现访问 AtomGit 上游，不属于 AtomGit/AtomCode 官方客户端或受支持集成。上游协议变更可能随时导致服务不可用，使用方式也可能带来账号限制或封禁风险。

**免责声明**：本项目仅供学习和研究，不隶属于 AtomGit/AtomCode，也未获其官方认可。使用者需自行评估风险、遵守 AtomGit/AtomCode 的服务条款及适用法律，并对账号封禁、数据泄露、费用或其它损失承担全部责任。

**AI 生成说明**：本项目代码 100% 由 AI（AtomCode / OpenCode）编写，人类仅负责提出需求和审核。

## 已实现

- AtomGit broker OAuth：启动授权、轮询状态、交换 token、提前 5 分钟自动刷新
- Coding Plan：按 `Max -> Pro -> Lite` 领取，读取订阅类型、滚动额度、到期时间、模型目录和 60 天用量
- OpenAI 兼容端点：`/v1/models`、`/v1/chat/completions`、`/v1/responses`、`/v1/completions`、`/v1/embeddings`
- 流式代理：SSE 即时转发，自动请求 `include_usage`，记录输入、输出、缓存和推理 tokens
- 多账号路由：跳过停用或额度耗尽账号，在可用账号之间轮询
- API Key：仅保存 SHA-256 摘要，支持模型白名单、撤销、恢复和过期时间
- 管理安全：HttpOnly + SameSite 会话、登录限速、OAuth token AES-256-GCM 加密落盘
- 仪表盘：请求趋势、请求数、输入/输出 tokens、成功率、延迟、模型分布和最近请求
- 单二进制部署：React 控制台嵌入 Go 服务，另附 Docker/Compose

### Responses API 兼容回退

`/v1/responses` 默认先请求模型的原生上游。若上游返回 404、405 或 501，Atom2Api 会使用同一模型改调 `/v1/chat/completions`，并将请求、普通响应和流式 SSE 转换回 Responses API；转换成功后会在当前进程内记住该模型的能力。响应头 `X-Atom2api-Responses-Compat: chat-completions` 表示本次请求使用了兼容回退。

兼容层支持文本、图片 URL、函数工具、JSON Schema、用量和流式事件。兼容层会接受 `prompt_cache_key`，但不会把这一缓存路由提示转发给 Chat Completions。Chat Completions 无法等价提供的服务端状态（例如 `store=true`、`previous_response_id`）和 OpenAI 内置工具会返回 `400 unsupported_parameter`，不会被静默忽略。

## 本地运行

要求 Go 1.22+、Node.js 20+。

```bash
cd frontend
npm ci
npm run build
cd ..
go test ./...
go run .
```

打开 `http://localhost:8080`。首次运行会生成 `config.json` 和随机 `encryption_key`。默认管理密码是 `atom2api`，登录后应立即在“系统设置”中修改。

也可以先使用示例配置：

```bash
cp config.example.json config.json
go run .
```

Windows PowerShell 对应命令：

```powershell
Copy-Item config.example.json config.json
go run .
```

## Docker

Compose 配置使用 Docker Hub 发布的 [`cnluminous/atom2api:1.0.2`](https://hub.docker.com/r/cnluminous/atom2api) 镜像，无需在本地构建镜像。

```bash
docker compose pull
docker compose up -d
```

容器启动后访问 `http://localhost:8080`。数据保存在 `atom2api-data` volume。首次启动后访问控制台修改默认密码。

## 版本发布

推送 `v1.2.0` 形式的语义化版本标签后，GitHub Actions 会自动执行测试、构建前端并发布 GitHub Release。Release 包含 Linux、Windows、macOS 的 amd64/arm64 压缩包以及 `SHA256SUMS` 校验文件。

在已与上游同步且工作树干净的分支中，使用 PowerShell 7 运行发布脚本：

```powershell
pwsh ./scripts/release.ps1 1.2.0
```

脚本会校验版本、拉取远端标签、验证前后端、创建带说明的 `v1.2.0` 标签，并且只推送该标签。添加 `-DryRun` 可仅执行本地检查，不创建或推送标签。`1.2.0-rc.1` 等候选版本会发布为 GitHub 预发布版本。

如需仅在本地生成同样的压缩包而不发布版本，可运行：

```powershell
pwsh ./scripts/build-artifacts.ps1 1.2.0
```

运行 `atom2api --version` 可查看发布二进制中注入的版本号；本地开发构建显示为 `dev`。

## 使用流程

1. 登录控制台，进入“账号管理”。
2. 点击“连接 AtomGit”，在新页面完成 OAuth 授权。
3. Atom2Api 自动领取或同步 Coding Plan、额度和可用模型。
4. 进入“密钥管理”创建外部 API Key；密钥明文只显示一次。
5. 将客户端的 Base URL 设为 `http://localhost:8080/v1`，API Key 使用刚创建的 `sk-atom2-*`。

Python SDK 示例：

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="sk-atom2-your-key",
)

response = client.chat.completions.create(
    model="deepseek-v4-flash",
    messages=[{"role": "user", "content": "hello"}],
)
print(response.choices[0].message.content)
```

流式请求：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-atom2-your-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","stream":true,"messages":[{"role":"user","content":"hello"}]}'
```

## 配置

| 字段 | 默认值 | 说明 |
|---|---|---|
| `listen_address` | `:8080` | HTTP 监听地址；`PORT` 环境变量优先 |
| `data_path` | `data/atom2api.json` | 账号、密钥摘要和计数状态 |
| `admin_password` | `atom2api` | 控制台管理密码 |
| `encryption_key` | 首次启动随机生成 | OAuth token 加密密钥，不应更换或泄露 |
| `platform_base_url` | `https://acs.atomgit.com` | OAuth broker |
| `codingplan_api_url` | `https://api.gitcode.com/api/v5` | Coding Plan REST API |
| `gateway_url` | `https://llm-api.atomgit.com/v1` | 默认 LLM 网关 |
| `signer_url` | 空 | 可选外部签名服务；为空时使用内置 signer |
| `audit_debug_enabled` | `false` | 开启后记录完整请求/响应正文及脱敏 Header |
| `request_timeout_seconds` | `120` | 上游请求超时范围 5-600 秒 |

主状态写入 `data_path`，请求明细以 NDJSON 追加到 `<data_path>.usage.ndjson`，内存和日志最多保留最近 50,000 条请求用于仪表盘聚合。默认只记录请求元数据与用量；开启审计调试模式后才保存请求/响应正文和 Header。无论调试模式是否开启，上游返回非 200 状态时都会保存其响应正文和 Header。认证、Cookie、Token、API Key 及签名 Header 的值会被脱敏。

## 签名兼容性

当前 AtomGit LLM 网关要求 `X-AtomCode-*` 请求签名。AtomCode 开源仓库只公开 signer 接口，官方构建中的实现不在仓库内。Atom2Api 内置了社区独立记录的 `atomcode-signing-v1` 实现，并用独立 fixture 验证 HKDF/HMAC 输出；来源见 [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)。

如果上游轮换协议或密钥，可在设置中配置 `signer_url` 覆盖内置实现。签名服务接收 JSON：

```json
{
  "method": "POST",
  "path": "/v1/chat/completions",
  "body": "base64-encoded-request-body",
  "oauth_token": "...",
  "user_id": "...",
  "timestamp_unix": 0,
  "nonce": "base64url-nonce",
  "client_version": "5.0.2"
}
```

返回 `{"headers":{"X-AtomCode-Sig":"..."}}`。外部 signer 会接触 OAuth token，只应配置为受信任的 HTTPS 服务或本机回环地址。

## 安全与运维

- 不要提交 `config.json`、`data/` 或 API Key；这些路径已加入 `.gitignore`。
- 公网部署必须置于 HTTPS 反向代理之后，并修改默认管理密码。
- 备份时必须同时保存 `config.json` 和 `data/`；丢失 `encryption_key` 后现有 OAuth token 无法解密。
- 本项目非 AtomGit/AtomCode 官方项目。使用前请确认你的调用方式符合账号套餐和服务条款。

## 验证

```bash
go test -count=1 -cover ./...
go vet ./...
cd frontend && npm run build
```

使用当前 `config.json` 中的账号对 `GLM-5.2` 执行 Responses 非流式、流式和函数工具实测：

```bash
ATOM2API_LIVE_GLM=1 go test -run TestManualGLMResponsesFallback -count=1 -v
```

测试覆盖凭据加密、API Key 摘要、Coding Plan 领取级联与同步、签名 fixture、非流式/流式代理用量、服务端管理会话和配置热加载。

## 致谢

衷心感谢 [Linux.do](https://linux.do/) 社区的所有成员。是你们的真诚、友善、团结与专业，让这个社区始终充满活力。
