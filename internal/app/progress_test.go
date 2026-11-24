package app

import (
	"testing"
	"time"
)

func TestRenderProgressBar(t *testing.T) {
	t.Parallel()

	if got := renderProgressBar(3, 4, 8); got != "[######..]" {
		t.Fatalf("unexpected bar: %q", got)
	}
}

func TestFormatElapsed(t *testing.T) {
	t.Parallel()

	if got := formatElapsed(65 * time.Second); got != "01:05" {
		t.Fatalf("unexpected elapsed: %q", got)
	}
}
