package workflow_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gamzabox/humble-ai-cli/internal/workflow"
)

func TestParseFileExtractsConfigsAndSteps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# CONFIGS\n" +
		"## Basic Config\n" +
		"```json\n" +
		"{\n" +
		"  \"models\": [\n" +
		"    {\n" +
		"      \"name\": \"wf-model\",\n" +
		"      \"provider\": \"openai\",\n" +
		"      \"apiKey\": \"sk-123\",\n" +
		"      \"active\": true\n" +
		"    }\n" +
		"  ],\n" +
		"  \"contextRetentionTurns\": 2\n" +
		"}\n" +
		"```\n\n" +
		"## MCP Servers\n" +
		"```json\n" +
		"{\n" +
		"  \"mcpServers\": {\n" +
		"    \"notes\": {\n" +
		"      \"enabled\": true,\n" +
		"      \"command\": \"notes-cmd\",\n" +
		"      \"args\": [\"--headless\"]\n" +
		"    }\n" +
		"  }\n" +
		"}\n" +
		"```\n\n" +
		"# WORKFLOWS\n" +
		"## research topic\n" +
		"Find three facts.\n\n" +
		"## summarize\n" +
		"Summarize previous facts in two sentences.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	def, err := workflow.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if def.BasicConfig == nil {
		t.Fatalf("expected BasicConfig to be parsed")
	}
	if len(def.BasicConfig.Models) != 1 {
		t.Fatalf("expected one model in basic config, got %d", len(def.BasicConfig.Models))
	}
	model := def.BasicConfig.Models[0]
	if model.Name != "wf-model" || model.Provider != "openai" || model.APIKey != "sk-123" || !model.Active {
		t.Fatalf("unexpected model parsed: %+v", model)
	}
	if def.BasicConfig.ContextRetentionTurns == nil || *def.BasicConfig.ContextRetentionTurns != 2 {
		t.Fatalf("expected contextRetentionTurns 2, got %+v", def.BasicConfig.ContextRetentionTurns)
	}

	if len(def.MCPServers) != 1 {
		t.Fatalf("expected one MCP server entry, got %d", len(def.MCPServers))
	}
	entry := def.MCPServers["notes"]
	if entry == nil {
		t.Fatalf("expected notes server config to exist")
	}
	if entry["command"] != "notes-cmd" {
		t.Fatalf("expected command to be preserved, got %#v", entry["command"])
	}

	if len(def.Steps) != 2 {
		t.Fatalf("expected two workflow steps, got %d", len(def.Steps))
	}
	if def.Steps[0].Title != "research topic" {
		t.Fatalf("unexpected first step title: %q", def.Steps[0].Title)
	}
	if def.Steps[0].Prompt != "Find three facts." {
		t.Fatalf("unexpected first prompt content: %q", def.Steps[0].Prompt)
	}
	if def.Steps[1].Prompt != "Summarize previous facts in two sentences." {
		t.Fatalf("unexpected second prompt content: %q", def.Steps[1].Prompt)
	}
}

func TestParseFileExtractsUserRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# USER RULES\nKeep it short.\n# WORKFLOWS\n## step\nDo it."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	def, err := workflow.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if def.UserRules == nil {
		t.Fatalf("expected user rules to be parsed")
	}
	if *def.UserRules != "Keep it short." {
		t.Fatalf("unexpected user rules content: %q", *def.UserRules)
	}
}

func TestParseFileErrorsWhenWorkflowsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# CONFIGS\n## Basic Config\n```json\n{}\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	if _, err := workflow.ParseFile(path); err == nil {
		t.Fatalf("expected error when workflows section is missing")
	}
}

func TestParseFileReturnsNilConfigsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# WORKFLOWS\n## only step\nDo something great."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	def, err := workflow.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if def.BasicConfig != nil {
		t.Fatalf("expected BasicConfig to be nil when omitted, got %+v", def.BasicConfig)
	}
	if def.MCPServers != nil {
		t.Fatalf("expected MCPServers to be nil when omitted, got %+v", def.MCPServers)
	}
	if len(def.Steps) != 1 || def.Steps[0].Prompt != "Do something great." {
		t.Fatalf("unexpected steps parsed: %+v", def.Steps)
	}
	if def.UserRules != nil {
		t.Fatalf("expected user rules to be nil when section is absent")
	}
}

func TestParseFileTreatsEmptyConfigBlocksAsOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# CONFIGS\n" +
		"## Basic Config\n" +
		"```json\n" +
		"\n" +
		"```\n\n" +
		"## MCP Servers\n" +
		"```json\n" +
		"\n" +
		"```\n\n" +
		"# WORKFLOWS\n" +
		"## step\n" +
		"Run prompt."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	def, err := workflow.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if def.BasicConfig == nil {
		t.Fatalf("expected BasicConfig pointer when block is present, got nil")
	}
	if def.MCPServers == nil {
		t.Fatalf("expected MCPServers map when block is present, got nil")
	}
	if len(def.MCPServers) != 0 {
		t.Fatalf("expected empty MCPServers map, got %d entries", len(def.MCPServers))
	}
}

func TestParseFileTrimsWorkflowContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# WORKFLOWS\n## step title\n\nLine one.\n\nLine two.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	def, err := workflow.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	expected := "Line one.\n\nLine two."
	if def.Steps[0].Prompt != expected {
		t.Fatalf("expected prompt %q, got %q", expected, def.Steps[0].Prompt)
	}
	if def.Steps[0].Title != "step title" {
		t.Fatalf("expected title to be preserved, got %q", def.Steps[0].Title)
	}
	if def.BasicConfig != nil || def.MCPServers != nil {
		t.Fatalf("expected configs to remain nil")
	}
}

func TestParseFileHandlesEmptyUserRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# USER RULES\n\n   \n# WORKFLOWS\n## step\nDo it."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	def, err := workflow.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if def.UserRules == nil {
		t.Fatalf("expected user rules pointer to exist even when empty")
	}
	if *def.UserRules != "" {
		t.Fatalf("expected empty user rules content, got %q", *def.UserRules)
	}
}

func TestParseFileKeepsNestedHeadingsWithinStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "WF.md")
	content := "# WORKFLOWS\n" +
		"## Do A\n" +
		"A prompt start\n" +
		"### Detail.1\n" +
		"First details\n" +
		"### Detail.2\n" +
		"Second details\n\n" +
		"## Do B\n" +
		"B prompt body.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write workflow file: %v", err)
	}

	def, err := workflow.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(def.Steps) != 2 {
		t.Fatalf("expected two workflow steps, got %d", len(def.Steps))
	}

	expected := "A prompt start\n### Detail.1\nFirst details\n### Detail.2\nSecond details"
	if def.Steps[0].Prompt != expected {
		t.Fatalf("unexpected first prompt: %q", def.Steps[0].Prompt)
	}
	if def.Steps[1].Prompt != "B prompt body." {
		t.Fatalf("unexpected second prompt: %q", def.Steps[1].Prompt)
	}
}
