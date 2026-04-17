package display

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// newTestDisplay creates a LiveDisplay in non-TTY/debug fallback mode for testing.
func newTestDisplay(out *bytes.Buffer, hosts []string) *LiveDisplay {
	return New(out, hosts, InitOptions{Debug: true, PreferLiveMode: true, Concurrent: false})
}

// newLiveModeDisplay creates a LiveDisplay that believes it is in live mode,
// without actually starting liveterm (for singleton guard testing).
func newLiveModeDisplay(out *bytes.Buffer, hosts []string) *LiveDisplay {
	d := New(out, hosts, InitOptions{Debug: true, PreferLiveMode: true, Concurrent: false}) // starts in fallback
	d.liveMode = true                                                                       // pretend it is live-capable
	d.isTTY = true
	d.debug = false
	for _, l := range d.lines {
		l.liveMode = true
	}
	return d
}

func TestPreferLiveKeepsLiveWhenCapable(t *testing.T) {
	var buf bytes.Buffer
	d := New(&buf, []string{"router1"}, InitOptions{Debug: false, PreferLiveMode: true, Concurrent: true})
	d.isTTY = true
	d.applyMode(true)

	if !d.liveMode {
		t.Fatal("expected live mode to stay enabled in concurrent auto mode on a live-capable terminal")
	}
	if d.pendingLines != nil {
		t.Fatalf("expected no buffered pending lines in concurrent auto live mode, got %#v", d.pendingLines)
	}
}

func TestBufferedPreferenceForcesConcurrentBuffering(t *testing.T) {
	var buf bytes.Buffer
	d := New(&buf, []string{"router1", "router2"}, InitOptions{Debug: false, PreferLiveMode: false, Concurrent: true})
	d.isTTY = true
	d.applyMode(false)

	if d.liveMode {
		t.Fatal("expected live mode to be disabled in buffered mode")
	}
	if d.pendingLines == nil || len(d.pendingLines) != 2 {
		t.Fatalf("expected pending lines buffer of size 2, got %#v", d.pendingLines)
	}
}

func TestNewFallbackMode(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"router1", "router2"})
	if d.liveMode {
		t.Error("expected liveMode=false in debug/non-TTY mode")
	}
	if len(d.lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(d.lines))
	}
}

func TestStartStopFallback(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"router1"})
	// In fallback mode start is a no-op; Stop flushes buffered lines (none here).
	d.Stop()
}

func TestLineRendering(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"myrouter.example.com"})
	l := d.Line(0)

	// Initial render: no step, no current — should start with overall status emoji.
	got := l.render()
	if !strings.HasPrefix(got, "⏳") {
		t.Errorf("render() = %q, want ⏳ prefix (overall status)", got)
	}
	if !strings.Contains(got, "myrouter.example.com") {
		t.Errorf("render() = %q, want hostname present", got)
	}

	// After UpdateStep.
	l.UpdateStep("⏳", "connecting…")
	got = l.render()
	if !strings.Contains(got, "⏳") || !strings.Contains(got, "connecting…") {
		t.Errorf("render() after UpdateStep = %q, want ⏳ and 'connecting…'", got)
	}

	// After CompleteStep.
	l.CompleteStep("✅")
	got = l.render()
	if !strings.Contains(got, "✅") {
		t.Errorf("render() after CompleteStep = %q, want ✅ in history", got)
	}
	// Current step should be cleared.
	if strings.Contains(got, "connecting…") {
		t.Errorf("render() after CompleteStep = %q, should not contain old label", got)
	}
}

