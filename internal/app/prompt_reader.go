package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

type lineReader interface {
	ReadLine(prompt string) (string, error)
}

type canonicalLineReader struct {
	reader *bufio.Reader
	output io.Writer
}

func newCanonicalLineReader(input io.Reader, output io.Writer) *canonicalLineReader {
	return &canonicalLineReader{
		reader: bufio.NewReader(input),
		output: output,
	}
}

func (r *canonicalLineReader) ReadLine(prompt string) (string, error) {
	if prompt != "" {
		if _, err := fmt.Fprint(r.output, prompt); err != nil {
			return "", err
		}
	}
	text, err := r.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(text, "\r\n"), nil
}

type interactiveLineReader struct {
	input       *os.File
	output      io.Writer
	onInterrupt func()
}

func newInteractiveLineReader(input *os.File, output io.Writer, onInterrupt func()) *interactiveLineReader {
	return &interactiveLineReader{
		input:       input,
		output:      output,
		onInterrupt: onInterrupt,
	}
}

func (r *interactiveLineReader) ReadLine(prompt string) (string, error) {
	fd := int(r.input.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = term.Restore(fd, oldState)
	}()

	reader := bufio.NewReader(r.input)
	buffer := newLineBuffer()

	if prompt != "" {
		if _, err := fmt.Fprint(r.output, prompt); err != nil {
			return "", err
		}
	}

	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}

		switch b {
		case '\r', '\n':
			renderLine(r.output, prompt, buffer)
			_, _ = fmt.Fprint(r.output, "\r\n")
			return buffer.String(), nil
		case 0x03: // Ctrl+C
			if r.onInterrupt != nil {
				r.onInterrupt()
			}
			_, _ = fmt.Fprint(r.output, "^C\r\n")
			return "", io.EOF
		case 0x04: // Ctrl+D
			if len(buffer.runes) == 0 {
				_, _ = fmt.Fprint(r.output, "\r\n")
				return "", io.EOF
			}
		case 0x7f, 0x08: // Backspace / Ctrl+H
			if buffer.Backspace() {
				renderLine(r.output, prompt, buffer)
			}
		case 0x1b:
			moved := r.handleEscape(reader, buffer)
			if moved {
				renderLine(r.output, prompt, buffer)
			}
		default:
			if r.insertRune(b, reader, buffer) {
				renderLine(r.output, prompt, buffer)
			}
		}
	}
}

func (r *interactiveLineReader) insertRune(first byte, reader *bufio.Reader, buffer *lineBuffer) bool {
	if first < utf8.RuneSelf {
		buffer.Insert(rune(first))
		return true
	}

	var buf [utf8.UTFMax]byte
	buf[0] = first
	size := 1
	for size < utf8.UTFMax && !utf8.FullRune(buf[:size]) {
		next, err := reader.ReadByte()
		if err != nil {
			return false
		}
		buf[size] = next
		size++
	}
	runeValue, width := utf8.DecodeRune(buf[:size])
	if runeValue == utf8.RuneError && width == 1 {
		return false
	}
	buffer.Insert(runeValue)
	return true
}

func (r *interactiveLineReader) handleEscape(reader *bufio.Reader, buffer *lineBuffer) bool {
	next, err := reader.ReadByte()
	if err != nil {
		return false
	}

	switch next {
	case '[':
		seq, err := readCSISequence(reader)
		if err != nil {
			return false
		}
		switch seq {
		case "D":
			return buffer.MoveLeft()
		case "C":
			return buffer.MoveRight()
		case "H", "1~", "7~":
			return buffer.MoveHome()
		case "F", "4~", "8~":
			return buffer.MoveEnd()
		case "3~":
			return buffer.Delete()
		default:
			return false
		}
	case 'O':
		third, err := reader.ReadByte()
		if err != nil {
			return false
		}
		switch third {
		case 'H':
			return buffer.MoveHome()
		case 'F':
			return buffer.MoveEnd()
		default:
			return false
		}
	default:
		return false
	}
}

func readCSISequence(reader *bufio.Reader) (string, error) {
	var seq []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		seq = append(seq, b)
		if b == '~' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') {
			break
		}
		if len(seq) > 6 {
			break
		}
	}
	return string(seq), nil
}

func createLineReader(input io.Reader, output io.Writer, onInterrupt func()) lineReader {
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return newInteractiveLineReader(file, output, onInterrupt)
	}
	return newCanonicalLineReader(input, output)
}
