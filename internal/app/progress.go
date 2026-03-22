package app

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type Progress struct {
	label     string
	done      int
	failed    int
	total     int
	startedAt time.Time
	lastPrint time.Time
	lastLen   int
	isTTY     bool
	mu        sync.Mutex
	printMu   sync.Mutex
}

func NewProgress(label string) *Progress {
	return &Progress{
		label:     strings.TrimSpace(label),
		startedAt: time.Now(),
		isTTY:     isStdoutTTY(),
	}
}

func (p *Progress) Start() {
	p.print(true)
}

func (p *Progress) AddTotal(n int) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	p.total += n
	p.mu.Unlock()
	p.print(false)
}

// IncOK increments the completed counter.
func (p *Progress) IncOK() {
	p.mu.Lock()
	p.done++
	p.mu.Unlock()
	p.print(false)
}

// IncFail increments the completed counter and tracks a failure.
func (p *Progress) IncFail() {
	p.mu.Lock()
	p.done++
	p.failed++
	p.mu.Unlock()
	p.print(false)
}

func (p *Progress) Failed() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failed
}

func (p *Progress) Finish() {
	p.print(true)
	if p.isTTY {
		fmt.Print("\n")
	}
}

func (p *Progress) print(force bool) {
	p.printMu.Lock()
	defer p.printMu.Unlock()

	now := time.Now()

	if !p.isTTY {
		if !force && now.Sub(p.lastPrint) < 1*time.Second {
			return
		}
		fmt.Println(p.line())
		p.lastPrint = now
		return
	}

	if !force && now.Sub(p.lastPrint) < 75*time.Millisecond {
		return
	}

	line := p.line()
	if len(line) < p.lastLen {
		line = line + strings.Repeat(" ", p.lastLen-len(line))
	}
	p.lastLen = len(line)
	fmt.Printf("\r%s", line)
	p.lastPrint = now
}

func (p *Progress) line() string {
	p.mu.Lock()
	done := p.done
	total := p.total
	failed := p.failed
	label := p.label
	startedAt := p.startedAt
	p.mu.Unlock()

	pct := 0
	if total > 0 {
		pct = int(float64(done) / float64(total) * 100.0)
		if pct > 100 {
			pct = 100
		}
	}

	bar := renderProgressBar(done, total, 24)
	elapsed := formatElapsed(time.Since(startedAt))

	if label == "" {
		line := fmt.Sprintf("%s %3d%% %d/%d %s", bar, pct, done, total, elapsed)
		if failed > 0 {
			line += fmt.Sprintf(" fail:%d", failed)
		}
		return line
	}

	line := fmt.Sprintf("%-14s %s %3d%% %d/%d %s", label, bar, pct, done, total, elapsed)
	if failed > 0 {
		line += fmt.Sprintf(" fail:%d", failed)
	}
	return line
}

func renderProgressBar(done, total, width int) string {
	if width <= 0 {
		width = 20
	}

	filled := 0
	if total > 0 {
		filled = int(float64(done) / float64(total) * float64(width))
		if filled > width {
			filled = width
		}
	}

	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)

	hours := int(d / time.Hour)
	minutes := int(d/time.Minute) % 60
	seconds := int(d/time.Second) % 60

	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func isStdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