func TestFinishFallback(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"myrouter.example.com"})
	l := d.Line(0)

	l.UpdateStep("⏳", "connecting…")
	l.CompleteStep("✅")
	l.Finish("✅", "is up-to-date (RouterOS: 7.16)")

	// In non-concurrent non-live mode, Finish writes immediately to out.
	output := buf.String()
	if !strings.HasPrefix(output, "✅") {
		t.Errorf("Finish() output = %q, want ✅ overall status at start", output)
	}
	if !strings.Contains(output, "myrouter.example.com") {
		t.Errorf("Finish() output = %q, want hostname present", output)
	}
	if !strings.Contains(output, "is up-to-date (RouterOS: 7.16)") {
		t.Errorf("Finish() output = %q, want final message present", output)
	}
	d.Stop() // no-op for sequential non-live mode

	// render() after Finish should produce the same line.
	got := l.render()
	if !strings.HasPrefix(got, "✅") {
		t.Errorf("render() after Finish = %q, want ✅ at start", got)
	}
	if !strings.Contains(got, "is up-to-date (RouterOS: 7.16)") {
		t.Errorf("render() after Finish = %q, want finalMessage", got)
	}
}

func TestFinishErrorFallback(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"badrouter.example.com"})
	l := d.Line(0)

	l.UpdateStep("⏳", "connecting…")
	l.CompleteStep("⏳")
	l.FinishError("updates failed: ssh: connect: timeout")

	// In non-concurrent non-live mode, FinishError writes immediately to out.
	output := buf.String()
	if !strings.Contains(output, "❌") {
		t.Errorf("FinishError() output = %q, want ❌", output)
	}
	if !strings.Contains(output, "badrouter.example.c…") {
		t.Errorf("FinishError() output = %q, want truncated hostname", output)
	}
	d.Stop() // no-op for sequential non-live mode
}

func TestHostLineThreadSafety(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"router.example.com"})
	l := d.Line(0)

	var wg sync.WaitGroup
	const goroutines = 50

	for range goroutines {
		wg.Go(func() {
			l.UpdateStep("⏳", "working…")
			l.CompleteStep("✅")
			_ = l.render()
		})
	}
	wg.Wait()
	// If we reach here without a race, thread safety is confirmed.
}

func TestMultipleHosts(t *testing.T) {
	var buf bytes.Buffer
	hosts := []string{"host1.example.com", "host2.example.com", "host3.example.com"}
	d := newTestDisplay(&buf, hosts)

	for i, host := range hosts {
		l := d.Line(i)
		l.UpdateStep("⏳", "connecting…")
		l.CompleteStep("✅")
		l.Finish("✅", host+" is up-to-date")
	}

	d.Stop()
	output := buf.String()
	for _, host := range hosts {
		if !strings.Contains(output, host) {
			t.Errorf("output missing host %q: %s", host, output)
		}
	}
}

// TestOutputOrder verifies that Stop flushes host lines in host-list order even
// when goroutines call Finish out of order (simulating concurrent completion).
func TestOutputOrder(t *testing.T) {
	hosts := []string{"host1.example.com", "host2.example.com", "host3.example.com"}
	var buf bytes.Buffer
	d := New(&buf, hosts, InitOptions{Debug: false, PreferLiveMode: false, Concurrent: true})

	// Finish hosts in reverse order to simulate out-of-order concurrent completion.
	d.Line(2).Finish("✅", "host3 done")
	d.Line(0).Finish("✅", "host1 done")
	d.Line(1).Finish("✅", "host2 done")

	d.Stop()
	output := buf.String()

	// Output must list hosts in the original host-list order.
	idx1 := strings.Index(output, "host1.example.com")
	idx2 := strings.Index(output, "host2.example.com")
	idx3 := strings.Index(output, "host3.example.com")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Fatalf("output missing one or more hostnames: %q", output)
	}
	if idx1 >= idx2 || idx2 >= idx3 {
		t.Errorf("output not in host-list order: host1=%d host2=%d host3=%d\n%s",
			idx1, idx2, idx3, output)
	}
}

func TestHostnamePadding(t *testing.T) {
	var buf bytes.Buffer
	// Short hostname should be padded to the configured minimum width.
	d := newTestDisplay(&buf, []string{"r1"})
	l := d.Line(0)
	l.UpdateStep("⏳", "test")
	got := l.render()
	// The line should contain the hostname and be longer than the hostname itself
	// (emoji + space + padded hostname + separators + current step).
	if !strings.Contains(got, "r1") {
		t.Errorf("render() = %q, should contain hostname", got)
	}
	if !strings.HasPrefix(got, "⏳") {
		t.Errorf("render() = %q, should start with overall status emoji", got)
	}
}

