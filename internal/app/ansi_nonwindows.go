//go:build !windows

package app

import (
	"io"
	"os"
)

func enableVirtualTerminalSequences(io.Writer) error {
	return nil
}

func enableVirtualTerminalInput(*os.File) error {
	return nil
}
