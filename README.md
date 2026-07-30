# Atom2Api

English | [简体中文](./README.zh-CN.md)

Atom2Api exposes AtomGit Coding Plan accounts through an OpenAI-compatible API for external applications. It also provides dashboards for accounts, quotas, API keys, model routing, and token usage.

## Security Warning

> [!WARNING]
> This project is intended for use only in trusted network environments. Before deploying or using it, carefully assess the following security risks:

- **Plain HTTP by default**: Atom2Api does not provide TLS. Prompts, source code, responses, and API keys may be transmitted in plain text between clients and the service. The default listen address, `:8080`, may also expose the service to other devices on the same network. For anything beyond local-only use, you must configure an HTTPS reverse proxy, a firewall, and strict access controls.
- **Insecure default administrator password**: The initial administrator password is `atom2api`. Although the OpenAI-compatible API requires a generated API key, anyone who can reach the service and knows the default password may access the management console. Change the password immediately after the first login and protect all issued API keys.
- **Sensitive local data**: OAuth tokens are encrypted at rest with AES-256-GCM, and only SHA-256 digests of API keys are stored. However, `config.json` stores the administrator password, `encryption_key`, and optional `signer_token` in plain text. Any process or user with access to both `config.json` and `data/` can decrypt the OAuth credentials. Restrict access to these files and back them up together securely.
- **Audit logs contain request content**: `<data_path>.usage.ndjson` records request and response bodies in plain text. These records may contain prompts, source code, personal data, or other secrets. Restrict log access, limit backup scope, and clean up records according to your data-retention requirements.
- **Unofficial compatibility implementation**: This project accesses the AtomGit upstream through an independently documented, community-maintained signing implementation. It is not an official AtomGit/AtomCode client or supported integration. Upstream protocol changes may break the service at any time, and usage may result in account restrictions or bans.

**Disclaimer**: This project is for learning and research only. It is not affiliated with or endorsed by AtomGit/AtomCode. You are responsible for evaluating the risks, complying with the AtomGit/AtomCode terms of service and applicable law, and accepting responsibility for account bans, data exposure, charges, or other losses.

**AI-generated code notice**: 100% of this project's code was written by AI (AtomCode / OpenCode). Humans only provided requirements and reviewed the result.

## Features

- AtomGit broker OAuth: starts authorization, polls status, exchanges tokens, and automatically refreshes them five minutes before expiry
- Coding Plan: claims plans in `Max -> Pro -> Lite` order and reads subscription type, rolling quota, expiry time, model catalog, and 60-day usage
- OpenAI-compatible endpoints: `/v1/models`, `/v1/chat/completions`, `/v1/responses`, `/v1/completions`, and `/v1/embeddings`
- Streaming proxy: forwards SSE in real time, automatically requests `include_usage`, and records input, output, cached, and reasoning tokens
- Multi-account routing: skips disabled or quota-exhausted accounts and distributes requests across available accounts in round-robin order
- API keys: stores SHA-256 digests only and supports model allowlists, revocation, restoration, and expiration
- Management security: HttpOnly and SameSite sessions, login rate limiting, and AES-256-GCM encryption for stored OAuth tokens
- Dashboard: request trends, request counts, input/output tokens, success rate, latency, model distribution, and recent requests
- Single-binary deployment: embeds the React console in the Go service and also provides Docker and Compose configurations

## Local Development

Requires Go 1.22+ and Node.js 20+.

```bash
cd frontend
npm ci
npm run build
cd ..
go test ./...
go run .
```

Open `http://localhost:8080`. On first launch, Atom2Api creates `config.json` with a random `encryption_key`. The default administrator password is `atom2api`; change it immediately under **System Settings** after logging in.

You can also start with the example configuration:

```bash
cp config.example.json config.json
go run .
```

The equivalent Windows PowerShell commands are:

```powershell
Copy-Item config.example.json config.json
go run .
```

## Docker

```bash
docker compose up -d --build
```

Data is stored in the `atom2api-data` volume. After the first launch, open the console and change the default password.

## Usage

1. Sign in to the console and open **Accounts**.
2. Select **Connect AtomGit** and complete OAuth authorization on the page that opens.
3. Atom2Api automatically claims or synchronizes the Coding Plan, quota, and available models.
4. Open **API Keys** and create an API key for external clients. The plaintext key is displayed only once.
5. Set the client's base URL to `http://localhost:8080/v1` and use the newly created `sk-atom2-*` API key.

Python SDK example:

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

Streaming request:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-atom2-your-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","stream":true,"messages":[{"role":"user","content":"hello"}]}'
```

## Configuration

| Field | Default | Description |
|---|---|---|
| `listen_address` | `:8080` | HTTP listen address; the `PORT` environment variable takes precedence |
| `data_path` | `data/atom2api.json` | Account state, API key digests, and counters |
| `admin_password` | `atom2api` | Management console password |
| `encryption_key` | Randomly generated on first launch | OAuth token encryption key; do not change or disclose it |
| `platform_base_url` | `https://acs.atomgit.com` | OAuth broker |
| `codingplan_api_url` | `https://api.gitcode.com/api/v5` | Coding Plan REST API |
| `gateway_url` | `https://llm-api.atomgit.com/v1` | Default LLM gateway |
| `signer_url` | Empty | Optional external signing service; the built-in signer is used when empty |
| `audit_debug_enabled` | `false` | Records full request/response bodies and sanitized headers when enabled |
| `request_timeout_seconds` | `120` | Upstream request timeout, from 5 to 600 seconds |

Primary state is written to `data_path`. Request records are appended to `<data_path>.usage.ndjson`, and both memory and the log retain at most the latest 50,000 requests for dashboard aggregation. By default only request metadata and usage are retained. Full bodies and headers are stored only when audit debug mode is enabled. Upstream responses with a non-200 status are retained regardless of that setting. Authentication, cookie, token, API key, and signature header values are redacted.

## Signing Compatibility

The AtomGit LLM gateway currently requires `X-AtomCode-*` request signatures. The open-source AtomCode repository exposes only the signer interface; the implementation included in official builds is not public. Atom2Api includes an independently documented, community-maintained implementation of `atomcode-signing-v1`, with separate fixtures that verify the HKDF/HMAC output. See [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md) for attribution.

If the upstream protocol or key changes, configure `signer_url` in the settings to override the built-in implementation. The signing service receives JSON in this format:

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

It must return `{"headers":{"X-AtomCode-Sig":"..."}}`. An external signer receives the OAuth token, so configure only a trusted HTTPS service or a local loopback address.

## Security and Operations

- Do not commit `config.json`, `data/`, or API keys. These paths are included in `.gitignore`.
- Any public deployment must sit behind an HTTPS reverse proxy and use a password other than the default administrator password.
- Back up `config.json` and `data/` together. Existing OAuth tokens cannot be decrypted if the `encryption_key` is lost.
- This is not an official AtomGit/AtomCode project. Before using it, confirm that your request patterns comply with your account plan and the applicable terms of service.

## Verification

```bash
go test -count=1 -cover ./...
go vet ./...
cd frontend && npm run build
```

The test suite covers credential encryption, API key digests, Coding Plan claim fallback and synchronization, signing fixtures, non-streaming and streaming proxy usage, server-side management sessions, and live configuration reloads.

## Acknowledgements

Our sincere thanks to every member of the [Linux.do](https://linux.do/) community. Your sincerity, kindness, solidarity, and professionalism are what make this community so vibrant.