func TestComputeHostnameWidthBounded(t *testing.T) {
	if got := computeHostnameWidth([]string{"r1", "r2"}); got != 10 {
		t.Errorf("computeHostnameWidth(short hosts) = %d, want 10", got)
	}

	if got := computeHostnameWidth([]string{"router-lab-01", "core-edge"}); got != 13 {
		t.Errorf("computeHostnameWidth(normal hosts) = %d, want 13", got)
	}

	if got := computeHostnameWidth([]string{"router-very-very-very-long-site-name-prod-001"}); got != 20 {
		t.Errorf("computeHostnameWidth(long hosts) = %d, want 20", got)
	}
}

func TestFormatHostnameTruncatesWithEllipsis(t *testing.T) {
	got := formatHostname("router-very-very-long-hostname", 20)
	if got != "router-very-very-lo…" {
		t.Errorf("formatHostname() = %q, want %q", got, "router-very-very-lo…")
	}
}

func TestRenderUsesTruncatedHostname(t *testing.T) {
	var buf bytes.Buffer
	host := "router-very-very-long-hostname"
	d := newTestDisplay(&buf, []string{host})
	l := d.Line(0)
	l.UpdateStep("⏳", "connecting…")

	got := l.render()
	if !strings.Contains(got, "router-very-very-lo…") {
		t.Errorf("render() = %q, expected truncated hostname", got)
	}
	if strings.Contains(got, host) {
		t.Errorf("render() = %q, should not contain full hostname when it exceeds max width", got)
	}
}

func TestStepSummaryOrder(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"router.example.com"})
	l := d.Line(0)

	l.UpdateStep("⏳", "step1")
	l.CompleteStep("✅")
	l.UpdateStep("⏳", "step2")
	l.CompleteStep("✅")
	l.UpdateStep("⏳", "step3")

	got := l.render()
	// Should contain both completed emojis and current step.
	idx1 := strings.Index(got, "✅")
	idx2 := strings.LastIndex(got, "✅")
	if idx1 == idx2 {
		t.Errorf("render() = %q, expected two ✅ in history", got)
	}
	if !strings.Contains(got, "step3") {
		t.Errorf("render() = %q, expected current step 'step3'", got)
	}
}

func TestNewStepCallbackCompletesTerminalStatuses(t *testing.T) {
	tests := []struct {
		name  string
		emoji string
	}{
		{name: "success", emoji: "✅"},
		{name: "failed", emoji: "❌"},
		{name: "unknown", emoji: "❓"},
		{name: "warning", emoji: "⚠️"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			d := newTestDisplay(&buf, []string{"router.example.com"})
			l := d.Line(0)
			cb := NewStepCallback(l)

			cb("⏳", "connecting…")
			cb(tt.emoji, "ignored")

			got := l.render()
			if !strings.Contains(got, tt.emoji) {
				t.Errorf("render() = %q, expected completed emoji %q", got, tt.emoji)
			}
			if strings.Contains(got, "connecting…") {
				t.Errorf("render() = %q, expected current step to be cleared", got)
			}
		})
	}
}

func TestNewStepCallbackKeepsInProgressStatus(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"router.example.com"})
	l := d.Line(0)
	cb := NewStepCallback(l)

	cb("⏳", "connecting…")

	got := l.render()
	if !strings.Contains(got, "⏳") || !strings.Contains(got, "connecting…") {
		t.Errorf("render() = %q, expected in-progress status and label", got)
	}
}

