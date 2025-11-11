package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gamzabox/humble-ai-cli/internal/app"
	"github.com/gamzabox/humble-ai-cli/internal/config"
	"github.com/gamzabox/humble-ai-cli/internal/llm"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to determine home directory: %v\n", err)
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
	}

	instance, err := app.New(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize application: %v\n", err)
		os.Exit(1)
	}

	if err := instance.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}
