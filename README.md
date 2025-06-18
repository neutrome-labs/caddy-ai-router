# Caddy AI Gateway

This project implements an AI gateway using Caddy with a custom Go module. It routes requests to `/chat/completions` to various AI providers based on model name prefixes.

## Features

-   Single primary endpoint: `/chat/completions`.
-   Secondary endpoint: `/models` (aggregates models from configured providers).
-   Provider selection via model name prefix (e.g., `openrouter#some-model/o3`, `openai#gpt-4o`).
-   Default provider for specific models if no prefix is given.
-   Super-default provider (e.g., OpenRouter) if no other rule matches.

## Prerequisites

-   [Go](https://golang.org/dl/) (version 1.21 or later recommended)
-   [xcaddy](https://github.com/caddyserver/xcaddy) (for building Caddy with custom modules)

## Setup

1.  **Install xcaddy:**
    Follow the instructions on the [xcaddy GitHub page](https://github.com/caddyserver/xcaddy#installation).
    A common way is:
    ```bash
    go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
    ```
    Ensure `$(go env GOPATH)/bin` is in your `PATH`.

2.  **Build Caddy with the `ai_router` module:**
    
    ```bash
    # Build with only the ai_router module
    ~/go/bin/xcaddy build --output ./dist/caddy-ai-gateway --with github.com/neutrome-labs/caddy-ai-router
    ```
    This command tells `xcaddy` to compile Caddy and include the `ai_router` module.

    This will create an executable file named `caddy-ai-gateway` in a `dist` directory (you might need to create `dist` first or adjust the output path).

## Configuration

1.  **Caddyfile:**
    The main configuration is in `Caddyfile`with `ai_core_router` directive.
    -   In `ai_core_router`:
        -   Define providers (e.g., `openai`, `openrouter`) with their `api_base_url`.
        -   Set `default_provider_for_model` mappings.
        -   Set the `super_default_provider`.
        -   Set the `upstream_path` (e.g., `/chat/completions`).

    Example middleware chain in `Caddyfile`:
    ```caddyfile
    # ... inside http://localhost:8080 { ... handle_path /api/* { route { ...
            ai_core_router {
                provider openrouter {
                    api_base_url "https://openrouter.ai/api/v1"
                }
                provider openai {
                    api_base_url "https://api.openai.com/v1"
                }
                # Add more providers as needed

                default_provider_for_model "gpt-4o" "openai"
                super_default_provider "openrouter"
                upstream_path "/chat/completions"
            }
    # ... } } }
    ```

2.  **ENV:**
    Set required ENV variables like `[provider]_API_KEY=<your_api_key>`, eg.:
    ```
    OPENAI_API_KEY=sk_...
    ```

## Running the Gateway

1.  **Navigate to the `dist` directory.**

2.  **Run the custom Caddy build:**
    ```bash
    ./caddy-ai-gateway run --config Caddyfile
    ```
    (On Windows, it might be `caddy-ai-gateway.exe run --config Caddyfile`)

    Caddy will start, load the `Caddyfile`, and your `ai_router` module will handle requests to `/api/chat/completions`. Logs will be printed to stdout.

## Making Requests

Send POST requests to `http://localhost:8080/api/chat/completions` (or your configured address) with a JSON body. The `model` field in the JSON body determines routing:

**Example Request Body:**

-   **To use OpenRouter for `anthropic/claude-3-haiku` (explicit provider):**
    ```json
    {
        "model": "openrouter#anthropic/claude-3-haiku-20240307",
        "messages": [
            {"role": "user", "content": "Hello!"}
        ]
    }
    ```
    The `ai_router` will strip `openrouter#` and send `anthropic/claude-3-haiku-20240307` as the model to OpenRouter.

-   **To use the default provider for `gpt-4o` (e.g., OpenAI, if configured):**
    ```json
    {
        "model": "gpt-4o",
        "messages": [
            {"role": "user", "content": "Tell me a joke."}
        ]
    }
    ```

-   **To use the super-default provider (e.g., OpenRouter) for an unrecognized model:**
    ```json
    {
        "model": "some-other-model/o3",
        "messages": [
            {"role": "user", "content": "What is Caddy?"}
        ]
    }
    ```

**Example using `curl`:**

```bash
curl -X POST http://localhost:8080/api/chat/completions \
-H "Content-Type: application/json" \
-d '{
    "model": "openai#gpt-4o",
    "messages": [{"role": "user", "content": "Explain quantum computing simply."}],
    "stream": false # Example, add other parameters as needed by the provider
}'
```
