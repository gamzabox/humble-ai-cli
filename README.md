# Humble AI CLI

Lightweight terminal client for conversational LLM sessions with OpenAI or Ollama backends. The app preserves chat context, streams responses, and keeps a local history of every conversation.

## Features
- Interactive REPL with streaming responses and “Thinking…” indicators.
- Remembers conversation context per session and persists transcripts to `~/.humble-ai-cli/sessions/`.
- Optionally chunks context messages when `ollamaContextChunkSize` is set for Ollama models, splitting at the configured BPE token limit to avoid oversized prompts while keeping OpenAI runs unchunked.
- Limits how many prior turns are sent back to the model via the `contextRetentionTurns` setting (defaults to the last 3 turns, supports disabling or sending the full history).
- Works with either OpenAI or Ollama providers as defined in `~/.humble-ai-cli/config.json`.
- Ships with a built-in system prompt and appends optional user rules from `~/.humble-ai-cli/user-rules.md`.
- Runs scripted conversations via `humble-ai-cli exec <workflow.md>`, executing multiple prompts from a workflow file in a single session.
- Built-in slash commands:
  - `/help` – show available commands.
  - `/new` – start a fresh session (clears in-memory history).
  - `/set-model` – select the active model from configured entries.
  - `/set-tool-mode` – switch MCP tool calls between manual confirmation and auto execution.
  - `/mcp` – display enabled MCP servers and the tools they expose.
  - `/toggle-mcp` – enable or disable MCP servers defined in `mcp-servers.json`.
  - `/exit` – quit the program; pressing `Ctrl+D` on an empty prompt exits as well, while `Ctrl+C` now only cancels an in-progress response.

## Quick Start
1. Configure at least one model in `~/.humble-ai-cli/config.json` (the CLI stays idle until a model is defined). Set a single entry to `"active": true` and include the required provider fields such as `apiKey` (OpenAI) or `baseUrl` (Ollama). Enable automatic MCP tool execution up front with `"toolCallMode": "auto"` if you do not want to confirm each call.
2. (Optional) Register MCP servers in `~/.humble-ai-cli/mcp-servers.json` under a top-level `mcpServers` object. Launch the CLI and use `/toggle-mcp` to enable or disable entries without editing the file, and `/mcp` to list the active servers and their functions.
3. Run the CLI (`go run ./...` from the repo or the `humble-ai-cli` binary) and start chatting. Use `/set-model` to switch between configured models at runtime.
4. To script a conversation, copy `WORKFLOW.sample.md`, update only the model in the `# CONFIGS` section (and MCP servers if needed), then execute `humble-ai-cli exec </path/to/workflow_file>`. The workflow file bundles config, MCP definitions, and prompts so you can run a repeatable session in one command.

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
  "ollamaContextChunkSize": 0,
  "contextRetentionTurns": 5,
  "ollamaNumCtx": 30000,
  "logLevel": "info",
  "toolCallMode": "manual"
}
```

`ollamaContextChunkSize` controls how many BPE tokens can be included in a single context message before it is split into multiple chunks when using Ollama. Omit the field or set it to 0 (the default) to keep chunking disabled; provide a positive value to enable chunking at that token limit. OpenAI models always send context as-is regardless of the value.

`contextRetentionTurns` decides how many user-assistant turn pairs are sent alongside each new user prompt. Omit the field to keep the default of the last 3 turns, set it to 0 to send only the latest user prompt without prior context, or use a negative value to always send the full history (no trimming). When a dangling user-only entry exists in history it is treated as a full turn for retention purposes.

`ollamaNumCtx` sets the `num_ctx` option sent to the Ollama chat API. Provide a positive number to cap the model context size, or omit/set it to 0 to fall back to the CLI default of 30000 tokens (the value is always sent to Ollama as `num_ctx`).

Optional: add additional guardrails in `~/.humble-ai-cli/user-rules.md`. The CLI creates the file if missing and appends its contents to the built-in system prompt for every request.
Set `active` to `true` for the model you want the CLI to use by default. Only one model should be active at a time.
Set `toolCallMode` to `auto` to automatically run approved MCP tool calls without the confirmation prompt (the default `manual` mode keeps the confirmation step). You can also adjust this within the CLI via `/set-tool-mode auto` or `/set-tool-mode manual`.

## Workflow Execution
- Run `humble-ai-cli exec path/to/workflow.md` to execute a predefined conversation and exit after completion.
- Format (see `WORKFLOW.sample.md`): the `# CONFIGS` section may include `## Basic Config` (JSON matching `config.json`) and `## MCP Servers` (JSON with the `mcpServers` object from `mcp-servers.json`). If either subsection is missing, the CLI falls back to `$HOME/.humble-ai-cli/config.json` or `$HOME/.humble-ai-cli/mcp-servers.json` respectively.
- When a Basic Config block is present, only that JSON is used—missing fields take program defaults rather than values from local config files. It must declare an active model; OpenAI entries require `apiKey` and Ollama entries require `baseUrl`. Missing required fields or malformed workflow files produce an error and abort execution.
- The optional `# USER RULES` section replaces `$HOME/.humble-ai-cli/user-rules.md` during workflow execution. If the section exists with content, it is appended to the system prompt; if the section is present but empty, no user rules are added. If the section is absent, the CLI falls back to the local `user-rules.md` file (creating it if missing).
- Under `# WORKFLOWS`, each `##` heading marks a step; only the body text becomes the user prompt. Steps run in order within a single session so later prompts reuse earlier context.
- Workflow output shows only the final answer for each prompt. Startup guides, waiting messages, and MCP call summaries are suppressed unless `toolCallMode` is `manual`, in which case MCP call details and the Y/N confirmation prompt still appear before execution.

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
      "headers": {
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
- For remote servers, set `headers` to forward HTTP headers (if omitted, existing `env` entries are still forwarded for compatibility). SSE endpoints that never emit the required `endpoint` event are automatically retried using the streamable HTTP protocol, so existing configs remain resilient.
- When the LLM requests a tool call, the CLI prints the server name and description. In `manual` mode it then asks `Call now? (Y/N)`; in `auto` mode it executes immediately after printing the summary. Toggle the behaviour with `/set-tool-mode`.
- On first launch the CLI auto-creates an empty `~/.humble-ai-cli/user-rules.md` (if missing) and lists all enabled MCP servers so the LLM understands which tools are available.
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
