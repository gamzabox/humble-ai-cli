package app

import (
	"bytes"
	"errors"
	"io"
	"os"
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
			handled, changed, err := handleWindowsControlKey(tt.prefix, reader, buf, nil, nil)
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

	handled, changed, err := handleWindowsControlKey(0x1b, bytes.NewBuffer(nil), buf, nil, nil)
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

func TestHandleWindowsControlKeyHandlesHistory(t *testing.T) {
	buf := newLineBuffer()
	for _, r := range "abc" {
		buf.Insert(r)
	}

	reader := bytes.NewBuffer([]byte{0x48})
	prevCalled := 0
	prev := func() bool {
		prevCalled++
		buf.SetString("older")
		return true
	}

	handled, changed, err := handleWindowsControlKey(0xe0, reader, buf, prev, nil)
	if err != nil {
		t.Fatalf("handleWindowsControlKey returned error: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("expected up arrow to be handled and change buffer")
	}
	if prevCalled != 1 {
		t.Fatalf("expected history previous to be invoked once, got %d", prevCalled)
	}
	if got := buf.String(); got != "older" {
		t.Fatalf("expected buffer replaced with history entry, got %q", got)
	}

	nextReader := bytes.NewBuffer([]byte{0x50})
	nextCalled := 0
	next := func() bool {
		nextCalled++
		buf.SetString("pending")
		return true
	}
	handled, changed, err = handleWindowsControlKey(0xe0, nextReader, buf, nil, next)
	if err != nil {
		t.Fatalf("handleWindowsControlKey returned error: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("expected down arrow to be handled and change buffer")
	}
	if nextCalled != 1 {
		t.Fatalf("expected history next to be invoked once, got %d", nextCalled)
	}
	if got := buf.String(); got != "pending" {
		t.Fatalf("expected buffer replaced with pending content, got %q", got)
	}
}

func TestInteractiveLineReaderEnsureANSIEnabledOnce(t *testing.T) {
	reader := newInteractiveLineReader(os.Stdin, io.Discard, nil)
	calls := 0
	reader.enableANSI = func(w io.Writer) error {
		calls++
		return nil
	}

	if err := reader.ensureANSIEnabled(); err != nil {
		t.Fatalf("ensureANSIEnabled returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected enableANSI to be called once, got %d", calls)
	}

	if err := reader.ensureANSIEnabled(); err != nil {
		t.Fatalf("ensureANSIEnabled returned error on second call: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected enableANSI not to run multiple times, got %d calls", calls)
	}
}

func TestInteractiveLineReaderEnsureANSIEnabledPropagatesError(t *testing.T) {
	reader := newInteractiveLineReader(os.Stdin, io.Discard, nil)
	wantErr := errors.New("vt failed")
	calls := 0
	reader.enableANSI = func(w io.Writer) error {
		calls++
		return wantErr
	}

	if err := reader.ensureANSIEnabled(); !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
	if err := reader.ensureANSIEnabled(); !errors.Is(err, wantErr) {
		t.Fatalf("expected cached error %v on second call, got %v", wantErr, err)
	}
	if calls != 1 {
		t.Fatalf("expected enableANSI to be called once despite errors, got %d", calls)
	}
}
