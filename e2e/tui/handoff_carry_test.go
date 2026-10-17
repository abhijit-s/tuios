package tuie2e

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
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

// TestSelfExecPrunesMetadataWithATimeToLive gives a working agent's pane a
// metadata token that lives 7 s (set-agent-meta ttl_ms), and hands the panes
// over at once. A client attached to the new image draws the token on the
// agent's rail row. Clients draw only what the daemon pushes, and keep no
// clock of their own, so the token leaves the rail only when the daemon's
// prune drops it from the state: the new image must arm that prune again.
//
// How it could pass wrongly: the token could expire before the client
// attached, and never be drawn. The test waits for it on the rail first.
func TestSelfExecPrunesMetadataWithATimeToLive(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	startHandoffDaemon(t, base)
	const sess = "e2e-ttl"
	const token = "ttlmark7"
	newCommandWindow(t, base, sess, "agent", false, "sh", "-c", "exec sleep 600")
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", sess, "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	setAt := time.Now()
	_ = mustCall(t, base, "set-agent-meta", map[string]any{"session": sess, "window": "0", "tokens": map[string]any{"phase": token}, "ttl_ms": 7000})

	handOver(t, base, map[string]any{})
	term := attachIn(t, base, sess, startOpts{})
	waitForAll(t, term, 5*time.Second, "the token on the rail of a client of the new image", token)
	time.Sleep(time.Until(setAt.Add(9 * time.Second)))
	err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), token) }, 3*time.Second)

	writeHandoffArtifact(t, map[string]any{"still_drawn_at_12s": err != nil, "screen": term.Screen().Text()})
	if err != nil {
		t.Errorf("ASSERTION: the token outlived its 7 s on the rail by %s after the handoff:\n%s", time.Since(setAt.Add(7*time.Second)).Round(time.Second), term.Snapshot())
	}
}