// TestRenderFormatConsistency verifies the line format is consistent between in-progress and done states.
// Both must follow: [overall status] <hostname> [step emojis] [step message]
func TestRenderFormatConsistency(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(l *HostLine)
		wantPrefix   string // overall status emoji
		wantContains []string
	}{
		{
			name: "in-progress connecting",
			setup: func(l *HostLine) {
				l.UpdateStep("⏳", "connecting…")
			},
			wantPrefix:   "⏳",
			wantContains: []string{"router.example.com", "⏳", "connecting…"},
		},
		{
			name: "done success",
			setup: func(l *HostLine) {
				l.CompleteStep("✅")
				l.Finish("✅", "is up-to-date (RouterOS: 7.16)")
			},
			wantPrefix:   "✅",
			wantContains: []string{"router.example.com", "✅", "is up-to-date (RouterOS: 7.16)"},
		},
		{
			name: "done error",
			setup: func(l *HostLine) {
				l.CompleteStep("❌")
				l.FinishError("updates failed: timeout")
			},
			wantPrefix:   "❌",
			wantContains: []string{"router.example.com", "❌", "updates failed: timeout"},
		},
		{
			name: "done unknown",
			setup: func(l *HostLine) {
				l.CompleteStep("❓")
				l.Finish("❓", "update applied, status unverified")
			},
			wantPrefix:   "❓",
			wantContains: []string{"router.example.com", "❓", "update applied, status unverified"},
		},
		{
			name: "done warning",
			setup: func(l *HostLine) {
				l.CompleteStep("⚠️")
				l.Finish("⚠️", "upgrade available (RouterOS: 7.14.0 → 7.14.1)")
			},
			wantPrefix:   "⚠️",
			wantContains: []string{"router.example.com", "⚠️", "upgrade available"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			d := newTestDisplay(&buf, []string{"router.example.com"})
			l := d.Line(0)
			tt.setup(l)
			got := l.render()
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("render() = %q, want prefix %q", got, tt.wantPrefix)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("render() = %q, missing %q", got, want)
				}
			}
		})
	}
}

// TestSingletonFallback verifies that a second LiveDisplay falls back to
// plain-text mode when another display already holds the singleton lock.
func TestSingletonFallback(t *testing.T) {
	resetSingleton(t)

	var buf1, buf2 bytes.Buffer

	// First display claims the singleton by setting activeLiveDisp directly
	// (we cannot actually start liveterm in a test environment with no TTY,
	// but the guard is checked before liveterm.Start, so we simulate an
	// already-active display by inserting it into the singleton slot).
	first := newLiveModeDisplay(&buf1, []string{"router1"})
	singletonMu.Lock()
	activeLiveDisp = first
	singletonMu.Unlock()

	// A second display in live mode should fall back to plain text.
	second := newLiveModeDisplay(&buf2, []string{"router2"})
	second.start() // should detect activeLiveDisp != nil and fall back

	if second.liveMode {
		t.Error("second start() should have fallen back to plain text when a display is already active")
	}
	for _, l := range second.lines {
		if l.liveMode {
			t.Error("HostLine.liveMode should be false after singleton fallback")
		}
	}

	// In plain-text fallback (non-concurrent), Finish writes immediately to out.
	second.Line(0).Finish("✅", "up-to-date")
	if !strings.Contains(buf2.String(), "router2") {
		t.Errorf("expected plain-text output for second display after fallback, got %q", buf2.String())
	}
	second.Stop() // no-op in non-concurrent non-live mode
}

// TestSingletonReleasedAfterStop verifies that after Stop the singleton slot
// is cleared, allowing a subsequent display to use live mode.
func TestSingletonReleasedAfterStop(t *testing.T) {
	resetSingleton(t)

	var buf bytes.Buffer
	first := newLiveModeDisplay(&buf, []string{"router1"})

	// Manually occupy + release the singleton (simulating a real Start+Stop
	// without needing a real TTY).
	singletonMu.Lock()
	activeLiveDisp = first
	singletonMu.Unlock()

	// Simulate Stop: releases singleton without calling liveterm.Stop (no TTY).
	first.release()

	// After release, singleton slot should be empty.
	singletonMu.Lock()
	current := activeLiveDisp
	singletonMu.Unlock()
	if current != nil {
		t.Error("singleton slot should be nil after Stop")
	}
}

// resetSingleton clears the singleton slot before and after a test.
func resetSingleton(t *testing.T) {
	t.Helper()
	singletonMu.Lock()
	activeLiveDisp = nil
	singletonMu.Unlock()
	t.Cleanup(func() {
		singletonMu.Lock()
		activeLiveDisp = nil
		singletonMu.Unlock()
	})
}
