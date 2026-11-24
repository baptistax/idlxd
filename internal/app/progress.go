package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/baptistax/idl/internal/fault"
	"github.com/baptistax/idl/internal/models"
)

// One section is downloaded sequentially. Totals grow as pages arrive.
type Progress struct {
	fileCounts
	ui        terminal
	label     string
	total     int
	lastPrint time.Time
	stop      func()
}

func (p *Progress) loading(ctx context.Context) {
	p.stopSpinner()
	if p.ui.animate {
		fmt.Fprint(p.ui.out, "\r", strings.Repeat(" ", len(p.line())), "\r")
	}
	p.stop = p.ui.spin(ctx, p.label)
}

func (p *Progress) stopSpinner() {
	if p.stop != nil {
		p.stop()
		p.stop = nil
	}
}

func (p *Progress) addTotal(n int) {
	p.stopSpinner()
	p.total += n
	p.update()
}

type mediaFailure struct {
	id    string
	cause error
}

func (e *mediaFailure) Error() string { return mediaMessage(e.cause) }
func (e *mediaFailure) Unwrap() error { return e.cause }

func (p *Progress) record(id string, err error) error {
	if err == nil {
		p.saved++
	} else {
		p.failed++
		err = &mediaFailure{id, err}
	}
	p.update()
	return err
}

func (p *Progress) update() {
	if p.ui.animate && time.Since(p.lastPrint) >= 75*time.Millisecond {
		fmt.Fprintf(p.ui.out, "\r%s", p.line())
		p.lastPrint = time.Now()
	}
}

func (p *Progress) line() string {
	done := p.saved + p.existing + p.repeated + p.failed
	return fmt.Sprintf("%-11s %s %d/%d processed", p.label, renderProgressBar(done, p.total, 20), done, p.total)
}

func (p *Progress) finish(err error, skip string) {
	p.stopSpinner()
	if p.ui.animate {
		fmt.Fprint(p.ui.out, "\r")
	}
	if p.total == 0 && skip != "" {
		message := p.label + ": " + skip
		if skip == "not checked" && err != nil {
			message += "  " + userMessage(err)
		}
		p.ui.warning(message)
		return
	}
	if err != nil && p.total == 0 {
		p.ui.check(p.label+"  "+userMessage(err), err)
		return
	}
	line := p.line()
	if p.total > 0 {
		line += fmt.Sprintf("  %d new", p.saved)
	}
	if skip != "" {
		line += "  collection " + skip
	}
	if p.existing > 0 {
		line += fmt.Sprintf("  %d existing", p.existing)
	}
	if p.repeated > 0 {
		line += fmt.Sprintf("  %d repeated", p.repeated)
	}
	if err != nil {
		switch {
		case p.failed > 0:
			line += p.ui.styled(fmt.Sprintf("  %d failed", p.failed), "31")
		case fault.OnlyPartial(err):
			line += p.ui.styled("  partial", "33")
		default:
			line += p.ui.styled("  incomplete", "31")
		}
		line += "  " + sectionMessages(err)
	}
	fmt.Fprintln(p.ui.out, line)
}

// Different items in a section can fail for different reasons. Keep one
// safe message per reason, even on a page with many failed items.
func sectionMessages(err error) string {
	var messages []string
	examples := map[string][]string{}
	var collect func(error)
	collect = func(err error) {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				collect(child)
			}
			return
		}
		message := userMessage(err)
		item, isItem := err.(*mediaFailure)
		if isItem {
			message = item.Error()
		}
		if _, seen := examples[message]; !seen {
			examples[message] = nil
			messages = append(messages, message)
		}
		if isItem && models.IsNumericID(item.id) {
			ids := examples[message]
			switch {
			case len(ids) < 4:
				examples[message] = append(ids, item.id)
			case len(ids) == 4:
				examples[message] = append(ids, "...")
			}
		}
	}
	collect(err)
	for i, message := range messages {
		if ids := examples[message]; len(ids) > 0 {
			messages[i] = strings.TrimSuffix(message, ".") + " [" + strings.Join(ids, ", ") + "]."
		}
	}
	return strings.Join(messages, " ")
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
