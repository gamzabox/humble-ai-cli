package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gamzabox/humble-ai-cli/internal/app"
	"github.com/gamzabox/humble-ai-cli/internal/buildinfo"
	"github.com/gamzabox/humble-ai-cli/internal/config"
	"github.com/gamzabox/humble-ai-cli/internal/llm"
	"github.com/gamzabox/humble-ai-cli/internal/workflow"
)

func main() {
	args := os.Args[1:]

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "> failed to determine home directory: %v\n", err)
		os.Exit(1)
	}

	store := config.NewFileStore(home)
	factory := llm.NewFactory(nil)

	options := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          os.Stdin,
		Output:         os.Stdout,
		ErrorOutput:    os.Stderr,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		Version:        buildinfo.Version,
		BuildDate:      buildinfo.Date,
	}

	if len(args) > 0 && strings.EqualFold(args[0], "exec") {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "> workflow file path is required (e.g., humble-ai-cli exec my-workflow.md)")
			os.Exit(1)
		}

		definition, err := workflow.ParseFile(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "> failed to load workflow file: %v\n", err)
			os.Exit(1)
		}

		if err := app.ExecuteWorkflow(context.Background(), options, definition); err != nil {
			fmt.Fprintf(os.Stderr, "> workflow execution failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	instance, err := app.New(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "> failed to initialize application: %v\n", err)
		os.Exit(1)
	}

	if err := instance.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "> application error: %v\n", err)
		os.Exit(1)
	}
}
