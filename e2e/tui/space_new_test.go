package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The "New space..." rows and the new_space action behind them: each opens the
// rename dialog titled "new space", and Enter makes a session in the typed
// space through new-session's space and switches to it. The rows sit on blank
// rail's menu, a session row's menu and a space heading's menu, and none reads
// the row it was opened on.
//
// Every claim about what changed is read off tuios ls --json, which is the
// daemon's word, and the screens are saved under artifactDir. The fixture is
// spaceMenusFixture's, with new_space bound to ctrl+b @ as well.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "New space rows and action").

// newSpaceBinding binds new_space to ctrl+b @, free in the default prefix
// table, under spaceMenusConfig's prefix_mode table.
const newSpaceBinding = "new_space = [\"@\"]\n"

// newSpaceFixture is spaceMenusFixture with new_space bound, attached to
// e2e-a1.
func newSpaceFixture(t *testing.T) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, spaceMenusConfig+newSpaceBinding)
	spaceUIFixture(t, base, true)
	term := attachSpaceUI(t, base, "e2e-a1", 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "alpha", "beta", "e2e-loose")
	windowManagementMode(t, term)
	return base, term
}

// waitNewSpaceDialog waits for the new space dialog to open, and fails unless
// its Enter hint says what Enter does there: create, not the rename's save.
func waitNewSpaceDialog(t *testing.T, term *tuitest.Terminal, how string) {
	t.Helper()
	if err := term.WaitForText("new space", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s opened no new space dialog: %v\n%s", how, err, term.Snapshot())
	}
	if text := term.Screen().Text(); !strings.Contains(text, "create") || strings.Contains(text, "save") {
		t.Fatalf("ASSERTION: %s opened a new space dialog whose Enter hint is not create\n%s", how, term.Snapshot())
	}
}

// wantNewSessionIn types space into the open dialog, commits it, and fails
// unless exactly one session was made, in space, with the client switched to
// it and the rail drawing it under space's heading. It returns the session.
func wantNewSessionIn(t *testing.T, base string, term *tuitest.Terminal, before map[string]string, space, what string) string {
	t.Helper()
	typeSpaceName(t, term, "", space)
	name, got := waitNewSession(t, base, before)
	if got != space {
		t.Fatalf("ASSERTION: %s made %s in space %q, want %s", what, name, got, space)
	}
	merged := make(map[string]string, len(before)+1)
	for k, v := range before {
		merged[k] = v
	}
	merged[name] = space
	wantSessionSpaces(t, base, what, merged)
	clientShows(t, base, name)
	waitRailHeadings(t, term, what, []string{space, name}, nil)
	return name
}

