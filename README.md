# Humble AI CLI

Lightweight terminal client for conversational LLM sessions with OpenAI or Ollama backends. The app preserves chat context, streams responses, and keeps a local history of every conversation.

## Features
- Interactive REPL with streaming responses and “Thinking…” indicators.
- Remembers conversation context per session and persists transcripts to `~/.humble-ai-cli/sessions/`.
- Works with either OpenAI or Ollama providers as defined in `~/.humble-ai-cli/config.json`.
- Supports configurable system prompts stored at `~/.humble-ai-cli/system_prompt.txt`.
- Built-in slash commands:
  - `/help` – show available commands.
  - `/new` – start a fresh session (clears in-memory history).
  - `/set-model` – select the active model from configured entries.
  - `/mcp` – display enabled MCP servers and the functions they expose.
  - `/exit` – quit the program (pressing `Ctrl+C` twice also exits; once during streaming cancels the response).

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
  "provider": "openai",
  "activeModel": "gpt-4o",
  "models": [
    {
      "name": "gpt-4o",
      "provider": "openai",
      "apiKey": "sk-..."
    },
    {
      "name": "llama2",
      "provider": "ollama",
      "baseUrl": "http://localhost:11434"
    }
  ]
}
```

Optional: provide a system prompt via `~/.humble-ai-cli/system_prompt.txt`. The contents will be prepended to every request.

### Logging
- Logs are written to `~/.humble-ai-cli/logs/application-hac-YYYY-MM-DD.log`.
- Set `logLevel` (debug, info, warn, error) in `config.json` to control verbosity. Debug level includes detailed LLM and MCP traces.

## MCP Server Configuration
- Create the MCP directory if it does not exist: `mkdir -p ~/.humble-ai-cli/mcp_servers`.
- Add one JSON file per server. Each file must set `enabled` to `true` for the CLI to load it. Minimal example saved as `~/.humble-ai-cli/mcp_servers/calculator.json`:
```json
{
  "name": "calculator",
  "description": "Adds or subtracts numbers for quick estimates.",
  "enabled": true,
  "command": "/usr/local/bin/mcp-calculator",
  "args": ["--port=0"]
}
```
- When the LLM requests a tool call, the CLI prints the server name and description, then asks `Call now? (Y/N)`. Enter `Y` to execute or `N` to cancel.
- On first launch the CLI auto-creates `~/.humble-ai-cli/system_prompt.txt` if missing and lists all enabled MCP servers so the LLM understands which tools are available.

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
Produce a standalone binary:

```bash
go build -o humble-ai-cli ./...
```

The resulting binary can be placed anywhere on your `PATH`. When run, it will continue to use the configuration files under `~/.humble-ai-cli`.
