package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/instagram"
	"github.com/baptistax/idl/internal/instagram/auth"
)

type terminal struct {
	out            io.Writer
	animate, color bool
}

func stdoutTerminal() terminal {
	tty, ansi := consoleMode(os.Stdout)
	return terminal{os.Stdout, tty && os.Getenv("TERM") != "dumb", ansi && os.Getenv("TERM") != "dumb" && os.Getenv("NO_COLOR") == ""}
}

func (u terminal) banner(title string) {
	lines := []string{
		" ___ ____  _      ",
		"|_ _|  _ \\| |     ",
		" | || | | | |     ",
		" | || |_| | |___  ",
		"|___|____/|_____| ",
	}
	border := "+" + strings.Repeat("-", 22) + "+"
	fmt.Fprintln(u.out, border)
	for _, line := range lines {
		fmt.Fprintf(u.out, "| %-20s |\n", line)
	}
	fmt.Fprintln(u.out, border)
	fmt.Fprintf(u.out, "IDL v2.0\n%s\n\n", title)
}

func (u terminal) styled(s, code string) string {
	if u.color {
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	return s
}

func (u terminal) check(label string, err error) {
	if err == nil {
		fmt.Fprintln(u.out, u.styled("[OK]", "32"), label)
	} else {
		fmt.Fprintln(u.out, u.styled("[!!]", "31"), label)
	}
}

func (u terminal) warning(label string) {
	fmt.Fprintln(u.out, u.styled("[--]", "33"), label)
}

// Only the spinner writes to the terminal until stop returns.
func (u terminal) spin(ctx context.Context, label string) func() {
	if !u.animate {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		defer fmt.Fprintf(u.out, "\r%s\r", strings.Repeat(" ", len(label)+4))
		for i := 0; ; i++ {
			fmt.Fprintf(u.out, "\r[%c] %s", "|/-\\"[i%4], label)
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { close(stop); <-done }
}

func (u terminal) wait(ctx context.Context, label string, work func() error) error {
	stop := u.spin(ctx, label)
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := work(); err != nil {
		return err
	}
	return ctx.Err()
}

func userMessage(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "Download canceled."
	case windowsPolicyError(err):
		return "Blocked by Windows security policy."
	case fault.Is(err, fault.Budget):
		return "Request limit reached. Download stopped."
	case fault.Is(err, fault.RateLimited):
		return "Rate limit reached. Download stopped."
	case fault.Is(err, fault.Verification), fault.Is(err, fault.Permission):
		return "Access restricted."
	case errors.Is(err, auth.ErrMissing):
		return "cookies.txt not found."
	case errors.Is(err, auth.ErrInvalid):
		return "Invalid cookie file."
	case errors.Is(err, auth.ErrExpired):
		return "Session expired. Update cookies.txt."
	case errors.Is(err, auth.ErrUnreadable):
		return "Cookie access unavailable."
	case fault.Is(err, fault.Authentication):
		return "Authentication unavailable."
	case errors.Is(err, instagram.ErrProfileNotFound):
		return "Profile not found."
	case fault.Is(err, fault.Unavailable):
		return "Media unavailable."
	case fault.Is(err, fault.Download):
		return "Download failed."
	case fault.Is(err, fault.Network), errors.Is(err, context.DeadlineExceeded):
		return "Connection failed."
	case fault.Is(err, fault.Schema):
		return "Media compatibility error."
	case fault.Is(err, fault.Upstream):
		return "Instagram request failed."
	case fault.Is(err, fault.Unsupported):
		return "Unsupported media or collection."
	case fault.Is(err, fault.Partial):
		return "Partial response."
	case fault.Is(err, fault.InvalidInput):
		return "Invalid configuration."
	case fault.Is(err, fault.Storage):
		return "Local storage unavailable."
	default:
		return "Completed with errors."
	}
}

func windowsPolicyError(err error) bool {
	// ERROR_ACCESS_DISABLED_BY_POLICY and ERROR_SYSTEM_INTEGRITY_POLICY_VIOLATION.
	return runtime.GOOS == "windows" && (errors.Is(err, syscall.Errno(1260)) || errors.Is(err, syscall.Errno(4551)))
}

// Show only local validation diagnostics and numeric HTTP status. Raw OS and
// transport errors can contain private paths or signed media URLs.
func mediaMessage(err error) string {
	if windowsPolicyError(err) {
		return userMessage(err)
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		e, ok := cause.(*fault.Error)
		if !ok {
			continue
		}
		if e.StatusCode != 0 {
			if fault.Global(err) {
				return fmt.Sprintf("%s (HTTP %d).", strings.TrimSuffix(userMessage(err), "."), e.StatusCode)
			}
			op := "Media transfer"
			if e.Op == "request" {
				op = "Media lookup"
			}
			return fmt.Sprintf("%s returned HTTP %d.", op, e.StatusCode)
		}
		if e.Kind == fault.Schema && (e.Op == "media file" || e.Op == "download") {
			return "Media validation failed (" + e.Detail + ")."
		}
	}
	var pathErr *os.PathError
	var renameErr *os.LinkError
	switch {
	case fault.Global(err):
		return userMessage(err)
	case errors.Is(err, context.DeadlineExceeded):
		return "Media request timed out."
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "Media transfer was truncated."
	case errors.As(err, &pathErr), errors.As(err, &renameErr), fault.Is(err, fault.Storage):
		if errors.Is(err, os.ErrPermission) {
			return "Local file access denied."
		}
		return "Local file operation failed."
	case fault.Is(err, fault.Network):
		return "Media connection failed."
	case fault.Is(err, fault.Upstream), fault.Is(err, fault.Download):
		return "Media request failed."
	default:
		return userMessage(err)
	}
}

func (u terminal) finished(start time.Time) {
	fmt.Fprintln(u.out, "Finished in", formatElapsed(time.Since(start)))
}
