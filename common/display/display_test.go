package display

import (
	"bytes"
	"fmt"
	"io"
	"os"
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

func TestPreferLiveFallsBackWhenTTYUnavailable(t *testing.T) {
	var buf bytes.Buffer
	d := New(&buf, []string{"router1"}, InitOptions{Debug: false, PreferLiveMode: true, Concurrent: true})

	if d.liveMode {
		t.Fatal("expected live mode to be disabled when output is not a TTY")
	}
	if d.pendingLines == nil || len(d.pendingLines) != 1 {
		t.Fatalf("expected pending lines buffer of size 1 in concurrent non-live mode, got %#v", d.pendingLines)
	}
}

func TestPreferLiveEnablesLiveModeWhenTTYAvailable(t *testing.T) {
	var buf bytes.Buffer
	d := New(&buf, []string{"router1", "router2"}, InitOptions{Debug: false, PreferLiveMode: true, Concurrent: true})

	d.isTTY = true
	d.debug = false
	d.setLiveMode(true)

	if !d.liveMode {
		t.Fatal("expected live mode to be enabled when PreferLiveMode is true and output is a TTY")
	}
	for i, l := range d.lines {
		if !l.liveMode {
			t.Fatalf("expected line %d to also be in live mode", i)
		}
	}
}
func TestBufferedPreferenceForcesConcurrentBuffering(t *testing.T) {
	var buf bytes.Buffer
	d := New(&buf, []string{"router1", "router2"}, InitOptions{Debug: false, PreferLiveMode: false, Concurrent: true})

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

func TestFinishWithErrorFallback(t *testing.T) {
	var buf bytes.Buffer
	d := newTestDisplay(&buf, []string{"badrouter.example.com"})
	l := d.Line(0)

	l.UpdateStep("⏳", "connecting…")
	l.CompleteStep("⏳")
	l.Finish("❌", "updates failed: ssh: connect: timeout")

	// In non-concurrent non-live mode, Finish writes immediately to out.
	output := buf.String()
	if !strings.Contains(output, "❌") {
		t.Errorf("Finish(❌, ...) output = %q, want ❌", output)
	}
	if !strings.Contains(output, "badrouter.example.c…") {
		t.Errorf("Finish(❌, ...) output = %q, want truncated hostname", output)
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
				l.Finish("❌", "updates failed: timeout")
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
	second.initLiveMode() // should detect activeLiveDisp != nil and fall back

	if second.liveMode {
		t.Error("second initLiveMode() should have fallen back to plain text when a display is already active")
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

// TestConcurrentFailureRenderAtomicity verifies that when hosts complete concurrently
// with mixed success and failures, renderLines() produces an atomic snapshot without
// intermediate states (e.g., "⏳ Connecting…") bleeding through into the output alongside
// final states (e.g., "❓ failed to dial"). This test reproduces the bug scenario reported
// in the issue where intermediate progress lines appeared mixed with final error messages.
func TestConcurrentFailureRenderAtomicity(t *testing.T) {
	hosts := []string{"router1", "router30", "router31", "router32", "router70", "router71", "router90"}
	var buf bytes.Buffer

	// Create a live-mode display (simulated; not actually running liveterm).
	d := newLiveModeDisplay(&buf, hosts)

	var wg sync.WaitGroup

	// Simulate concurrent host processing: some succeed, router32 and router70 fail
	failingHosts := map[string]bool{"router32": true, "router70": true}

	for i, host := range hosts {
		wg.Add(1)
		go func(idx int, hostname string) {
			defer wg.Done()
			line := d.Line(idx)
			cb := NewStepCallback(line)

			// All hosts go through "Connecting" phase
			cb("⏳", "Connecting to router…")

			// Simulate varying network delays by spinning briefly
			for j := 0; j < 1000; j++ {
				_ = d.renderLines() // Rapidly call renderLines to increase contention
			}

			// Some hosts fail, others succeed. This matches the actual processHost flow:
			// 1. CompleteStep(emoji) adds to history
			// 2. Finish(emoji, msg) sets overall emoji and marks done
			if failingHosts[hostname] {
				// Failed host reports error after "Connecting" step
				cb("❓", "failed to dial: "+hostname+".home:22: dial tcp: lookup "+hostname+".home: no such host")
				line.Finish("❓", "failed to dial: "+hostname+".home:22: dial tcp: lookup "+hostname+".home: no such host")
			} else {
				// Successful host reports completion
				cb("✅", "is up-to-date (RouterOS: 7.22.1, RouterBoard: 7.22.1)")
				line.Finish("✅", "is up-to-date (RouterOS: 7.22.1, RouterBoard: 7.22.1)")
			}
		}(i, host)
	}

	wg.Wait()

	// After all goroutines complete, verify renderLines() produces a consistent snapshot.
	// The snapshot contains: optional blank line, delimiter, then one line per host.
	// We only inspect host lines (starting at offset 1 for delimiter, no blank line since no logs).
	snapshot := d.renderLines()

	// Without logs, snapshot = [delimiter, host0, host1, ...]
	offset := 1 // skip delimiter
	if len(snapshot) != len(hosts)+offset {
		t.Fatalf("expected %d snapshot lines (delimiter + %d hosts), got %d: %v",
			len(hosts)+offset, len(hosts), len(snapshot), snapshot)
	}

	for i, host := range hosts {
		line := snapshot[i+offset]
		t.Logf("Final snapshot for %s: %s", host, line)

		// Every line should start with a final emoji, not intermediate
		if !strings.HasPrefix(line, "✅") && !strings.HasPrefix(line, "❓") {
			t.Errorf("snapshot[%d] for %q = %q, expected final emoji (✅ or ❓), not intermediate state",
				i+offset, host, line)
		}

		// No intermediate "Connecting" text should appear
		if strings.Contains(line, "Connecting to router…") {
			t.Errorf("snapshot[%d] for %q contains intermediate step 'Connecting to router…', should be final only: %q",
				i+offset, host, line)
		}

		// Verify expected final states
		if failingHosts[host] {
			if !strings.HasPrefix(line, "❓") {
				t.Errorf("snapshot[%d] for %q = %q, expected ❓ (unknown/failed status)", i+offset, host, line)
			}
			if !strings.Contains(line, "failed to dial") {
				t.Errorf("snapshot[%d] for %q = %q, expected failure message", i+offset, host, line)
			}
		} else {
			if !strings.HasPrefix(line, "✅") {
				t.Errorf("snapshot[%d] for %q = %q, expected ✅ (success)", i+offset, host, line)
			}
			if !strings.Contains(line, "is up-to-date") {
				t.Errorf("snapshot[%d] for %q = %q, expected success message", i+offset, host, line)
			}
		}
	}
}

// TestRenderLinesLocksAllHosts verifies that renderLines() correctly acquires and
// releases locks on all hostlines, preventing race conditions during snapshot capture.
// This test catches regressions if renderLines() ever reverts to acquiring locks
// individually per-line instead of all-at-once.
func TestRenderLinesLocksAllHosts(t *testing.T) {
	hosts := []string{"host1", "host2", "host3"}
	d := newLiveModeDisplay(new(bytes.Buffer), hosts)

	// Set up each line with different states to verify they're all captured atomically
	d.Line(0).UpdateStep("⏳", "step1")
	d.Line(1).CompleteStep("✅")
	d.Line(2).UpdateStep("⏳", "step2")

	// Without logs: delimiter + 3 hosts = 4 lines.
	// Call renderLines multiple times: should always see the same state
	// (deadlock would manifest as goroutine hanging, race detector would catch non-atomic reads)
	for i := 0; i < 100; i++ {
		snapshot := d.renderLines()
		if len(snapshot) != 4 {
			t.Errorf("iteration %d: renderLines returned %d lines, want 4 (delimiter + 3 hosts)", i, len(snapshot))
		}
		if !strings.Contains(snapshot[0], "HOSTS STATUS") {
			t.Errorf("iteration %d: snapshot[0] should be delimiter, got %q", i, snapshot[0])
		}
		if !strings.Contains(snapshot[1], "step1") {
			t.Errorf("iteration %d: snapshot[1] lost 'step1', got %q", i, snapshot[1])
		}
	}
}

// TestRenderLinesDelimiterAlwaysPresent verifies the HOSTS STATUS delimiter is always
// the first output line, regardless of whether any logs have been written.
func TestRenderLinesDelimiterAlwaysPresent(t *testing.T) {
	d := newLiveModeDisplay(new(bytes.Buffer), []string{"host1", "host2"})

	snapshot := d.renderLines()

	if len(snapshot) < 1 {
		t.Fatalf("expected at least 1 line, got 0")
	}
	if !strings.Contains(snapshot[0], "HOSTS STATUS") {
		t.Errorf("delimiter should be first when no logs written, got: %q", snapshot[0])
	}
}

// TestRenderLinesBlankLineWhenLogsWritten verifies that a blank separator line is
// prepended before the HOSTS STATUS delimiter when logs have been written.
func TestRenderLinesBlankLineWhenLogsWritten(t *testing.T) {
	d := newLiveModeDisplay(new(bytes.Buffer), []string{"host1", "host2"})
	d.Line(0).UpdateStep("⏳", "working")

	// Simulate log writes by wiring a logWriter that has already written.
	d.logWriter = &logWriterWithSeparator{base: io.Discard, hasWritten: true}

	snapshot := d.renderLines()

	// blank + delimiter + 2 host lines = 4 lines
	if len(snapshot) != 4 {
		t.Fatalf("with logs: expected 4 lines, got %d: %v", len(snapshot), snapshot)
	}
	if snapshot[0] != "" {
		t.Errorf("with logs: first line should be blank, got: %q", snapshot[0])
	}
	if !strings.Contains(snapshot[1], "HOSTS STATUS") {
		t.Errorf("with logs: second line should be delimiter, got: %q", snapshot[1])
	}
	if !strings.Contains(snapshot[2], "host1") {
		t.Errorf("with logs: host1 should be at index 2, got: %q", snapshot[2])
	}
}

// TestRenderLinesNoBlankLineWithoutLogs verifies that no blank line is added
// before the HOSTS STATUS delimiter when no logs have been written.
func TestRenderLinesNoBlankLineWithoutLogs(t *testing.T) {
	d := newLiveModeDisplay(new(bytes.Buffer), []string{"host1", "host2"})
	d.Line(0).UpdateStep("⏳", "working")

	// No logWriter set — simulates a run with no log output.
	snapshot := d.renderLines()

	// delimiter + 2 host lines = 3 lines
	if len(snapshot) != 3 {
		t.Fatalf("without logs: expected 3 lines, got %d: %v", len(snapshot), snapshot)
	}
	if snapshot[0] == "" {
		t.Errorf("without logs: first line should NOT be blank")
	}
	if !strings.Contains(snapshot[0], "HOSTS STATUS") {
		t.Errorf("without logs: first line should be delimiter, got: %q", snapshot[0])
	}
	if !strings.Contains(snapshot[1], "host1") {
		t.Errorf("without logs: host1 should be at index 1, got: %q", snapshot[1])
	}
}

// TestLogWriterWithSeparatorTracksWrites verifies HasWritten reflects write state
// and that the LOGS header is emitted exactly once before the first log line.
func TestLogWriterWithSeparatorTracksWrites(t *testing.T) {
	var buf bytes.Buffer
	// outFd: -1 → termWidth returns 80 (no TTY fallback)
	w := &logWriterWithSeparator{base: &buf, outFd: -1}

	if w.HasWritten() {
		t.Errorf("expected HasWritten=false initially, got true")
	}

	if _, err := w.Write([]byte("test log")); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	if !w.HasWritten() {
		t.Errorf("expected HasWritten=true after Write(), got false")
	}

	// The LOGS header must appear before the log content.
	wantHeader := "── LOGS\n"
	want := wantHeader + "test log"
	if buf.String() != want {
		t.Errorf("expected buffer %q, got: %q", want, buf.String())
	}

	// A second write must not repeat the header.
	if _, err := w.Write([]byte(" second")); err != nil {
		t.Fatalf("second Write() error: %v", err)
	}
	if buf.String() != want+" second" {
		t.Errorf("header should appear only once, got: %q", buf.String())
	}
}

// TestLogWriterWithSeparatorThreadSafe verifies concurrent writes are tracked safely
// and that the LOGS header appears exactly once regardless of concurrency.
func TestLogWriterWithSeparatorThreadSafe(t *testing.T) {
	var buf bytes.Buffer
	var bufMu sync.Mutex
	// outFd: -1 → termWidth returns 80 (no TTY fallback)
	w := &logWriterWithSeparator{base: &lockedWriter{mu: &bufMu, w: &buf}, outFd: -1}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w.Write([]byte("log\n")) //nolint:errcheck
		}(i)
	}
	wg.Wait()

	if !w.HasWritten() {
		t.Errorf("expected HasWritten=true after concurrent writes")
	}

	got := buf.String()
	if got == "" {
		t.Errorf("expected data in buffer after concurrent writes")
	}
	// Header must appear exactly once at the very beginning.
	wantHeader := "── LOGS\n"
	if !strings.HasPrefix(got, wantHeader) {
		t.Errorf("expected buffer to start with LOGS header, got: %q", got[:min(len(got), 60)])
	}
	if strings.Count(got, wantHeader) != 1 {
		t.Errorf("LOGS header should appear exactly once, got %d occurrences", strings.Count(got, wantHeader))
	}
}

// TestSeparatorLine verifies separatorLine builds correctly-sized strings.
func TestSeparatorLine(t *testing.T) {
	tests := []struct {
		label        string
		width        int
		wantContains string
		wantLen      int // expected rune length of output
	}{
		{label: "LOGS", width: 80, wantContains: "LOGS", wantLen: 80},
		{label: "HOSTS STATUS", width: 80, wantContains: "HOSTS STATUS", wantLen: 80},
		{label: "LOGS", width: 40, wantContains: "LOGS", wantLen: 40},
		// Width too small: trailing dashes are omitted, label still present.
		{label: "LOGS", width: 5, wantContains: "LOGS", wantLen: 7}, // "── LOGS" = 7 runes
		{label: "LOGS", width: 0, wantContains: "LOGS", wantLen: 7},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/w=%d", tt.label, tt.width), func(t *testing.T) {
			got := separatorLine(tt.label, tt.width)
			if !strings.Contains(got, tt.label) {
				t.Errorf("separatorLine(%q, %d) = %q: label missing", tt.label, tt.width, got)
			}
			if gotLen := len([]rune(got)); gotLen != tt.wantLen {
				t.Errorf("separatorLine(%q, %d) rune length = %d, want %d: %q",
					tt.label, tt.width, gotLen, tt.wantLen, got)
			}
		})
	}
}

// TestTermWidth verifies termWidth returns 80 for non-TTY file descriptors.
func TestTermWidth(t *testing.T) {
	// fd=-1 is the explicit no-TTY sentinel.
	if got := termWidth(-1); got != 80 {
		t.Errorf("termWidth(-1) = %d, want 80", got)
	}

	// A pipe read-end is never a TTY; GetSize will fail → fallback to 80.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	defer r.Close() //nolint:errcheck
	defer w.Close() //nolint:errcheck
	if got := termWidth(int(r.Fd())); got != 80 {
		t.Errorf("termWidth(pipe fd) = %d, want 80 (pipe is not a TTY)", got)
	}
}

// lockedWriter is a helper that serialises writes to an underlying writer using an external mutex.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}
