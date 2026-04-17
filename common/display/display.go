package display

import (
	"fmt"
	"io"
	"log/slog"
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

// Mode controls how concurrent output is rendered.
type Mode string

const (
	ModeAuto     Mode = "auto"
	ModeBuffered Mode = "buffered"
)

// InitOptions configures display initialization parameters.
type InitOptions struct {
	// Debug indicates whether --debug is enabled. Debug has priority and disables live mode.
	Debug bool
	// Mode controls the display behavior (auto or buffered).
	Mode Mode
	// Concurrent enables ordered buffering when live mode is off.
	Concurrent bool
}

// ParseMode parses a user-provided mode value.
func ParseMode(value string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		return ModeAuto, nil
	}
	switch mode {
	case ModeAuto, ModeBuffered:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid display mode %q: must be auto or buffered", value)
	}
}

// completedStep records an already-finished step with its emoticon.
type completedStep struct {
	emoji string
}

// HostLine tracks the display state for a single host.
type HostLine struct {
	mu            sync.Mutex
	index         int       // position in the parent LiveDisplay.pendingLines slice
	pending       *[]string // points to LiveDisplay.pendingLines; nil when not buffering (sequential non-live)
	out           io.Writer // used for immediate writes in sequential non-live mode
	hostname      string
	hostnameWidth int
	overallEmoji  string // ⏳ while in-progress; set to the final status emoji by Finish
	history       []completedStep
	currentEmoji  string
	currentLabel  string
	done          bool
	finalMessage  string // set by Finish; does not include the hostname or overall emoji
	liveMode      bool
}

// StepCallback is used to update step display for a host.
type StepCallback func(emoji string, message string)

// NewStepCallback returns a StepCallback that sends terminal status emojis
// (✅, ❌, ❓, ⚠️) to CompleteStep and treats all other emojis as in-progress
// updates via UpdateStep.
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
// In concurrent non-live mode the rendered line is buffered and written to out in
// host-list order when LiveDisplay.Stop is called. In sequential non-live mode the
// line is written immediately so the caller sees progress in real time.
func (h *HostLine) Finish(overallEmoji, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.done = true
	h.overallEmoji = overallEmoji
	h.finalMessage = message
	if !h.liveMode {
		if h.pending != nil {
			// concurrent mode: buffer for ordered flush in Stop
			(*h.pending)[h.index] = h.renderUnlocked()
		} else {
			// sequential mode: write immediately for real-time feedback
			if _, err := fmt.Fprintf(h.out, "%s\n", h.renderUnlocked()); err != nil {
				slog.Warn("display: failed to write host output", "host", h.hostname, "error", err)
			}
		}
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
			return fmt.Sprintf("%s %s %s", h.overallEmoji, hostname, h.finalMessage)
		}
		return fmt.Sprintf("%s %s %s %s", h.overallEmoji, hostname, summary, h.finalMessage)
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
		return fmt.Sprintf("%s %s %s", h.overallEmoji, hostname, current)
	}
	if current == "" {
		return fmt.Sprintf("%s %s %s", h.overallEmoji, hostname, summary)
	}
	return fmt.Sprintf("%s %s %s %s", h.overallEmoji, hostname, summary, current)
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
	lines []*HostLine
	out   io.Writer

	// liveMode is the effective runtime state after evaluating display mode,
	// debug, TTY, and any live startup fallback (for example singleton contention).
	liveMode bool

	// isTTY is detected once at construction time and does not change.
	isTTY bool
	// debug indicates whether --debug is enabled. Debug has priority and disables live mode.
	debug bool
	// concurrent enables ordered buffering when live mode is off.
	concurrent bool

	// pendingLines holds one rendered final line per host in concurrent non-live mode.
	pendingLines []string
}

// New creates and initializes a LiveDisplay.
// It applies display mode policy, configures concurrency behavior, and starts
// live rendering when applicable.
func New(out io.Writer, hosts []string, opts InitOptions) *LiveDisplay {
	isTTY := false
	if f, ok := out.(*os.File); ok {
		isTTY = term.IsTerminal(int(f.Fd()))
	}
	hostnameWidth := computeHostnameWidth(hosts)

	d := &LiveDisplay{
		lines: make([]*HostLine, len(hosts)),
		isTTY: isTTY,
		debug: opts.Debug,
		out:   out,
	}

	for i, host := range hosts {
		d.lines[i] = &HostLine{
			hostname:      host,
			hostnameWidth: hostnameWidth,
			overallEmoji:  "⏳",
			index:         i,
			out:           out,
		}
	}

	// Centralize mode and concurrency setup in one place so constructor and
	// runtime behavior match.
	d.applyMode(opts.Mode)
	d.setConcurrent(opts.Concurrent)
	d.start()

	return d
}

