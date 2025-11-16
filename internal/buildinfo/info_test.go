package buildinfo_test

import (
	"testing"

	"github.com/gamzabox/humble-ai-cli/internal/buildinfo"
)

func TestDefaults(t *testing.T) {
	if diff := buildinfo.Version; diff != "dev" {
		t.Fatalf("expected default version %q, got %q", "dev", diff)
	}

	if diff := buildinfo.Date; diff != "unknown" {
		t.Fatalf("expected default date %q, got %q", "unknown", diff)
	}
}
