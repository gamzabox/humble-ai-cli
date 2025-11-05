package app

import (
	"fmt"
	"io"

	"github.com/mattn/go-runewidth"
)

type lineBuffer struct {
	runes  []rune
	cursor int
}

func newLineBuffer() *lineBuffer {
	return &lineBuffer{
		runes:  make([]rune, 0, 64),
		cursor: 0,
	}
}

func (b *lineBuffer) Insert(r rune) {
	if b.cursor == len(b.runes) {
		b.runes = append(b.runes, r)
	} else {
		b.runes = append(b.runes[:b.cursor], append([]rune{r}, b.runes[b.cursor:]...)...)
	}
	b.cursor++
}

func (b *lineBuffer) MoveLeft() bool {
	if b.cursor == 0 {
		return false
	}
	b.cursor--
	return true
}

func (b *lineBuffer) MoveRight() bool {
	if b.cursor >= len(b.runes) {
		return false
	}
	b.cursor++
	return true
}

func (b *lineBuffer) MoveHome() bool {
	if b.cursor == 0 {
		return false
	}
	b.cursor = 0
	return true
}

func (b *lineBuffer) MoveEnd() bool {
	if b.cursor == len(b.runes) {
		return false
	}
	b.cursor = len(b.runes)
	return true
}

func (b *lineBuffer) Backspace() bool {
	if b.cursor == 0 || len(b.runes) == 0 {
		return false
	}
	b.runes = append(b.runes[:b.cursor-1], b.runes[b.cursor:]...)
	b.cursor--
	return true
}

func (b *lineBuffer) Delete() bool {
	if b.cursor >= len(b.runes) || len(b.runes) == 0 {
		return false
	}
	b.runes = append(b.runes[:b.cursor], b.runes[b.cursor+1:]...)
	return true
}

func (b *lineBuffer) String() string {
	return string(b.runes)
}

func (b *lineBuffer) CursorWidth() int {
	if b.cursor == 0 {
		return 0
	}
	return runewidth.StringWidth(string(b.runes[:b.cursor]))
}

func (b *lineBuffer) ContentWidth() int {
	if len(b.runes) == 0 {
		return 0
	}
	return runewidth.StringWidth(string(b.runes))
}

func renderLine(w io.Writer, prompt string, buf *lineBuffer) {
	line := buf.String()
	_, _ = fmt.Fprintf(w, "\r%s%s", prompt, line)
	_, _ = fmt.Fprint(w, "\x1b[K")
	moveLeft := buf.ContentWidth() - buf.CursorWidth()
	if moveLeft > 0 {
		_, _ = fmt.Fprintf(w, "\x1b[%dD", moveLeft)
	}
}
