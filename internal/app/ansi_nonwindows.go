//go:build !windows

package app

import "io"

func enableVirtualTerminalSequences(io.Writer) error {
	return nil
}
