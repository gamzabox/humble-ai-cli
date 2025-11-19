//go:build windows

package app

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func enableVirtualTerminalSequences(writer io.Writer) error {
	file, ok := writer.(*os.File)
	if !ok {
		return nil
	}

	handle := windows.Handle(file.Fd())

	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			return nil
		}
		return err
	}

	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return nil
	}

	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
