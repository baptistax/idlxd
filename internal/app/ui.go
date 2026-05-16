package app

import (
	"fmt"
	"strings"
	"time"
)

func printBanner() {
	lines := []string{
		" ___ ____  _      ",
		"|_ _|  _ \\| |     ",
		" | || | | | |     ",
		" | || |_| | |___  ",
		"|___|____/|_____| ",
		"Instagram Downloader",
	}

	width := 0
	for _, line := range lines {
		if len(line) > width {
			width = len(line)
		}
	}

	border := "+" + strings.Repeat("-", width+2) + "+"
	fmt.Println(border)
	for _, line := range lines {
		fmt.Printf("| %-*s |\n", width, line)
	}
	fmt.Println(border)
}

func printKV(label, value string) {
	fmt.Printf("%-10s %s\n", label+":", value)
}

func printSectionHeader(step, total int, title string) {
	header := title
	if step > 0 && total > 0 {
		header = fmt.Sprintf("[%d/%d] %s", step, total, title)
	}
	fmt.Printf("\n%s\n", header)
	fmt.Println(strings.Repeat("-", len(header)))
}

func printSectionSummary(downloaded, failed int) {
	fmt.Printf("Saved: %d files\n", downloaded)
	fmt.Printf("Failed: %d files\n", failed)
}

func printSectionError(err error) {
	if err != nil {
		fmt.Printf("Section error: %v\n", err)
	}
}

func printFooter(elapsed time.Duration, status string) {
	fmt.Printf("\nFinished in %s\n", formatElapsed(elapsed))
	fmt.Printf("Status: %s\n", status)
}
