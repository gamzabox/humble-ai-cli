package app

import (
	"bytes"
	"testing"
)

func TestHandleWindowsControlKeyRecognizesEditKeys(t *testing.T) {
	tests := []struct {
		name        string
		prefix      byte
		seq         byte
		setup       func(*lineBuffer)
		wantCursor  int
		wantContent string
		wantChanged bool
	}{
		{
			name:        "left arrow",
			prefix:      0xe0,
			seq:         0x4b,
			setup:       func(buf *lineBuffer) {},
			wantCursor:  2,
			wantContent: "abc",
			wantChanged: true,
		},
		{
			name:   "right arrow",
			prefix: 0xe0,
			seq:    0x4d,
			setup: func(buf *lineBuffer) {
				buf.MoveLeft()
			},
			wantCursor:  3,
			wantContent: "abc",
			wantChanged: true,
		},
		{
			name:   "home key",
			prefix: 0xe0,
			seq:    0x47,
			setup: func(buf *lineBuffer) {
				buf.MoveLeft()
			},
			wantCursor:  0,
			wantContent: "abc",
			wantChanged: true,
		},
		{
			name:   "end key",
			prefix: 0xe0,
			seq:    0x4f,
			setup: func(buf *lineBuffer) {
				buf.MoveLeft()
				buf.MoveLeft()
			},
			wantCursor:  3,
			wantContent: "abc",
			wantChanged: true,
		},
		{
			name:   "delete key",
			prefix: 0xe0,
			seq:    0x53,
			setup: func(buf *lineBuffer) {
				buf.MoveLeft()
				buf.MoveLeft()
			},
			wantCursor:  1,
			wantContent: "ac",
			wantChanged: true,
		},
		{
			name:        "zero prefix handled",
			prefix:      0x00,
			seq:         0x4b,
			setup:       func(buf *lineBuffer) {},
			wantCursor:  2,
			wantContent: "abc",
			wantChanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := newLineBuffer()
			for _, r := range "abc" {
				buf.Insert(r)
			}
			if tt.setup != nil {
				tt.setup(buf)
			}

			reader := bytes.NewBuffer([]byte{tt.seq})
			handled, changed, err := handleWindowsControlKey(tt.prefix, reader, buf)
			if err != nil {
				t.Fatalf("handleWindowsControlKey returned error: %v", err)
			}
			if !handled {
				t.Fatalf("expected key to be handled")
			}
			if changed != tt.wantChanged {
				t.Fatalf("expected changed %v, got %v", tt.wantChanged, changed)
			}
			if got := buf.String(); got != tt.wantContent {
				t.Fatalf("expected buffer content %q, got %q", tt.wantContent, got)
			}
			if buf.cursor != tt.wantCursor {
				t.Fatalf("expected cursor position %d, got %d", tt.wantCursor, buf.cursor)
			}
		})
	}
}

func TestHandleWindowsControlKeyIgnoresUnknownPrefix(t *testing.T) {
	buf := newLineBuffer()
	for _, r := range "abc" {
		buf.Insert(r)
	}

	handled, changed, err := handleWindowsControlKey(0x1b, bytes.NewBuffer(nil), buf)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if handled {
		t.Fatalf("expected handled to be false for unknown prefix")
	}
	if changed {
		t.Fatalf("expected changed to be false for unknown prefix")
	}
	if got := buf.String(); got != "abc" {
		t.Fatalf("expected buffer unchanged, got %q", got)
	}
}
