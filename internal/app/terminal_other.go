//go:build !linux && !windows

package app

import "os"

// Use plain output on platforms without a console probe.
func consoleMode(*os.File) (interactive, ansi bool) { return false, false }
