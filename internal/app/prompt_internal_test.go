package app

import (
	"strings"
	"testing"

	"github.com/gamzabox/humble-ai-cli/internal/llm"
)

func TestToolContextPromptIncludesFunctionsWhenListingEnabled(t *testing.T) {
	t.Parallel()

	defs := []llm.ToolDefinition{
		routeIntentToolDefinition(),
		{
			Name:        "weather__get_weather",
			Description: "Get the weather",
			Server:      "weather",
			Method:      "get_weather",
		},
	}

	prompt := toolContextPrompt(defs, true)
	if !strings.Contains(prompt, "# Connected Tools") {
		t.Fatalf("expected connected tools heading, got %q", prompt)
	}
	if strings.Contains(prompt, routeIntentToolName) {
		t.Fatalf("route-intent entry should be omitted, got %q", prompt)
	}
	if !strings.Contains(prompt, "## MCP Server: weather") {
		t.Fatalf("expected weather server entry, got %q", prompt)
	}
	if !strings.Contains(prompt, "- function name: **weather__get_weather**") {
		t.Fatalf("expected weather tool name, got %q", prompt)
	}
	if !strings.Contains(prompt, "- description: Get the weather") {
		t.Fatalf("expected weather tool description, got %q", prompt)
	}
	if !strings.Contains(prompt, "# Function Call Schema and Example") {
		t.Fatalf("expected function call schema block, got %q", prompt)
	}
	if !strings.Contains(prompt, `"server": "server_name"`) {
		t.Fatalf("expected schema block to include placeholder server entry, got %q", prompt)
	}
	if !strings.Contains(prompt, `"server": "good-server"`) {
		t.Fatalf("expected schema example to include good-server entry, got %q", prompt)
	}
	if !strings.Contains(prompt, `"name": "server_name__tool name"`) {
		t.Fatalf("expected schema block to include placeholder namespaced tool entry, got %q", prompt)
	}
	if !strings.Contains(prompt, `"name": "good-server__good-tool"`) {
		t.Fatalf("expected schema example to include namespaced tool entry, got %q", prompt)
	}
}

func TestToolContextPromptShowsFallbackNotice(t *testing.T) {
	t.Parallel()

	prompt := toolContextPrompt([]llm.ToolDefinition{routeIntentToolDefinition()}, true)
	if !strings.Contains(prompt, "NO FUNCTION CONNECTED") {
		t.Fatalf("expected fallback notice, got %q", prompt)
	}
	if strings.Contains(prompt, "**") {
		t.Fatalf("expected fallback notice without emphasis, got %q", prompt)
	}
	if !strings.Contains(prompt, "# Function Call Schema and Example") {
		t.Fatalf("expected function call schema block, got %q", prompt)
	}
	if !strings.Contains(prompt, `"arg1 name": "argument1 value"`) {
		t.Fatalf("expected placeholder arguments in schema block, got %q", prompt)
	}
	if !strings.Contains(prompt, `"goodArg": "nice"`) {
		t.Fatalf("expected example arguments in schema block, got %q", prompt)
	}
}

func TestToolContextPromptSkipsListingWhenDisabled(t *testing.T) {
	t.Parallel()

	defs := []llm.ToolDefinition{
		routeIntentToolDefinition(),
		{
			Name:        "context7__resolve-library-id",
			Description: "Resolve IDs",
			Server:      "context7",
			Method:      "resolve-library-id",
		},
	}

	prompt := toolContextPrompt(defs, false)
	if strings.TrimSpace(prompt) != "" {
		t.Fatalf("expected empty prompt when listing disabled and tools exist, got %q", prompt)
	}
	emptyPrompt := toolContextPrompt([]llm.ToolDefinition{routeIntentToolDefinition()}, false)
	if emptyPrompt != "NO FUNCTION CONNECTED" {
		t.Fatalf("expected fallback notice without schema, got %q", emptyPrompt)
	}
}
