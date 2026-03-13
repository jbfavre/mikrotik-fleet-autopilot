package display

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hekmon/liveterm/v2"
	"golang.org/x/term"
)

const (
	minHostnameWidth = 10
	maxHostnameWidth = 20
)

// completedStep records an already-finished step with its emoticon.
type completedStep struct {
	emoji string
}

// HostLine tracks the display state for a single host.
type HostLine struct {
	mu            sync.Mutex
	hostname      string
	hostnameWidth int
	overallEmoji  string // ⏳ while in-progress; set to the final status emoji by Finish
	history       []completedStep
	currentEmoji  string
	currentLabel  string
	done          bool
	finalMessage  string // set by Finish; does not include the hostname or overall emoji
	liveMode      bool
	out           io.Writer
}

// StepCallback is used to update step display for a host.
type StepCallback func(emoji string, message string)

func NewStepCallback(line *HostLine) StepCallback {
	return func(emoji, msg string) {
		switch emoji {
		case "✅", "❌", "❓", "⚠️":
			line.CompleteStep(emoji)
		default:
			line.UpdateStep(emoji, msg)
		}
	}
}

// UpdateStep sets the current in-flight step label.
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

// Finish marks the line as done with an overall status emoji and a final message.
// overallEmoji should be one of ✅, ❌, ❓, ⚠️.
// message is the human-readable result description (no hostname, no leading emoji).
func (h *HostLine) Finish(overallEmoji, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.done = true
	h.overallEmoji = overallEmoji
	h.finalMessage = message
	if !h.liveMode {
		fmt.Fprintf(h.out, "%s\n", h.renderUnlocked())
	}
}

// FinishError marks the line as done with ❌ and the given error message.
func (h *HostLine) FinishError(msg string) {
	h.Finish("❌", msg)
}

// renderUnlocked renders the line. The caller must hold h.mu.
func (h *HostLine) renderUnlocked() string {
	hostname := formatHostname(h.hostname, h.hostnameWidth)

	// Build summary from completed step emojis.
	var sb strings.Builder
	for _, s := range h.history {
		sb.WriteString(s.emoji)
	}
	summary := sb.String()

	if h.done {
		// Format: [overallEmoji] [hostname] [step emojis] [finalMessage]
		if summary == "" {
			return fmt.Sprintf("%s %s  %s", h.overallEmoji, hostname, h.finalMessage)
		}
		return fmt.Sprintf("%s %s  %s %s", h.overallEmoji, hostname, summary, h.finalMessage)
	}

	// In-progress: [overallEmoji] [hostname] [step emojis] [currentEmoji currentLabel]
	var current string
	if h.currentEmoji != "" || h.currentLabel != "" {
		current = fmt.Sprintf("%s %s", h.currentEmoji, h.currentLabel)
	}

	if summary == "" && current == "" {
		return fmt.Sprintf("%s %s", h.overallEmoji, hostname)
	}
	if summary == "" {
		return fmt.Sprintf("%s %s  %s", h.overallEmoji, hostname, current)
	}
	if current == "" {
		return fmt.Sprintf("%s %s  %s", h.overallEmoji, hostname, summary)
	}
	return fmt.Sprintf("%s %s  %s %s", h.overallEmoji, hostname, summary, current)
}

// render returns the current display line for this host.
func (h *HostLine) render() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.renderUnlocked()
}

// singleton guard: only one LiveDisplay may drive liveterm at a time.
var (
	singletonMu    sync.Mutex
	activeLiveDisp *LiveDisplay
)

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
	hostnameWidth := computeHostnameWidth(hosts)

	lines := make([]*HostLine, len(hosts))
	for i, host := range hosts {
		lines[i] = &HostLine{
			hostname:      host,
			hostnameWidth: hostnameWidth,
			overallEmoji:  "⏳",
			liveMode:      liveMode,
			out:           out,
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
// Only one LiveDisplay may be in live mode at a time: if another display is
// already active, Start falls back to plain-text mode so that the existing
// liveterm instance is not disturbed.
func (d *LiveDisplay) Start() {
	if !d.liveMode {
		return
	}

	// Try to claim the singleton slot. The lock is released before calling
	// liveterm.Start so the refresh goroutine can acquire it if needed.
	if !d.tryClaim() {
		// Another display is already running; fall back to plain text.
		d.liveMode = false
		for _, l := range d.lines {
			l.liveMode = false
		}
		return
	}

	liveterm.RefreshInterval = 100 * time.Millisecond
	liveterm.Output = d.out
	liveterm.SetMultiLinesUpdateFx(d.renderLines)
	if err := liveterm.Start(); err != nil {
		// If liveterm fails to start (e.g. no real TTY despite fd check), fall back.
		d.release()
		d.liveMode = false
		for _, l := range d.lines {
			l.liveMode = false
		}
	}
}

// tryClaim atomically claims the singleton slot. Returns true if successful.
func (d *LiveDisplay) tryClaim() bool {
	singletonMu.Lock()
	defer singletonMu.Unlock()
	if activeLiveDisp != nil {
		return false
	}
	activeLiveDisp = d
	return true
}

// release clears the singleton slot if this display holds it.
func (d *LiveDisplay) release() {
	singletonMu.Lock()
	defer singletonMu.Unlock()
	if activeLiveDisp == d {
		activeLiveDisp = nil
	}
}

// Stop finalises live output. In fallback mode this is a no-op.
func (d *LiveDisplay) Stop() {
	if !d.liveMode {
		return
	}
	defer d.release()
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

func computeHostnameWidth(hosts []string) int {
	maxLen := 0
	for _, host := range hosts {
		hostLen := len([]rune(host))
		if hostLen > maxLen {
			maxLen = hostLen
		}
	}
	if maxLen < minHostnameWidth {
		return minHostnameWidth
	}
	if maxLen > maxHostnameWidth {
		return maxHostnameWidth
	}
	return maxLen
}

func formatHostname(hostname string, width int) string {
	if width <= 0 {
		return hostname
	}
	runes := []rune(hostname)
	if len(runes) > width {
		if width == 1 {
			return "…"
		}
		hostname = string(runes[:width-1]) + "…"
	}
	return fmt.Sprintf("%-*s", width, hostname)
}