// setConcurrent marks the display as serving concurrent host processing.
func (d *LiveDisplay) setConcurrent(concurrent bool) {
	d.concurrent = concurrent
	d.applyConcurrencyBuffering()
}

func (d *LiveDisplay) applyMode(mode Mode) {
	// Debug flag has priority: if --debug is enabled, always disable live mode
	// to keep output clean and deterministic for log analysis.
	if d.debug {
		d.setLiveMode(false)
	} else {
		var shouldLive bool

		switch mode {
		case ModeBuffered:
			// Explicitly buffered: never use live updates
			shouldLive = false
		case ModeAuto:
			shouldLive = d.isTTY
		default:
			// Unknown/zero values are treated like auto for defensive behavior.
			shouldLive = d.isTTY
		}

		d.setLiveMode(shouldLive)
	}

	d.applyConcurrencyBuffering()
}

// applyConcurrencyBuffering configures whether host lines are buffered until
// Stop based on current concurrency + effective live mode.
func (d *LiveDisplay) applyConcurrencyBuffering() {
	// Concurrent runs in non-live mode are buffered so Stop can emit
	// deterministic host-list ordering for non-interactive outputs.
	if d.concurrent && !d.liveMode {
		if d.pendingLines == nil {
			d.initNonLive()
		}
		return
	}
	d.clearNonLive()
}

func (d *LiveDisplay) setLiveMode(enabled bool) {
	d.liveMode = enabled
	for _, l := range d.lines {
		l.liveMode = enabled
	}
}

// clearNonLive removes any non-live buffering state so sequential mode writes
// each line immediately instead of buffering for Stop.
func (d *LiveDisplay) clearNonLive() {
	d.pendingLines = nil
	for _, l := range d.lines {
		l.pending = nil
	}
}

// initNonLive allocates d.pendingLines and wires each HostLine.pending to it.
// Called during initial construction when the display starts in non-live mode,
// and whenever a live-mode display transitions to non-live mode via Start's
// fallback paths (singleton contention or liveterm.Start failure).
func (d *LiveDisplay) initNonLive() {
	d.pendingLines = make([]string, len(d.lines))
	for _, l := range d.lines {
		l.pending = &d.pendingLines
	}
}

// Line returns the HostLine for the i-th host (0-indexed).
func (d *LiveDisplay) Line(i int) *HostLine {
	return d.lines[i]
}

// start begins live output. In fallback mode this is a no-op.
// Only one LiveDisplay may be in live mode at a time: if another display is
// already active, start falls back to plain-text mode so that the existing
// liveterm instance is not disturbed.
func (d *LiveDisplay) start() {
	if !d.liveMode {
		return
	}

	// Try to claim the singleton slot. The lock is released before calling
	// liveterm.Start so the refresh goroutine can acquire it if needed.
	if !d.tryClaim() {
		// Another display is already running; fall back to plain text.
		d.setLiveMode(false)
		if d.concurrent {
			d.initNonLive()
		}
		return
	}

	liveterm.RefreshInterval = 100 * time.Millisecond
	liveterm.Output = d.out
	liveterm.SetMultiLinesUpdateFx(d.renderLines)
	if err := liveterm.Start(); err != nil {
		// If liveterm fails to start (e.g. no real TTY despite fd check), fall back.
		d.release()
		d.setLiveMode(false)
		if d.concurrent {
			d.initNonLive()
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

// Stop finalises output. In live mode it stops liveterm. In concurrent non-live
// mode it flushes all buffered host lines to out in host-list order, so that
// goroutines that finish at different times still produce deterministic output.
func (d *LiveDisplay) Stop() {
	if d.liveMode {
		defer d.release()
		if err := liveterm.Stop(false); err != nil {
			slog.Warn("display: failed to stop live terminal", "error", err)
		}
		return
	}
	for i, line := range d.pendingLines {
		if line == "" {
			continue
		}
		if _, err := fmt.Fprintf(d.out, "%s\n", line); err != nil {
			slog.Warn("display: failed to write host line", "error", err, "line_index", i, "hostname", d.lines[i].hostname)
		}
	}
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
