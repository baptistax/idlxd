package app

import (
	"os"
	"syscall"
	"unsafe"
)

func consoleMode(f *os.File) (interactive, ansi bool) {
	var settings syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&settings)))
	return err == 0, err == 0 && os.Getenv("TERM") != ""
}
