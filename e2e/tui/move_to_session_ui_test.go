package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Moving a pane from the UI: the move_window action (Ctrl+B t m) and the pane
// menu's "Move to..." row open one flow, which picks the session, this one or
// another, then the workspace in it, and sends move-window for the pane it
// was opened on: with to_session for another session, without it for this
// one.
//
// How it could fail, and how each check could pass wrongly, written down
// before the code:
//   - The action is named in the config but nothing runs it, or the default
//     key is not bound. The key opens the flow's first step, whose title
//     names the pane, so a key that does nothing fails at the title.
//   - The first step leaves out this session, or does not mark it. The test
//     that moves within alpha waits for the "this session" mark and takes the
//     first row; without the row Enter picks beta and the pane leaves alpha.
//   - Picking this session sends to_session anyway. The daemon refuses it
//     (same_session) and the pane stays on its workspace.
//   - This session's step offers the pane's own workspace. It would sort
//     before the workspace that holds the other pane, so Enter on the first
//     row would move nothing. A digit naming the pane's own workspace must
//     move nothing and say so.
//   - The move within alpha follows the pane. alpha must still show
//     workspace 1 after it.
//   - The other sessions' rows are not after this session's. Every move to
//     beta steps down one row first, so a list without alpha first picks
//     the wrong session.
//   - The workspace step is shown but its choice is not sent. The pane is
//     moved to a workspace the target does not show (3 from a row, 5 from a
//     typed digit), so a call without the workspace lands on 1 and fails.
//   - The default row sends a workspace of its own instead of leaving the
//     choice to the daemon. The default path is checked against the
//     workspace the target shows, read from session-info.
//   - The pane is copied or respawned rather than moved. list-windows of both
//     sessions is read, and the program prints its pid before the move and
//     answers a line typed after it from the same pid in the target.
//   - The flow follows the pane. The client must still show alpha after the
//     move, and say where the pane went.
//   - The menu row acts on the focused pane rather than the one clicked. The
//     row is opened on the pane that is not focused, and only that pane may
//     move.
//   - Esc at either step moves something anyway, or leaves the flow half
//     open so the next Enter moves. After each Esc the pane is still in
//     alpha, and the next overlay opened is the flow from its first step.
//   - The row looks live on a pane the daemon refuses to move. On a scratch
//     pane the arrows must step over it and a click on it must open nothing.
//
// Each test saves list-windows of both sessions and the client's screen at
// the end to the artifact directory (artifactDir).
//
// NEGATIVE CONTROLS are in NEGATIVE_CONTROLS.md under "Move a pane to another
// session from the UI" and "Move a pane to another workspace or session from
// the UI".

// moveUIConfig tiles alpha so its two panes sit side by side where a click
// can tell them apart. The action keeps its default key.
const moveUIConfig = `[startup]
tiled = true
layout = "bsp"
`

// openMovePane presses the default key, Ctrl+B t m.
func openMovePane(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	pressKeys(t, term, tuitest.Ctrl('b'), "t", "m")
}

// moveUIFixture makes alpha with two panes, "stay" and "mover", and beta with
// one pane on workspace 3 while it shows workspace 1. mover runs a program
// that prints its pid once and then answers every line it reads with it. The
// client is attached to alpha in window management mode, with focus on the
// pane named focus.
func moveUIFixture(t *testing.T, focus string) (*tuitest.Terminal, string, func(string, map[string]any) map[string]any, map[string]string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, moveUIConfig)
	for _, s := range []string{"alpha", "beta"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	must := func(verb string, params map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(params)
		out, err := tuiosCLI(t, base, "call", verb, string(raw))
		var res map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &res)
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", verb, params, err, out)
		}
		return res
	}
	ids := map[string]string{}
	open := func(session, name string, cmd []string) {
		params := map[string]any{"session": session, "name": name}
		if cmd != nil {
			params["command"] = cmd
		}
		id, _ := must("new-window", params)["window_id"].(string)
		if id == "" {
			t.Fatalf("new-window %s in %s gave no window_id", name, session)
		}
		ids[name] = id
	}
	open("alpha", "stay", nil)
	open("alpha", "mover", []string{"sh", "-c", `echo MOVEUI-MARK pid-$$; while read l; do echo "got-$l pid-$$"; done`})
	open("beta", "resident", nil)
	must("move-window", map[string]any{"session": "beta", "window": ids["resident"], "workspace": 3})

	term := attachIn(t, base, "alpha", startOpts{cols: 140, rows: 40})
	clientShows(t, base, "alpha")
	waitForAll(t, term, 20*time.Second, "mover draws its marker", "MOVEUI-MARK")
	must("focus-window", map[string]any{"session": "alpha", "window": ids[focus]})
	return term, base, must, ids
}

