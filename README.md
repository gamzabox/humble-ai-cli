# Humble AI CLI

Lightweight terminal client for conversational LLM sessions with OpenAI or Ollama backends. The app preserves chat context, streams responses, and keeps a local history of every conversation.

## Features
- Interactive REPL with streaming responses and “Thinking…” indicators.
- Remembers conversation context per session and persists transcripts to `chat_history/`.
- Works with either OpenAI or Ollama providers as defined in `~/.config/humble-ai-cli/config.json`.
- Supports configurable system prompts stored at `~/.config/humble-ai-cli/system_prompt.txt`.
- Built-in slash commands:
  - `/help` – show available commands.
  - `/set-model` – select the active model from configured entries.
  - `/exit` – quit the program (pressing `Ctrl+C` twice also exits; once during streaming cancels the response).

## Prerequisites
- Go 1.25.2 (or a Go toolchain that supports a compatible `go` version).  
  Verify with: `go version`
- Network access to your chosen provider (OpenAI API or local/remote Ollama).

## Configuration
Create the config directory if it does not exist:

```bash
mkdir -p ~/.config/humble-ai-cli
```

Add provider and model details to `~/.config/humble-ai-cli/config.json`, for example:

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

Optional: provide a system prompt via `~/.config/humble-ai-cli/system_prompt.txt`. The contents will be prepended to every request.

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

The resulting binary can be placed anywhere on your `PATH`. When run, it will continue to use the configuration files under `~/.config/humble-ai-cli`.
