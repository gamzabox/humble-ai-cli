package app

import (
	"strings"
	"testing"
)

func TestLineBufferInsertWithCursorMovement(t *testing.T) {
	buf := newLineBuffer()
	for _, r := range "hello" {
		buf.Insert(r)
	}
	buf.MoveLeft()
	buf.MoveLeft()
	buf.Insert('X')

	if got := buf.String(); got != "helXlo" {
		t.Fatalf("expected buffer to be %q, got %q", "helXlo", got)
	}

	if cursor := buf.CursorWidth(); cursor != 4 {
		t.Fatalf("expected cursor width 4 after insertion, got %d", cursor)
	}
}

func TestLineBufferSupportsCJKRunes(t *testing.T) {
	buf := newLineBuffer()
	for _, r := range []rune{'한', '글'} {
		buf.Insert(r)
	}
	buf.MoveLeft()
	buf.Insert('テ')

	if got := buf.String(); got != "한テ글" {
		t.Fatalf("expected buffer to be %q, got %q", "한テ글", got)
	}

	if cursor := buf.CursorWidth(); cursor != 4 {
		t.Fatalf("expected cursor width 4 for CJK handling, got %d", cursor)
	}
}

func TestRenderLineProducesExpectedCursorMovement(t *testing.T) {
	buf := newLineBuffer()
	for _, r := range []rune{'你', '好', '!'} {
		buf.Insert(r)
	}
	buf.MoveLeft()

	var builder strings.Builder
	renderLine(&builder, "humble-ai> ", buf)

	got := builder.String()
	expected := "\rhumble-ai> 你好!\x1b[K\x1b[1D"
	if got != expected {
		t.Fatalf("render output mismatch\nexpected: %q\ngot:      %q", expected, got)
	}
}
