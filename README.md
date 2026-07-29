# Atom2Api

Atom2Api 将 AtomGit Coding Plan 账号统一转换为可供外部应用调用的 OpenAI 兼容接口，并提供账号、额度、密钥、模型路由和 Token 用量仪表盘。

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

```bash
docker compose up -d --build
```

数据保存在 `atom2api-data` volume。首次启动后访问控制台修改默认密码。

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
| `request_timeout_seconds` | `120` | 上游请求超时范围 5-600 秒 |

主状态写入 `data_path`，请求明细以 NDJSON 追加到 `<data_path>.usage.ndjson`，内存和日志最多保留最近 50,000 条请求用于仪表盘聚合。

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

测试覆盖凭据加密、API Key 摘要、Coding Plan 领取级联与同步、签名 fixture、非流式/流式代理用量、服务端管理会话和配置热加载。
