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

// Constants for hostname column width limits to keep display aligned and prevent overflow.
const (
	minHostnameWidth = 10
	maxHostnameWidth = 20
)

// singleton guard: only one LiveDisplay may drive liveterm at a time.
var (
	singletonMu    sync.Mutex
	activeLiveDisp *LiveDisplay
	startLiveTerm  = liveterm.Start
)

// InitOptions configures display initialization parameters.
type InitOptions struct {
	// Debug indicates whether --debug is enabled. Debug has priority and disables live mode.
	Debug bool
	// PreferLiveMode indicates whether live rendering is preferred when possible.
	PreferLiveMode bool
	// Concurrent enables ordered buffering when live mode is off.
	Concurrent bool
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

// completedStep records an already-finished step with its emoticon.
type completedStep struct {
	emoji string
}

// logWriter wraps a writer and tracks whether any write has occurred.
// On the first write it emits a LOGS header line so
// the log stream is visually labelled. renderLines uses HasWritten to decide
// whether to add a blank separator line between the log stream and the host status block.
type logWriter struct {
	mu         sync.Mutex
	base       io.Writer
	outFd      int // file descriptor for terminal width queries; -1 for no TTY
	hasWritten bool
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.hasWritten {
		header := []byte("── LOGS\n")
		n, err := w.base.Write(header)
		if err != nil {
			return n, err
		}
		if n != len(header) {
			return n, io.ErrShortWrite
		}
		w.hasWritten = true
	}

	return w.base.Write(p)
}

func (w *logWriter) HasWritten() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hasWritten
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

// New creates and initializes a LiveDisplay.
// It applies display mode policy, configures concurrency behavior, and starts
// live rendering when applicable.
func New(out io.Writer, hosts []string, opts InitOptions) *LiveDisplay {
	isTTY := false
	outFd := -1
	if f, ok := out.(*os.File); ok {
		fd := int(f.Fd())
		if term.IsTerminal(fd) {
			isTTY = true
			outFd = fd
		}
	}
	hostnameWidth := computeHostnameWidth(hosts)

	d := &LiveDisplay{
		lines: make([]*HostLine, len(hosts)),
		isTTY: isTTY,
		outFd: outFd,
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

	// Finalize live and concurrency flags first, then reconcile buffering once so
	// initialization does not perform redundant setup passes.
	if d.debug {
		d.setLiveMode(false)
	} else {
		d.setLiveMode(opts.PreferLiveMode && d.isTTY)
	}
	d.concurrent = opts.Concurrent
	d.applyConcurrencyBuffering()
	d.initLiveMode()
	d.initLiveLogWriter()

	return d
}

// LiveDisplay manages per-host live output lines.
type LiveDisplay struct {
	lines []*HostLine
	out   io.Writer

	// outFd is the file descriptor of out when it is a queryable TTY; -1 otherwise.
	// Used to query terminal width dynamically for separator lines.
	outFd int

	// logWriter tracks whether any log output has been written via LogWriter.
	// Used by renderLines to decide whether to prepend a blank separator line.
	logWriter *logWriter

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

// Line returns the HostLine for the i-th host (0-indexed).
func (d *LiveDisplay) Line(i int) *HostLine {
	return d.lines[i]
}

// LogWriter returns a writer safe to use for permanent log output while the
// live display is active. The returned writer tracks whether anything has been
// written so that renderLines can insert a blank separator line between the log
// stream and the host status block.
func (d *LiveDisplay) LogWriter() io.Writer {
	if !d.liveMode {
		return nil
	}
	return d.logWriter
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
	d.pendingLines = nil
	for _, l := range d.lines {
		l.pending = nil
	}
}

// initLiveMode begins live output. In fallback mode this is a no-op.
// Only one LiveDisplay may be in live mode at a time: if another display is
// already active, initLiveMode falls back to plain-text mode so that the existing
// liveterm instance is not disturbed.
func (d *LiveDisplay) initLiveMode() {
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
	if err := startLiveTerm(); err != nil {
		// If liveterm fails to start (e.g. no real TTY despite fd check), fall back.
		d.release()
		d.setLiveMode(false)
		if d.concurrent {
			d.initNonLive()
		}
	}
}

// initLiveLogWriter eagerly initializes the live log writer.
// It is a no-op when live mode is disabled.
func (d *LiveDisplay) initLiveLogWriter() {
	if !d.liveMode {
		return
	}
	if d.logWriter == nil {
		d.logWriter = &logWriter{base: liveterm.Bypass(), outFd: d.outFd}
	}
}

// initNonLive allocates d.pendingLines and wires each HostLine.pending to it.
// Called during initial construction when the display starts in non-live mode,
// and whenever a live-mode display transitions to non-live mode via initLiveMode's
// fallback paths (singleton contention or liveterm.Start failure).
func (d *LiveDisplay) initNonLive() {
	d.pendingLines = make([]string, len(d.lines))
	for _, l := range d.lines {
		l.pending = &d.pendingLines
	}
}

// release clears the singleton slot if this display holds it.
func (d *LiveDisplay) release() {
	singletonMu.Lock()
	defer singletonMu.Unlock()
	if activeLiveDisp == d {
		activeLiveDisp = nil
	}
}

// renderLines returns all host lines for liveterm to display.
// This implementation atomically captures the state of all lines to prevent race
// conditions where intermediate states (e.g., ⏳ Connecting…) are visible alongside
// final states (e.g., ✅ or ❓). By acquiring all locks in order before rendering,
// we ensure the snapshot is consistent and matches the moment in time it was taken.
// WARNING: removing the locks or changing the locking strategy may cause race conditions
// WARNING: where intermediate states are rendered alongside final states, leading to confusing output.
// WARNING: Do not modify without careful consideration.
func (d *LiveDisplay) renderLines() []string {
	// Acquire all locks in index order to prevent deadlock and capture an atomic snapshot.
	for _, l := range d.lines {
		l.mu.Lock()
	}
	defer func() {
		// Release all locks in reverse order to maintain consistency.
		for i := len(d.lines) - 1; i >= 0; i-- {
			d.lines[i].mu.Unlock()
		}
	}()

	// Now render all lines while holding all locks; no state can change during this render.
	// Prepend a blank separator line when logs have been written, so the host status
	// block is visually distinct from the bypass log stream above it.
	// Allocate enough capacity for all lines plus the optional separator up front
	// to reduce append reallocations and avoid extra allocations in this hot refresh path.
	var out = make([]string, 0, len(d.lines)+2)
	if d.logWriter != nil && d.logWriter.HasWritten() {
		out = append(out, "")
	}
	out = append(out, separatorLine("HOSTS STATUS", termWidth(d.outFd)))
	for _, l := range d.lines {
		out = append(out, l.renderUnlocked())
	}
	return out
}

// setLiveMode sets the liveMode flag and updates all HostLines accordingly.
func (d *LiveDisplay) setLiveMode(enabled bool) {
	d.liveMode = enabled
	for _, l := range d.lines {
		l.liveMode = enabled
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

// termWidth returns the current terminal width for the given file descriptor.
// Returns 80 if fd is negative or the terminal size cannot be determined.
func termWidth(fd int) int {
	if fd < 0 {
		return 80
	}
	width, _, err := term.GetSize(fd)
	if err != nil || width <= 0 {
		return 80
	}
	return width
}

// separatorLine builds a separator of the form "── LABEL ──────...".
// When width is large enough, the returned line is exactly width terminal
// columns wide. If width is too small to accommodate the label section, it
// returns the minimal "── LABEL" form and may therefore exceed width.
func separatorLine(label string, width int) string {
	base := "── " + label
	baseCols := 3 + len([]rune(label))
	trailing := width - baseCols - 1 // account for the space before trailing dashes
	if trailing <= 0 {
		return base
	}
	return base + " " + strings.Repeat("─", trailing)
}

// Helper to compute hostname column width based on the longest hostname, with min and max bounds.
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

// helper to format hostname with fixed width and truncation if needed, to keep display aligned.
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
