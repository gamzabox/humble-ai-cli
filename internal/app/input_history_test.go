package app

import "testing"

func TestInputHistoryPreviousNavigation(t *testing.T) {
	h := newInputHistory()
	h.Add("first")
	h.Add("second")
	h.Add("third")

	got, ok := h.Previous("")
	if !ok || got != "third" {
		t.Fatalf("expected last entry, got %q (ok=%v)", got, ok)
	}

	got, ok = h.Previous("")
	if !ok || got != "second" {
		t.Fatalf("expected middle entry, got %q (ok=%v)", got, ok)
	}

	got, ok = h.Previous("")
	if !ok || got != "first" {
		t.Fatalf("expected oldest entry, got %q (ok=%v)", got, ok)
	}

	if _, ok := h.Previous(""); ok {
		t.Fatalf("expected no more entries when already at oldest")
	}
}

func TestInputHistoryNextRestoresPendingInput(t *testing.T) {
	h := newInputHistory()
	h.Add("old")
	h.Add("new")

	if _, ok := h.Next(); ok {
		t.Fatalf("expected next to return false when not browsing")
	}

	got, ok := h.Previous("draft message")
	if !ok || got != "new" {
		t.Fatalf("expected to load most recent entry, got %q (ok=%v)", got, ok)
	}

	got, ok = h.Previous("ignored")
	if !ok || got != "old" {
		t.Fatalf("expected to navigate to older entry, got %q (ok=%v)", got, ok)
	}

	got, ok = h.Next()
	if !ok || got != "new" {
		t.Fatalf("expected to move forward within history, got %q (ok=%v)", got, ok)
	}

	got, ok = h.Next()
	if !ok || got != "draft message" {
		t.Fatalf("expected pending content restored, got %q (ok=%v)", got, ok)
	}

	if _, ok := h.Next(); ok {
		t.Fatalf("expected navigation to reset after reaching pending input")
	}
}

func TestInputHistorySkipsEmptyEntries(t *testing.T) {
	h := newInputHistory()
	h.Add("")
	h.Add("data")

	got, ok := h.Previous("")
	if !ok || got != "data" {
		t.Fatalf("expected only non-empty entry to be returned, got %q (ok=%v)", got, ok)
	}

	if _, ok := h.Previous(""); ok {
		t.Fatalf("expected no additional entries after single stored value")
	}
}
