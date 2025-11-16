package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandleInterruptInInputModeGuidesCtrlD(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	a := &App{
		output: &out,
		mode:   modeInput,
	}

	a.handleInterrupt()

	if a.shouldExit() {
		t.Fatalf("Ctrl+C in input mode should not request exit")
	}
	got := out.String()
	if !strings.Contains(got, "Press CTRL+D to exit the program.") {
		t.Fatalf("expected CTRL+D guidance message, got %q", got)
	}
}
