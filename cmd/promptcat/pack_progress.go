package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

// packProgress counts eligible sources after they are written or skipped.
// It redraws only when progress changes and both output streams are terminals.
// The summary counts actual ZIP entries and recorded skipped paths.
type packProgress struct {
	writer      io.Writer
	interactive bool
	color       bool
	started     time.Time
	operation   string
	total       int
	completed   int
	visible     bool
}

func newPackProgress(stdout, stderr io.Writer, detailed bool) *packProgress {
	stdoutFile, stdoutOK := stdout.(*os.File)
	stderrFile, stderrOK := stderr.(*os.File)
	interactive := stdoutOK && stderrOK && term.IsTerminal(int(stdoutFile.Fd())) && term.IsTerminal(int(stderrFile.Fd())) && os.Getenv("TERM") != "dumb" && !detailed
	progress := &packProgress{
		writer: stderr, interactive: interactive,
		color: interactive && os.Getenv("NO_COLOR") == "", started: time.Now(),
	}
	if interactive {
		fmt.Fprint(stderr, "Promptcat · Packing project\n\n")
	}
	return progress
}

func (p *packProgress) start(total int) {
	p.total = total
	p.completed = 0
	p.operation = "Creating archive"
	if p.interactive {
		p.draw()
	}
}

func (p *packProgress) update() {
	if p.completed >= p.total {
		return
	}
	p.completed++
	if p.interactive {
		p.draw()
	}
}

func (p *packProgress) setOperation(operation string) {
	if p.operation == operation {
		return
	}
	p.operation = operation
	if p.interactive {
		p.draw()
	}
}

func (p *packProgress) draw() {
	const width = 30
	percentage := 0
	filled := 0
	if p.total > 0 {
		percentage = p.completed * 100 / p.total
		filled = p.completed * width / p.total
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	if p.color {
		bar = "\x1b[32m" + strings.Repeat("█", filled) + "\x1b[0m" + strings.Repeat("░", width-filled)
	}
	fmt.Fprintf(p.writer, "\r\x1b[2K%s...  %s %3d%%  %d / %d files", p.operation, bar, percentage, p.completed, p.total)
	p.visible = true
}

func (p *packProgress) clear() {
	if p.visible {
		fmt.Fprint(p.writer, "\r\x1b[2K")
		p.visible = false
	}
}

func (p *packProgress) fail() {
	p.clear()
}

func (p *packProgress) summary(files, skipped int, size int64, path string) {
	p.clear()
	if home, err := os.UserHomeDir(); err == nil {
		if relative, err := filepath.Rel(home, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			path = filepath.Join("~", relative)
		}
	}
	sizeText := fmt.Sprintf("%d B", size)
	if size >= 1000 {
		sizeText = fmt.Sprintf("%.1f KB", float64(size)/1000)
	}
	if size >= 1000000 {
		sizeText = fmt.Sprintf("%.1f MB", float64(size)/1000000)
	}
	check := "✓"
	if p.color {
		check = "\x1b[32m✓\x1b[0m"
	}
	fmt.Fprintf(p.writer, "%s Archive created successfully!\n\n  Files:      %d\n  Skipped:    %d paths\n  Size:       %s\n  Duration:   %.1fs\n  Output:     %s\n", check, files, skipped, sizeText, time.Since(p.started).Seconds(), path)
}
