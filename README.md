## Caddy AI Router

A small yet scalable AI gateway for Caddy that gives you one OpenAI-like endpoint across multiple providers (OpenAI, OpenRouter, Anthropic, Google, Cloudflare). Point your apps at a single URL, pick a model, and we'll route, transform, and proxy the request where it needs to go.

## Why this exists

If you use more than one AI provider, you've probably juggled different request/response shapes, endpoint paths, and credentials. This router sits in front of them and:
- exposes a unified chat completions endpoint
- normalizes requests/responses
- routes by falltrough order, explicit provider or per-model defaults
- fetches and aggregates available models
- makes API key management simple (env-based by default)

## Highlights

- OpenAI-compatible chat endpoint: POST /api/chat/completions
- Aggregated models endpoint: GET /api/models
- Provider transforms built-in: OpenAI, Anthropic, Google (Gemini), Cloudflare AI
- Routing options:
  - Explicit provider: model as "provider/modelName" (e.g., "openai/gpt-4o")
  - Provider selection falltrough (first config tried first)
  - Per-model defaults via Caddyfile
  - Best-effort fuzzy match if a model can't be resolved (search across providers you allow) eg. `qwq` -> `qwen/qwq-32b`, `gpt` -> `openai/gpt-4.1`
- Pluggable API key source; default is environment variables like OPENAI_API_KEY, GOOGLE_API_KEY, etc.
- Optional observability via PostHog (POSTHOG_API_KEY)

## Prerequisites

- Go 1.21+
- xcaddy (to build Caddy with this module)

## Build

You can use the workspace task or run xcaddy directly.

- VS Code task: builds Caddy with this module in this repo
- Manual:
  ```bash
  ~/go/bin/xcaddy build --with github.com/neutrome-labs/caddy-ai-router=./
  ```

This produces a Caddy binary in the current directory. Run it with your Caddyfile.

## Environment variables

The default API key provider reads keys from env vars named <PROVIDER>_API_KEY:

- OPENAI_API_KEY
- OPENROUTER_API_KEY
- ANTHROPIC_API_KEY
- GOOGLE_API_KEY
- CF_API_KEY (Cloudflare API Token)

Optional observability:
- POSTHOG_API_KEY (enable PostHog events)
- POSTHOG_BASE_URL (custom endpoint, optional)

Tip: Cloudflare also needs your account ID embedded in the provider's api_base_url.

## How routing works

You control the target in three ways:

1) Explicit provider prefix in the model field
- Format: "provider/model"
- Example: "openai/gpt-4o", "openrouter/anthropic/claude-3-haiku-20240307"

2) Per-model defaults
- In Caddyfile via default_provider_for_model "<model>" "<provider1>" "<provider2>" ... "<providerN>"
- If the request's model matches, it routes there.

3) Faltrough as configured with fuzzy match across providers:
- If not, the router will fetch model lists from allowed providers and find the closest match
- Example: `qwq` -> `cloudflare/@cf/qwen/qwq-32b`, `gpt-4.1` -> `openrouter/openai/gpt-4.1`, `r1` -> `cloudflare/@cf/deepseek-ai/deepseek-r1-distill-qwen-32b`

## Endpoints and shapes

GET /api/models
- Returns an aggregated list: { "data": [{ "id": "...", "name": "..." }, ...] }
- Some providers (e.g., Anthropic) don't expose models; they'll just be absent.

POST /api/chat/completions
- Request is OpenAI-like: { model, messages, stream?, max_tokens?, temperature? }
- Response is normalized to an OpenAI-like shape with choices[].
- Provider-specific transforms are applied automatically:
  - OpenAI/OpenRouter: pass-through (path set to /chat/completions)
  - Anthropic: maps to /v1/messages and back to OpenAI-like response
  - Google (Gemini): maps to /models/{model}:generateContent and back
  - Cloudflare AI: maps to /run/{model}; streaming and non-streaming are converted to an OpenAI-like format

## Quick try with curl

Explicit provider:
```bash
curl -sS http://localhost:8080/api/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o",
    "messages": [{"role": "user", "content": "Explain quantum computing simply."}],
    "stream": false
  }'
```

Using a default mapping:
```bash
curl -sS http://localhost:8080/api/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-pro",
    "messages": [{"role": "user", "content": "Give me a haiku about summer rain."}]
  }'
```

List models:
```bash
curl -sS http://localhost:8080/api/models
```

## Running

After building with xcaddy, run Caddy with your Caddyfile:

```bash
./caddy run --config Caddyfile
```

## Notes and limitations

- Streaming: OpenAI-style streaming works; Cloudflare streaming is adapted. Other providers are best-effort.
- No built-in user auth: if you need per-user keys, plug in your own ExternalAPIKeyProvider (env provider is the default).
- Replace <your_account_id> in the Cloudflare api_base_url.
- This is an early version. Expect breaking changes as the module evolves.

---

Questions or ideas? Open an issue or PR. Happy routing.
