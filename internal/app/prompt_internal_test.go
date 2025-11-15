package app

import (
	"strings"
	"testing"

	"github.com/gamzabox/humble-ai-cli/internal/llm"
)

func TestToolContextPromptIncludesFunctions(t *testing.T) {
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

	prompt := toolContextPrompt(defs)
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
}

func TestToolContextPromptShowsFallbackNotice(t *testing.T) {
	t.Parallel()

	prompt := toolContextPrompt([]llm.ToolDefinition{routeIntentToolDefinition()})
	if !strings.Contains(prompt, "**NO FUNCTION CONNECTED**") {
		t.Fatalf("expected fallback notice, got %q", prompt)
	}
	if !strings.Contains(prompt, "# Function Call Schema and Example") {
		t.Fatalf("expected function call schema block, got %q", prompt)
	}
}