// moverPid is the pid the mover program printed in session, read from its
// captured output.
func moverPid(t *testing.T, base, session, window, want string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		raw, _ := json.Marshal(map[string]any{"session": session, "window": window, "source": "recent"})
		out, err := tuiosCLI(t, base, "call", "capture-pane", string(raw))
		var res map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &res)
		last, _ = res["content"].(string)
		if err == nil {
			if i := strings.Index(last, want); i >= 0 {
				for _, f := range strings.Fields(last[i:]) {
					if strings.HasPrefix(f, "pid-") {
						return f
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s in session %s never showed %q with a pid:\n%s", window, session, want, last)
	return ""
}

// saveMoveUIArtifact writes list-windows of both sessions and the client's
// screen, so a person can see where every pane ended up.
func saveMoveUIArtifact(t *testing.T, term *tuitest.Terminal, base, name string) {
	t.Helper()
	dir := artifactDir(t)
	for _, s := range []string{"alpha", "beta"} {
		out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", s)
		if err != nil {
			t.Errorf("list-windows %s: %v\n%s", s, err, out)
			continue
		}
		path := filepath.Join(dir, name+"-list-windows-"+s+".json")
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Errorf("save %s: %v", path, err)
		}
	}
	saveArtifact(t, term, dir, name+"-screen")
	t.Logf("artifacts: %s", dir)
}

// waitMoved waits for the pane to be on workspace ws of beta and gone from
// alpha, then checks that its program is the same process and still reads
// its input there.
func waitMoved(t *testing.T, term *tuitest.Terminal, base, window, pid string, ws int, what string) {
	t.Helper()
	waitMovedIn(t, base, "beta", window, pid, ws, what)
	if _, ok := listPanes(t, base, "alpha")[window]; ok {
		t.Fatalf("ASSERTION: %s: the pane is in beta and still in alpha\n%s", what, term.Snapshot())
	}
}

// waitMovedIn waits for the pane to be on workspace ws of session, then
// checks that its program is the same process and still reads its input
// there.
func waitMovedIn(t *testing.T, base, session, window, pid string, ws int, what string) {
	t.Helper()
	got := waitPane(t, base, session, window, what+": the pane in "+session, func(p paneRow) bool { return p.ID == window && p.Workspace == ws })
	if got.Workspace != ws {
		t.Fatalf("ASSERTION: %s: the pane is on workspace %d of %s, want %d", what, got.Workspace, session, ws)
	}
	if after := moverPid(t, base, session, window, "MOVEUI-MARK"); after != pid {
		t.Fatalf("ASSERTION: %s: the program printed %s before the move and %s after it: it was started again", what, pid, after)
	}
	for _, keys := range [][]string{{"--raw", "hello"}, {"Enter"}} {
		args := append([]string{"send-keys", "-s", session, "-w", window}, keys...)
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("send-keys to the moved pane: %v\n%s", err, out)
		}
	}
	if got := moverPid(t, base, session, window, "got-hello"); got != pid {
		t.Fatalf("ASSERTION: %s: the moved pane answered from %s, want %s", what, got, pid)
	}
}

// stillHome fails unless the pane is in alpha and not in beta.
func stillHome(t *testing.T, term *tuitest.Terminal, base, window, what string) {
	t.Helper()
	if _, ok := listPanes(t, base, "alpha")[window]; !ok {
		t.Fatalf("ASSERTION: %s: the pane left alpha\n%s", what, term.Snapshot())
	}
	if _, ok := listPanes(t, base, "beta")[window]; ok {
		t.Fatalf("ASSERTION: %s: the pane is in beta\n%s", what, term.Snapshot())
	}
}

// pressKeys sends keys and fails the test when the terminal will not take them.
func pressKeys(t *testing.T, term *tuitest.Terminal, keys ...any) {
	t.Helper()
	if err := term.SendKeys(keys...); err != nil {
		t.Fatalf("send %v: %v", keys, err)
	}
}

// waitScreenText waits for text, failing with the frame.
func waitScreenText(t *testing.T, term *tuitest.Terminal, text, what string) {
	t.Helper()
	if err := term.WaitForText(text, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: %q never showed: %v\n%s", what, text, err, term.Snapshot())
	}
}

// TestMoveToSessionKey: the default key moves the focused pane to beta's
// workspace 3, chosen as a row of the workspace step, and the client stays on
// alpha.
func TestMoveToSessionKey(t *testing.T) {
	term, base, _, ids := moveUIFixture(t, "mover")
	win := ids["mover"]
	pid := moverPid(t, base, "alpha", win, "MOVEUI-MARK")
	stillHome(t, term, base, win, "before the key")

	openMovePane(t, term)
	waitScreenText(t, term, "Move mover to session", "the key opens the session step")
	saveArtifact(t, term, artifactDir(t), "key-session-step")
	// alpha, this session, is the first row and beta the second.
	pressKeys(t, term, tuitest.Down, tuitest.Enter)
	waitScreenText(t, term, "Move mover to beta", "Enter on beta opens the workspace step")
	saveArtifact(t, term, artifactDir(t), "key-workspace-step")

	// The step lists the workspace beta shows first and then the ones holding
	// a pane, so 3 is the second row.
	pressKeys(t, term, tuitest.Down, tuitest.Enter)
	waitScreenText(t, term, "Moved mover to beta · workspace 3", "the notice says where the pane went")
	waitMoved(t, term, base, win, pid, 3, "the key path")

	// The client stays where it was: on alpha, with the other pane.
	clientShows(t, base, "alpha")
	if _, ok := listPanes(t, base, "alpha")[ids["stay"]]; !ok {
		t.Fatalf("ASSERTION: the pane that was not moved left alpha")
	}
	if info := sessionInfoOf(t, base, "beta"); info.CurrentWorkspace != 1 {
		t.Fatalf("ASSERTION: beta shows workspace %d after the move, want 1: the move followed the pane", info.CurrentWorkspace)
	}
	saveMoveUIArtifact(t, term, base, "key")
}

// TestMoveToSessionMenuRow: the pane menu's row, opened on the pane that is
// not focused, moves that pane to a workspace typed as a digit, one beta has
// no pane on.
func TestMoveToSessionMenuRow(t *testing.T) {
	term, base, _, ids := moveUIFixture(t, "stay")
	win := ids["mover"]
	pid := moverPid(t, base, "alpha", win, "MOVEUI-MARK")
	stillHome(t, term, base, win, "before the menu")

	p := listPanes(t, base, "alpha")[win]
	shiftRightClick(t, term, p.X+p.Width/2, p.Y+p.Height/2)
	waitMenu(t, term, "the pane menu", "Move to...")
	row, col := findText(t, term, "Move to...")
	leftClick(t, term, col+2, row)
	waitScreenText(t, term, "Move mover to session", "the row opens the flow on the clicked pane")
	pressKeys(t, term, tuitest.Down, tuitest.Enter)
	waitScreenText(t, term, "Move mover to beta", "Enter on beta opens the workspace step")

	pressKeys(t, term, "5")
	waitScreenText(t, term, "Moved mover to beta · workspace 5", "the notice says where the pane went")
	waitMoved(t, term, base, win, pid, 5, "the menu row")
	if _, ok := listPanes(t, base, "alpha")[ids["stay"]]; !ok {
		t.Fatalf("ASSERTION: the focused pane left alpha: the row acted on the wrong pane")
	}
	clientShows(t, base, "alpha")
	saveMoveUIArtifact(t, term, base, "menu")
}

// TestMoveToSessionEscCancels: Esc at the session step and at the workspace
// step moves nothing, and the flow opened again afterwards starts over. The
// last run takes the default row, which leaves the workspace to the daemon:
// the one beta shows.
func TestMoveToSessionEscCancels(t *testing.T) {
	term, base, _, ids := moveUIFixture(t, "mover")
	win := ids["mover"]
	pid := moverPid(t, base, "alpha", win, "MOVEUI-MARK")

	openMovePane(t, term)
	waitScreenText(t, term, "Move mover to session", "the key opens the session step")
	pressKeys(t, term, tuitest.Esc)
	waitMenuGone(t, term, "Move mover to session", "Esc at the session step")
	time.Sleep(time.Second)
	stillHome(t, term, base, win, "after Esc at the session step")

	openMovePane(t, term)
	waitScreenText(t, term, "Move mover to session", "the key opens the session step again")
	pressKeys(t, term, tuitest.Down, tuitest.Enter)
	waitScreenText(t, term, "Move mover to beta", "Enter on beta opens the workspace step")
	pressKeys(t, term, tuitest.Esc)
	waitMenuGone(t, term, "Move mover to beta", "Esc at the workspace step")
	time.Sleep(time.Second)
	stillHome(t, term, base, win, "after Esc at the workspace step")

	// Opened again, the flow starts at the session step, not where it was
	// left.
	openMovePane(t, term)
	waitScreenText(t, term, "Move mover to session", "the flow starts over after Esc")
	pressKeys(t, term, tuitest.Down, tuitest.Enter)
	waitScreenText(t, term, "Move mover to beta", "the workspace step")
	shown := sessionInfoOf(t, base, "beta").CurrentWorkspace
	pressKeys(t, term, tuitest.Enter)
	waitMoved(t, term, base, win, pid, shown, "the default row")
	saveMoveUIArtifact(t, term, base, "esc")
}

// TestMoveToSessionRowDimmedOnScratch: on a scratch pane the row is there
// and dimmed. The arrows step over it, and a click on it opens nothing and
// moves nothing. The row is live on an ordinary pane, which
// TestMoveToSessionMenuRow clicks.
func TestMoveToSessionRowDimmedOnScratch(t *testing.T) {
	term, base, _, _ := moveUIFixture(t, "mover")

	pressKeys(t, term, tuitest.Ctrl('b'), "g")
	waitSessionScratch(t, term, base, "alpha", 1, "the scratch pane opens")
	scratch := scratchPaneOf(t, base, "alpha")
	if scratch == "" {
		t.Fatal("setup: no scratch pane in alpha")
	}
	p := listPanes(t, base, "alpha")[scratch]
	shiftRightClick(t, term, p.X+p.Width/2, p.Y+p.Height/2)
	waitMenu(t, term, "the scratch pane's menu", "Move to...", "Close pane")

	// One lap and more of the menu: the marker never lands on the row.
	for i := range 14 {
		if strings.Contains(markedRow(term.Screen()), "Move to...") {
			t.Fatalf("ASSERTION: step %d: the arrows landed on the row on a scratch pane\n%s", i, term.Snapshot())
		}
		pressKeys(t, term, tuitest.Down)
		time.Sleep(60 * time.Millisecond)
	}

	row, col := findText(t, term, "Move to...")
	leftClick(t, term, col+2, row)
	time.Sleep(time.Second)
	s := term.Screen()
	_, rows := s.Size()
	for r := range rows {
		if line := s.Line(r); strings.Contains(line, " to session") {
			t.Fatalf("ASSERTION: a click on the dimmed row opened the flow\n%s", term.Snapshot())
		}
	}
	if _, ok := listPanes(t, base, "alpha")[scratch]; !ok {
		t.Fatalf("ASSERTION: the scratch pane left alpha")
	}
	saveMoveUIArtifact(t, term, base, "scratch")
}

// TestMovePaneWithinSession: the key, then this session's row, moves the pane
// to another workspace of alpha. A digit naming the workspace the pane is on
// moves nothing. The pane keeps its process, and the client stays on the
// workspace it was on.
func TestMovePaneWithinSession(t *testing.T) {
	term, base, must, ids := moveUIFixture(t, "mover")
	win := ids["mover"]
	pid := moverPid(t, base, "alpha", win, "MOVEUI-MARK")
	// A pane on alpha's workspace 4 makes it the one row of this session's
	// step: workspace 1 holds the mover, and moving there is no move.
	parked, _ := must("new-window", map[string]any{"session": "alpha", "name": "parked"})["window_id"].(string)
	must("move-window", map[string]any{"session": "alpha", "window": parked, "workspace": 4})
	must("focus-window", map[string]any{"session": "alpha", "window": win})
	waitPane(t, base, "alpha", parked, "the parked pane on workspace 4", func(p paneRow) bool { return p.Workspace == 4 })

	openMovePane(t, term)
	waitScreenText(t, term, "this session", "the session step marks this session")
	saveArtifact(t, term, artifactDir(t), "here-session-step")
	pressKeys(t, term, tuitest.Enter)
	waitScreenText(t, term, "Move mover to alpha", "Enter on this session opens its workspace step")
	pressKeys(t, term, "1")
	waitScreenText(t, term, "mover is already on workspace 1", "a digit naming the pane's own workspace")
	time.Sleep(time.Second)
	if p := listPanes(t, base, "alpha")[win]; p.Workspace != 1 {
		t.Fatalf("ASSERTION: the digit for the pane's own workspace moved it to %d\n%s", p.Workspace, term.Snapshot())
	}

	openMovePane(t, term)
	waitScreenText(t, term, "this session", "the session step again")
	pressKeys(t, term, tuitest.Enter)
	waitScreenText(t, term, "Move mover to alpha", "this session's workspace step")
	saveArtifact(t, term, artifactDir(t), "here-workspace-step")
	pressKeys(t, term, tuitest.Enter)
	waitScreenText(t, term, "Moved mover to workspace 4", "the notice says where the pane went")
	waitMovedIn(t, base, "alpha", win, pid, 4, "the move within alpha")
	if _, ok := listPanes(t, base, "beta")[win]; ok {
		t.Fatalf("ASSERTION: the pane moved within alpha is in beta")
	}

	clientShows(t, base, "alpha")
	if info := sessionInfoOf(t, base, "alpha"); info.CurrentWorkspace != 1 {
		t.Fatalf("ASSERTION: alpha shows workspace %d after the move, want 1: the move followed the pane", info.CurrentWorkspace)
	}
	if p := listPanes(t, base, "alpha")[ids["stay"]]; p.Workspace != 1 {
		t.Fatalf("ASSERTION: the pane that was not moved is on workspace %d, want 1", p.Workspace)
	}
	saveMoveUIArtifact(t, term, base, "here")
}

// scratchPaneOf is the id of a scratch pane in session, or "".
func scratchPaneOf(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var res struct {
		Windows []struct {
			ID      string `json:"window_id"`
			Scratch bool   `json:"scratch"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse list-windows: %v\n%s", err, out)
	}
	for _, w := range res.Windows {
		if w.Scratch {
			return w.ID
		}
	}
	return ""
}
