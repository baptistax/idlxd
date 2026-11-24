package app

import (
	"os"
	"syscall"
)

func consoleMode(f *os.File) (interactive, ansi bool) {
	var mode uint32
	if syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) != nil {
		return false, false
	}
	const virtualTerminalProcessing = 0x0004
	return true, mode&virtualTerminalProcessing != 0
}
