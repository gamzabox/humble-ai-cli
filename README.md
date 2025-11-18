# Humble AI CLI

Lightweight terminal client for conversational LLM sessions with OpenAI or Ollama backends. The app preserves chat context, streams responses, and keeps a local history of every conversation.

## Features
- Interactive REPL with streaming responses and “Thinking…” indicators.
- Remembers conversation context per session and persists transcripts to `~/.humble-ai-cli/sessions/`.
- Automatically chunks context messages every 1,500 BPE tokens by default (configurable via `contextChunkSize`) to avoid oversized prompts.
- Works with either OpenAI or Ollama providers as defined in `~/.humble-ai-cli/config.json`.
- Supports configurable system prompts stored at `~/.humble-ai-cli/system_prompt.txt`.
- Built-in slash commands:
  - `/help` – show available commands.
  - `/new` – start a fresh session (clears in-memory history).
  - `/set-model` – select the active model from configured entries.
  - `/set-tool-mode` – switch MCP tool calls between manual confirmation and auto execution.
  - `/mcp` – display enabled MCP servers and the tools they expose.
  - `/toggle-mcp` – enable or disable MCP servers defined in `mcp-servers.json`.
  - `/exit` – quit the program; pressing `Ctrl+D` on an empty prompt exits as well, while `Ctrl+C` now only cancels an in-progress response.

## Prerequisites
- Go 1.25.2 (or a Go toolchain that supports a compatible `go` version).  
  Verify with: `go version`
- Network access to your chosen provider (OpenAI API or local/remote Ollama).

## Configuration
Create the configuration directory if it does not exist:

```bash
mkdir -p ~/.humble-ai-cli/sessions
```

Add provider and model details to `~/.humble-ai-cli/config.json`, for example:

```json
{
  "models": [
    {
      "name": "gpt-4o",
      "provider": "openai",
      "apiKey": "sk-...",
      "active": true
    },
    {
      "name": "llama2",
      "provider": "ollama",
      "baseUrl": "http://localhost:11434"
    }
  ],
  "contextChunkSize": 2000,
  "logLevel": "debug",
  "toolCallMode": "manual"
}
```

`contextChunkSize` controls how many BPE tokens can be included in a single context message before it is split into multiple chunks. The default is 1,500 tokens; increase or decrease the value based on the limits of your target model.

Optional: provide a system prompt via `~/.humble-ai-cli/system_prompt.txt`. The contents will be prepended to every request.
Set `active` to `true` for the model you want the CLI to use by default. Only one model should be active at a time.
Set `toolCallMode` to `auto` to automatically run approved MCP tool calls without the confirmation prompt (the default `manual` mode keeps the confirmation step). You can also adjust this within the CLI via `/set-tool-mode auto` or `/set-tool-mode manual`.

### Logging
- Logs are written to `~/.humble-ai-cli/logs/application-hac-YYYY-MM-DD.log`.
- Set `logLevel` (debug, info, warn, error) in `config.json` to control verbosity. Debug level includes detailed LLM and MCP traces.

## MCP Server Configuration
- Ensure the config directory exists: `mkdir -p ~/.humble-ai-cli`.
- Define every MCP server inside a single file `~/.humble-ai-cli/mcp-servers.json`. The root object must contain `mcpServers`, whose properties are server names. Example:
```json
{
  "mcpServers": {
    "calculator": {
      "description": "Adds or subtracts numbers for quick estimates.",
      "enabled": true,
      "command": "/usr/local/bin/mcp-calculator",
      "args": ["--port=0"],
      "env": {
        "API_TOKEN": "secret"
      }
    },
    "remote-sse": {
      "description": "Hosted SSE tool endpoint.",
      "url": "https://your-server.example.com/mcp/sse",
      "type": "sse",
      "env": {
        "Authorization": "Bearer token"
      }
    },
    "remote-http": {
      "description": "Streamable HTTP endpoint.",
      "url": "https://your-server.example.com/mcp",
      "type": "streamable-http"
    }
  }
}
```
- `command` servers spawn a local process (passing `args` and `env`).
- `type` controls how the CLI connects:
  - Allowed values: `stdio`, `sse`, `streamable-http`.
  - If only `command` is provided you can omit `type` (defaults to `stdio`), but if you set it explicitly it must be `stdio`.
  - If a `url` is provided you must set `type`. Use `sse` or `streamable-http`. When both `command` and `url` exist, `type` decides which transport is used (`stdio` → command, `sse`/`streamable-http` → URL).
- For remote servers, `env` entries are sent as HTTP headers. SSE endpoints that never emit the required `endpoint` event are automatically retried using the streamable HTTP protocol, so existing configs remain resilient.
- When the LLM requests a tool call, the CLI prints the server name and description. In `manual` mode it then asks `Call now? (Y/N)`; in `auto` mode it executes immediately after printing the summary. Toggle the behaviour with `/set-tool-mode`.
- On first launch the CLI auto-creates `~/.humble-ai-cli/system_prompt.txt` if missing and lists all enabled MCP servers so the LLM understands which tools are available.
- Use `/toggle-mcp` inside the CLI to quickly enable or disable specific MCP servers without manually editing the JSON file.

### Prompting Example
```
Please double-check the shipping fee by calling the MCP `shipping-calculator` tool with
{ "weightKg": 1.8, "distanceKm": 120 } and summarize the total cost.
```
The assistant will pause at the confirmation step, run the MCP tool after approval, and then incorporate the tool result into its answer.

## Running the CLI
From the project root:

```bash
go run ./...
```

Follow the on-screen prompt to enter questions or slash commands. If no active model is set, the app guides you through `/set-model`.

## Testing
Execute all tests (requires Go toolchain):

```bash
go test ./...
```

## Building
Use the provided build script to generate platform-specific binaries with embedded version metadata:

```bash
./build.sh
```

The script derives the version from the latest git tag (fallback `dev`), injects both the version and the build timestamp via `-ldflags`, and outputs the following artifacts under `dist/`:
- `humble-ai-cli` (linux/amd64)
- `humble-ai-cli_amd64.exe` (windows/amd64)
- `humble-ai-cli_arm64.exe` (windows/arm64)

If you only need a quick local build for your current platform, you can still run `go build -o humble-ai-cli ./...`, but version/date values will remain at their default (`dev`, `unknown`).
