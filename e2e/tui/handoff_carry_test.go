package tuie2e

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// What the daemon holds about a pane, a session or itself outside the
// session state, carried across a live handoff or re-armed after it
// (docs/design/live-handoff.md, section 7.3). Each test sets one up, hands the
// panes to a new image with test-handoff, and checks what a person or a
// program sees after.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// focusInReports counts the focus-in reports a cat -v pane printed.
func focusInReports(t *testing.T, base, sess, window string) int {
	t.Helper()
	lines, err := daemonScrollback(base, sess, window, 2000)
	if err != nil {
		t.Fatalf("capture the pane: %v", err)
	}
	return strings.Count(strings.Join(lines, "\n"), "^[[I")
}

// TestSelfExecKeepsFocusReporting hands over a pane whose program turned on
// focus reporting (DECSET 1004) and echoes its input through cat -v, with no
// client attached. A client then attaches to the new image, and reports the
// pane's focus as it does on every attach: the program hears one focus-in.
// Its echo is the pane's first output in the new image, and the daemon sends
// a focus-in of its own when a write finds the mode newly on, so an image
// that started the pane believing the mode off would send a second.
func TestSelfExecKeepsFocusReporting(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-focus"
	win := newCommandWindow(t, base, sess, "probe", false, "sh", "-c", `stty -icanon -echo min 1; printf '\033[?1004h'; echo ready; exec cat -v`)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^ready`), 10*time.Second)
	if n := focusInReports(t, base, sess, win); n != 0 {
		t.Fatalf("the pane heard %d focus-in reports before any client attached", n)
	}

	handOver(t, base, map[string]any{})
	attachIn(t, base, sess, startOpts{})
	deadline := time.Now().Add(10 * time.Second)
	for focusInReports(t, base, sess, win) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	n := focusInReports(t, base, sess, win)

	writeHandoffArtifact(t, map[string]any{"focus_in_reports": n, "screen": mustScrollback(t, base, sess, win)})
	if n == 0 {
		t.Fatalf("the attached client reported no focus to the pane, so the test exercised nothing")
	}
	if n != 1 {
		t.Errorf("ASSERTION: the program heard %d focus-in reports after one client attached to the new image, want 1", n)
	}
}
