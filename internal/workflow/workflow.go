package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/gamzabox/humble-ai-cli/internal/config"
)

// Step represents a single workflow prompt.
type Step struct {
	Title  string
	Prompt string
}

// Definition captures a parsed workflow file.
type Definition struct {
	BasicConfig *config.Config
	MCPServers  map[string]map[string]any
	UserRules   *string
	Steps       []Step
}

var (
	reBasicConfig = regexp.MustCompile("(?is)##\\s*Basic Config\\s*```json\\s*(?P<body>.*?)\\s*```")
	reMCPServers  = regexp.MustCompile("(?is)##\\s*MCP Servers\\s*```json\\s*(?P<body>.*?)\\s*```")
)

// ParseFile reads and parses a workflow markdown file.
func ParseFile(path string) (Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, err
	}
	return parseContent(string(data))
}

func parseContent(content string) (Definition, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return Definition{}, errors.New("workflow file is empty")
	}

	def := Definition{}

	if matches := reBasicConfig.FindStringSubmatch(content); len(matches) > 1 {
		body := strings.TrimSpace(matches[1])
		if body != "" {
			var cfg config.Config
			if err := json.Unmarshal([]byte(body), &cfg); err != nil {
				return Definition{}, fmt.Errorf("parse Basic Config: %w", err)
			}
			def.BasicConfig = &cfg
		}
	}

	if matches := reMCPServers.FindStringSubmatch(content); len(matches) > 1 {
		body := strings.TrimSpace(matches[1])
		if body != "" {
			var payload struct {
				MCPServers map[string]map[string]any `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				return Definition{}, fmt.Errorf("parse MCP Servers: %w", err)
			}
			if len(payload.MCPServers) > 0 {
				def.MCPServers = payload.MCPServers
			}
		}
	}

	steps, err := parseWorkflowSteps(content)
	if err != nil {
		return Definition{}, err
	}
	def.Steps = steps

	if rules, ok := extractUserRules(content); ok {
		def.UserRules = &rules
	}

	return def, nil
}

func parseWorkflowSteps(content string) ([]Step, error) {
	lines := strings.Split(content, "\n")
	startIdx := -1
	for idx, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "# WORKFLOWS") {
			startIdx = idx + 1
			break
		}
	}
	if startIdx == -1 {
		return nil, errors.New("workflow file must contain a WORKFLOWS section")
	}

	var steps []Step
	var currentTitle string
	var body []string

	flush := func() error {
		if strings.TrimSpace(currentTitle) == "" {
			return nil
		}
		prompt := strings.TrimSpace(strings.Join(body, "\n"))
		if prompt == "" {
			return fmt.Errorf("workflow step %q is missing content", currentTitle)
		}
		steps = append(steps, Step{
			Title:  strings.TrimSpace(currentTitle),
			Prompt: prompt,
		})
		return nil
	}

	for _, line := range lines[startIdx:] {
		trimmed := strings.TrimSpace(line)
		if isWorkflowStepHeading(trimmed) {
			if err := flush(); err != nil {
				return nil, err
			}
			currentTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
			body = body[:0]
			continue
		}
		if strings.TrimSpace(currentTitle) != "" {
			body = append(body, line)
		}
	}

	if err := flush(); err != nil {
		return nil, err
	}

	if len(steps) == 0 {
		return nil, errors.New("workflow file must define at least one workflow step")
	}
	return steps, nil
}

func isWorkflowStepHeading(line string) bool {
	if !strings.HasPrefix(line, "##") {
		return false
	}
	if len(line) == 2 {
		return true
	}
	return line[2] != '#'
}

func extractUserRules(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	var (
		started bool
		body    []string
	)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !started {
			if strings.EqualFold(trimmed, "# USER RULES") {
				started = true
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			break
		}
		body = append(body, line)
	}
	if !started {
		return "", false
	}
	return strings.TrimSpace(strings.Join(body, "\n")), true
}
