package display

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hekmon/liveterm"
	"golang.org/x/term"
)

const hostnameWidth = 30

// completedStep records an already-finished step with its emoticon.
type completedStep struct {
	emoji string
}

// HostLine tracks the display state for a single host.
type HostLine struct {
	mu           sync.Mutex
	hostname     string
	history      []completedStep
	currentEmoji string
	currentLabel string
	done         bool
	finalStatus  string
	liveMode     bool
	out          io.Writer
}

// UpdateStep sets the current in-flight step label (no-op in fallback mode).
func (h *HostLine) UpdateStep(emoji, label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentEmoji = emoji
	h.currentLabel = label
}

// CompleteStep marks the current step as done and adds its emoji to history.
func (h *HostLine) CompleteStep(emoji string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append(h.history, completedStep{emoji: emoji})
	h.currentEmoji = ""
	h.currentLabel = ""
}

// Finish replaces the line with the provided final status string.
func (h *HostLine) Finish(finalStatus string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.done = true
	h.finalStatus = finalStatus
	if !h.liveMode {
		fmt.Fprintf(h.out, "%s\n", finalStatus)
	}
}

// FinishError replaces the line with an error final status.
func (h *HostLine) FinishError(msg string) {
	h.Finish(fmt.Sprintf("❌ %s: %s", h.hostname, msg))
}

// render returns the current display line for this host (called from liveterm goroutine, lock held outside).
func (h *HostLine) render() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.done {
		return h.finalStatus
	}

	hostname := fmt.Sprintf("%-*s", hostnameWidth, h.hostname)

	// Build summary from completed step emojis
	var sb strings.Builder
	for _, s := range h.history {
		sb.WriteString(s.emoji)
	}
	summary := sb.String()

	var current string
	if h.currentEmoji != "" || h.currentLabel != "" {
		current = fmt.Sprintf("%s %s", h.currentEmoji, h.currentLabel)
	}

	if summary == "" && current == "" {
		return hostname
	}
	if summary == "" {
		return fmt.Sprintf("%s  %s", hostname, current)
	}
	if current == "" {
		return fmt.Sprintf("%s  %s", hostname, summary)
	}
	return fmt.Sprintf("%s  %s %s", hostname, summary, current)
}

// LiveDisplay manages per-host live output lines.
type LiveDisplay struct {
	lines    []*HostLine
	liveMode bool
	out      io.Writer
}

// New creates a LiveDisplay. If debug=true or stdout is not a TTY, falls back to verbose mode.
func New(out io.Writer, hosts []string, debug bool) *LiveDisplay {
	liveMode := false
	if !debug {
		if f, ok := out.(*os.File); ok {
			liveMode = term.IsTerminal(int(f.Fd()))
		}
	}

	lines := make([]*HostLine, len(hosts))
	for i, host := range hosts {
		lines[i] = &HostLine{
			hostname: host,
			liveMode: liveMode,
			out:      out,
		}
	}

	return &LiveDisplay{
		lines:    lines,
		liveMode: liveMode,
		out:      out,
	}
}

// Line returns the HostLine for the i-th host (0-indexed).
func (d *LiveDisplay) Line(i int) *HostLine {
	return d.lines[i]
}

// Start begins live output. In fallback mode this is a no-op.
func (d *LiveDisplay) Start() {
	if !d.liveMode {
		return
	}
	liveterm.RefreshInterval = 100 * time.Millisecond
	liveterm.Output = d.out
	liveterm.SetMultiLinesUpdateFx(d.renderLines)
	if err := liveterm.Start(); err != nil {
		// If liveterm fails to start (e.g. no real TTY despite fd check), fall back.
		d.liveMode = false
		for _, l := range d.lines {
			l.liveMode = false
		}
	}
}

// Stop finalises live output. In fallback mode this is a no-op.
func (d *LiveDisplay) Stop() {
	if !d.liveMode {
		return
	}
	liveterm.Stop(false)
}

// renderLines returns all host lines for liveterm to display.
func (d *LiveDisplay) renderLines() []string {
	out := make([]string, len(d.lines))
	for i, l := range d.lines {
		out[i] = l.render()
	}
	return out
}