// TestBlankRailNewSpaceRow: blank rail's menu offers "New space...", which
// names a space and makes a session in it. The rail draws the sessions section
// alone, so the blank rail under it is no other section's.
//
// How it could pass wrongly, and what stops it:
//   - the row missing or unwired: the dialog must open from it.
//   - a plain session made, or the attached session's space taken: the new
//     session must be in gamma, which no session was in.
//   - the session made and not switched to: the client must show it.
func TestBlankRailNewSpaceRow(t *testing.T) {
	dir := artifactDir(t)
	base := t.TempDir()
	writeConfig(t, base, spaceUIRail+"sections = \"sessions\"\n")
	spaceUIFixture(t, base, true)
	term := attachSpaceUI(t, base, "e2e-a1", 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "alpha", "beta", "e2e-loose")
	windowManagementMode(t, term)
	s := term.Screen()
	_, rows := s.Size()
	blank := -1
	for r := rows - 4; r > railRowOf(s, "e2e-loose"); r-- {
		if strings.Trim(railLine(s, r), " │") == "" {
			blank = r
			break
		}
	}
	if blank < 0 {
		t.Fatalf("no blank rail row to right-click\n%s", term.Snapshot())
	}
	mouseClick(t, term, 3, blank, tuitest.MouseRight, 0)
	if err := term.WaitForText("Sidebar settings", uiTimeout); err != nil {
		t.Fatalf("blank rail opened no menu: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "New space") {
		t.Fatalf("ASSERTION: blank rail's menu offered no New space\n%s", term.Snapshot())
	}
	saveArtifact(t, term, dir, "rail-menu")
	clickMenuRow(t, term, "New space")
	waitNewSpaceDialog(t, term, "blank rail's New space")
	saveArtifact(t, term, dir, "dialog")
	wantNewSessionIn(t, base, term, spaceFixtureSpaces, "gamma", "after blank rail's New space gamma")
	saveArtifact(t, term, dir, "rail-gamma")
	alive(t, term, "after blank rail's New space")
}

// TestSessionMenuNewSpaceRow: a session row's "New space..." makes a session
// in the typed space and leaves the row's own session where it was.
//
// How it could pass wrongly, and what stops it:
//   - the row missing or unwired: the menu must offer it and the dialog open.
//   - the row moving its session instead (the "Move to space..." path):
//     e2e-b1 must stay in beta, and one session must be added.
func TestSessionMenuNewSpaceRow(t *testing.T) {
	dir := artifactDir(t)
	base, term := newSpaceFixture(t)

	rightClickRail(t, term, "e2e-b1", "New space")
	saveArtifact(t, term, dir, "session-menu")
	clickMenuRow(t, term, "New space")
	waitNewSpaceDialog(t, term, "the session row's New space")
	wantNewSessionIn(t, base, term, spaceFixtureSpaces, "delta", "after the session row's New space delta")
	saveArtifact(t, term, dir, "rail-delta")
	alive(t, term, "after the session row's New space")
}

// TestSpaceHeadingNewSpaceRow: a space heading's "New space..." makes a
// session in the typed space, not in the heading's.
//
// How it could pass wrongly, and what stops it:
//   - the row missing or unwired: the menu must offer it and the dialog open.
//   - the heading's space taken (the "New session here" path): the session
//     must be in epsilon, not beta.
func TestSpaceHeadingNewSpaceRow(t *testing.T) {
	dir := artifactDir(t)
	base, term := newSpaceFixture(t)

	rightClickRail(t, term, "beta", "New space")
	saveArtifact(t, term, dir, "heading-menu")
	clickMenuRow(t, term, "New space")
	waitNewSpaceDialog(t, term, "the heading's New space")
	wantNewSessionIn(t, base, term, spaceFixtureSpaces, "epsilon", "after the heading's New space epsilon")
	saveArtifact(t, term, dir, "rail-epsilon")
	alive(t, term, "after the heading's New space")
}

// TestNewSpaceKey: new_space bound to a key opens the dialog, and a typed name
// makes a session in that space and switches to it. A name that is a space
// already makes a session in that space, as tuios new --space does.
//
// How it could pass wrongly, and what stops it:
//   - the action unregistered or the key unbound: the dialog must open.
//   - an existing name refused or turned into a second space: the session
//     typed as alpha must be filed under alpha, beside e2e-a1 and e2e-a2.
func TestNewSpaceKey(t *testing.T) {
	dir := artifactDir(t)
	base, term := newSpaceFixture(t)

	sendKeys(t, term, tuitest.Ctrl('b'), "@")
	waitNewSpaceDialog(t, term, "ctrl+b @")
	saveArtifact(t, term, dir, "dialog")
	first := wantNewSessionIn(t, base, term, spaceFixtureSpaces, "zeta", "after new_space zeta")
	saveArtifact(t, term, dir, "rail-zeta")

	before := withSpaces(map[string]string{first: "zeta"})
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "@")
	waitNewSpaceDialog(t, term, "ctrl+b @ again")
	wantNewSessionIn(t, base, term, before, "alpha", "after new_space alpha, a space that exists")
	saveArtifact(t, term, dir, "rail-alpha")
	alive(t, term, "after new_space")
}

// TestNewSpaceCancelMakesNothing: Esc, and Enter on an empty field, close the
// dialog and make nothing.
//
// How it could pass wrongly, and what stops it:
//   - the dialog never opening, so nothing is trivially made: each attempt
//     must see the dialog open first.
//   - Esc committing the typed name, or an empty field making a plain session:
//     ls --json must be as it was after both, with no kappa heading drawn.
func TestNewSpaceCancelMakesNothing(t *testing.T) {
	dir := artifactDir(t)
	base, term := newSpaceFixture(t)

	sendKeys(t, term, tuitest.Ctrl('b'), "@")
	waitNewSpaceDialog(t, term, "ctrl+b @")
	sendKeys(t, term, "kappa")
	if err := term.WaitForText("kappa", uiTimeout); err != nil {
		t.Fatalf("the dialog did not take kappa: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "typed")
	sendKeys(t, term, tuitest.Esc)
	waitNewSpaceDialogGone(t, term, "after Esc")

	sendKeys(t, term, tuitest.Ctrl('b'), "@")
	waitNewSpaceDialog(t, term, "ctrl+b @ again")
	sendKeys(t, term, tuitest.Enter)
	waitNewSpaceDialogGone(t, term, "after Enter on an empty field")

	time.Sleep(2 * time.Second)
	saveArtifact(t, term, dir, "after-cancel")
	wantSessionSpaces(t, base, "after cancelling new_space twice", spaceFixtureSpaces)
	if railRowOf(term.Screen(), "kappa") >= 0 {
		t.Fatalf("ASSERTION: the rail draws kappa after a cancel\n%s", term.Snapshot())
	}
	clientShows(t, base, "e2e-a1")
	alive(t, term, "after cancelling new_space")
}

// waitNewSpaceDialogGone waits for the new space dialog to close.
func waitNewSpaceDialogGone(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "new space")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s the new space dialog is still open: %v\n%s", when, err, term.Snapshot())
	}
}
