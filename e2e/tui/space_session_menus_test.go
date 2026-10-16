package tuie2e

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The space rows of the rail's menus, and the actions behind them:
//
//   - a session row's "Move to space..." (set_session_space) opens the rename
//     dialog on the session's space and commits it through set-session-space;
//   - a space heading's "New session here" (new_session_in_space) makes a
//     session filed under the heading's space through new-session's space;
//   - a session row's "New session", and blank rail's, run new_session.
//
// Every claim about what changed is read off tuios ls --json, which is the
// daemon's word, and the screens are saved under artifactDir.
//
// The fixture is spaceUIFixture's: e2e-loose in no space, e2e-b1 in beta,
// e2e-a1 and e2e-a2 in alpha. The client is attached to e2e-a1 unless a test
// says otherwise, so a row that acts on the attached session instead of the
// one it was opened on moves the wrong session.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space rows of the session menus").

// spaceMenusConfig opens the rail and binds set_session_space to ctrl+b & and
// new_session_in_space to ctrl+b ^, both free in the default prefix table.
const spaceMenusConfig = spaceUIRail + "\n[keybindings.prefix_mode]\nset_session_space = [\"&\"]\nnew_session_in_space = [\"^\"]\n"

// spaceFixtureSpaces is spaceUIFixture's sessions and their spaces.
var spaceFixtureSpaces = map[string]string{
	"e2e-loose": "", "e2e-b1": "beta", "e2e-a1": "alpha", "e2e-a2": "alpha",
}

// spaceMenusFixture is the spaced fixture, plus whatever extra makes, with a
// client attached to session in window management mode and the rail drawn.
func spaceMenusFixture(t *testing.T, session string, extra func(base string)) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, spaceMenusConfig)
	spaceUIFixture(t, base, true)
	if extra != nil {
		extra(base)
	}
	term := attachSpaceUI(t, base, session, 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "alpha", "beta", "e2e-loose")
	windowManagementMode(t, term)
	return base, term
}

// rightClickRail right-clicks the rail row naming name and waits for the menu
// to offer row.
func rightClickRail(t *testing.T, term *tuitest.Terminal, name, row string) {
	t.Helper()
	s := term.Screen()
	r := railRowOf(s, name)
	col := nameColOf(s, r, name)
	if col < 0 {
		t.Fatalf("%s is not on the rail\n%s", name, term.Snapshot())
	}
	mouseClick(t, term, col+1, r, tuitest.MouseRight, 0)
	if err := term.WaitForText(row, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the menu of %s offered no %q: %v\n%s", name, row, err, term.Snapshot())
	}
}

// clickMenuRow left-clicks the first line on screen holding label.
func clickMenuRow(t *testing.T, term *tuitest.Terminal, label string) {
	t.Helper()
	col, row := findOnScreen(t, term, label)
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
}

// openMoveToSpace right-clicks session's rail row, takes "Move to space...",
// and waits for the dialog to name session.
func openMoveToSpace(t *testing.T, term *tuitest.Terminal, session string) {
	t.Helper()
	rightClickRail(t, term, session, "Move to space")
	clickMenuRow(t, term, "Move to space")
	waitMoveDialog(t, term, session)
}

// waitMoveDialog waits for the move dialog to name session.
func waitMoveDialog(t *testing.T, term *tuitest.Terminal, session string) {
	t.Helper()
	if err := term.WaitForText("move "+session+" to space", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the move dialog did not name %s: %v\n%s", session, err, term.Snapshot())
	}
}

// withSpaces is spaceFixtureSpaces with the given changes.
func withSpaces(changes map[string]string) map[string]string {
	out := maps.Clone(spaceFixtureSpaces)
	maps.Copy(out, changes)
	return out
}

// waitNewSession waits for ls --json to list one session that before did not,
// and returns its name and space.
func waitNewSession(t *testing.T, base string, before map[string]string) (string, string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		got := sessionSpaces(t, base)
		var added []string
		for name := range got {
			if _, ok := before[name]; !ok {
				added = append(added, name)
			}
		}
		if len(added) == 1 {
			return added[0], got[added[0]]
		}
		if len(added) > 1 {
			t.Fatalf("ASSERTION: %d sessions were made, want 1: %v", len(added), got)
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: no session was made: %v", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestRailSessionMenuMovesThatSessionToANewSpace: "Move to space..." on a
// session the client is not attached to files that session under the typed
// name, which is a space nobody is in yet, and the rail draws its heading.
//
// How it could pass wrongly, and what stops it:
//   - the row acting on the attached session (the bug class of a row that
//     names one thing and acts on another): the dialog must name e2e-loose,
//     and e2e-a1 must stay in alpha.
//   - the move shown and never sent: ls --json is read, not the screen.
func TestRailSessionMenuMovesThatSessionToANewSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	openMoveToSpace(t, term, "e2e-loose")
	saveArtifact(t, term, dir, "dialog")
	typeSpaceName(t, term, "", "gamma")

	waitRailHeadings(t, term, "after moving e2e-loose to gamma", []string{"gamma", "alpha", "beta"}, nil)
	saveArtifact(t, term, dir, "rail-gamma")
	wantSessionSpaces(t, base, "after moving e2e-loose to gamma", withSpaces(map[string]string{"e2e-loose": "gamma"}))
	alive(t, term, "after moving a session to a new space")
}

// TestRailSessionMenuJoinsAnExistingSpace: typing a space that exists files the
// session beside its members.
//
// How it could pass wrongly, and what stops it:
//   - the attached session moved instead: the dialog must name e2e-b1, seeded
//     with beta, and e2e-a1 is in alpha already, so only ls telling e2e-b1 is
//     in alpha passes.
//   - a rename of beta instead of a move: beta's heading must go, and alpha
//     must keep e2e-a1 and e2e-a2.
func TestRailSessionMenuJoinsAnExistingSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	openMoveToSpace(t, term, "e2e-b1")
	if !strings.Contains(term.Screen().Text(), "beta") {
		t.Fatalf("ASSERTION: the dialog is not seeded with beta\n%s", term.Snapshot())
	}
	typeSpaceName(t, term, "beta", "alpha")

	waitRailHeadings(t, term, "after e2e-b1 joined alpha", []string{"alpha", "e2e-b1"}, []string{"beta"})
	saveArtifact(t, term, dir, "rail-joined")
	wantSessionSpaces(t, base, "after e2e-b1 joined alpha", withSpaces(map[string]string{"e2e-b1": "alpha"}))
	alive(t, term, "after joining a space")
}

// TestRailSessionMenuEmptyFieldClearsTheSpace: emptying the field takes the
// session out of every space.
//
// How it could pass wrongly, and what stops it:
//   - an empty field treated as a cancel, as the space rename's is: e2e-a2
//     must end in no space.
//   - the attached session cleared instead: e2e-a1 must stay in alpha.
func TestRailSessionMenuEmptyFieldClearsTheSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	openMoveToSpace(t, term, "e2e-a2")
	sendKeys(t, term, strings.Repeat("\x7f", len("alpha")), tuitest.Enter)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sessionSpaces(t, base)["e2e-a2"] == ""
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: e2e-a2 is still in a space: %v", sessionSpaces(t, base))
	}
	saveArtifact(t, term, dir, "cleared")
	wantSessionSpaces(t, base, "after clearing e2e-a2's space", withSpaces(map[string]string{"e2e-a2": ""}))
	alive(t, term, "after clearing a session's space")
}

// TestRailSessionMenuRefusesAnInvalidSpace: a name set-session-space refuses
// is refused in the verb's words, and nothing changes.
//
// How it could pass wrongly, and what stops it:
//   - the refusal swallowed: the notice must carry the verb's message.
//   - the name applied anyway: ls --json must be as it was.
func TestRailSessionMenuRefusesAnInvalidSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	openMoveToSpace(t, term, "e2e-b1")
	typeSpaceName(t, term, "beta", "bad/name")
	if err := term.WaitForText("Move failed: a space name follows the session", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the refusal did not carry the verb's message: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "refused")
	wantSessionSpaces(t, base, "after a refused move", spaceFixtureSpaces)
	alive(t, term, "after a refused move")
}

// TestAGlobalSessionCannotBeMovedToASpace: on a global session's row the row
// is dimmed and opens nothing, and the action reached by key in a global
// session says why instead of opening a dialog that could only fail.
//
// How it could pass wrongly, and what stops it:
//   - the dialog opened and the verb's refusal relied on: no "move session"
//     dialog may show from the row, and the key must show the refusal notice,
//     which only the client's own check shows.
//   - the session filed anyway: ls --json must be as it was.
func TestAGlobalSessionCannotBeMovedToASpace(t *testing.T) {
	dir := artifactDir(t)
	makeGlobal := func(base string) {
		if out, err := tuiosCLI(t, base, "new", "e2e-g", "--global"); err != nil {
			t.Fatalf("create the global session: %v: %s", err, out)
		}
		if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-g"); err != nil {
			t.Fatalf("open a window in the global session: %v: %s", err, out)
		}
	}
	want := withSpaces(map[string]string{"e2e-g": ""})

	base, term := spaceMenusFixture(t, "e2e-a1", makeGlobal)
	waitSpaceUIRail(t, term, "the rail with the global session", "e2e-g")
	rightClickRail(t, term, "e2e-g", "Move to space")
	saveArtifact(t, term, dir, "global-menu")
	clickMenuRow(t, term, "Move to space")
	time.Sleep(time.Second)
	_ = term.WaitStable(time.Second)
	saveArtifact(t, term, dir, "global-row-clicked")
	if strings.Contains(term.Screen().Text(), "move e2e-g to space") {
		t.Fatalf("ASSERTION: the row opened a move dialog on a global session\n%s", term.Snapshot())
	}
	wantSessionSpaces(t, base, "after the global session's row", want)

	gterm := attachSpaceUI(t, base, "e2e-g", 140, 40)
	windowManagementMode(t, gterm)
	sendKeys(t, gterm, tuitest.Ctrl('b'), "&")
	if err := gterm.WaitForText("Not moved: a global session cannot be in a space", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: set_session_space in a global session did not refuse: %v\n%s", err, gterm.Snapshot())
	}
	saveArtifact(t, gterm, dir, "global-key")
	if strings.Contains(gterm.Screen().Text(), "move e2e-g to space") {
		t.Fatalf("ASSERTION: a move dialog opened in a global session\n%s", gterm.Snapshot())
	}
	wantSessionSpaces(t, base, "after set_session_space in the global session", want)
	alive(t, gterm, "after refusing a global session's move")
}

// TestSetSessionSpaceKeyMovesTheAttachedSession: set_session_space bound to a
// key opens the same dialog on the attached session, seeded with its space,
// and moves that session alone.
//
// How it could pass wrongly, and what stops it:
//   - the key unbound or the action unregistered: the dialog must open.
//   - the whole space renamed: e2e-a2 must stay in alpha.
func TestSetSessionSpaceKeyMovesTheAttachedSession(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	sendKeys(t, term, tuitest.Ctrl('b'), "&")
	waitMoveDialog(t, term, "e2e-a1")
	saveArtifact(t, term, dir, "dialog")
	typeSpaceName(t, term, "alpha", "delta")

	waitRailHeadings(t, term, "after moving e2e-a1 to delta", []string{"delta", "alpha", "beta"}, nil)
	saveArtifact(t, term, dir, "rail-delta")
	wantSessionSpaces(t, base, "after moving the attached session by key", withSpaces(map[string]string{"e2e-a1": "delta"}))
	alive(t, term, "after moving the attached session by key")
}

// TestSpaceHeadingNewSessionHere: "New session here" on beta's heading, with
// the client in alpha, makes a session in beta and switches to it.
//
// How it could pass wrongly, and what stops it:
//   - the attached session's space taken instead of the heading's: the new
//     session must be in beta, not alpha.
//   - a plain session made and the space dropped: it must be in a space.
//   - an existing session moved: every fixture session keeps its space.
func TestSpaceHeadingNewSessionHere(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	rightClickRail(t, term, "beta", "New session here")
	saveArtifact(t, term, dir, "heading-menu")
	clickMenuRow(t, term, "New session here")

	name, space := waitNewSession(t, base, spaceFixtureSpaces)
	if space != "beta" {
		t.Fatalf("ASSERTION: the new session %s is in space %q, want beta", name, space)
	}
	wantSessionSpaces(t, base, "after New session here on beta", withSpaces(map[string]string{name: "beta"}))
	clientShows(t, base, name)
	waitRailHeadings(t, term, "after New session here on beta", []string{"beta", name}, nil)
	saveArtifact(t, term, dir, "rail-new")
	alive(t, term, "after making a session in a space")
}

// TestNewSessionInSpaceKey: new_session_in_space bound to a key makes a
// session in the attached session's space, and in a session in no space it
// makes a plain session, as new_session does.
//
// How it could pass wrongly, and what stops it:
//   - the action unregistered: a session must be made at all.
//   - the space not passed: the session made from e2e-b1 must be in beta.
//   - a space invented for the session in no space: that one must be in none.
func TestNewSessionInSpaceKey(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-b1", nil)

	sendKeys(t, term, tuitest.Ctrl('b'), "^")
	name, space := waitNewSession(t, base, spaceFixtureSpaces)
	if space != "beta" {
		t.Fatalf("ASSERTION: the session made from e2e-b1 is in space %q, want beta", space)
	}
	clientShows(t, base, name)
	saveArtifact(t, term, dir, "in-beta")

	before := withSpaces(map[string]string{name: "beta"})
	lterm := attachSpaceUI(t, base, "e2e-loose", 140, 40)
	windowManagementMode(t, lterm)
	sendKeys(t, lterm, tuitest.Ctrl('b'), "^")
	plain, plainSpace := waitNewSession(t, base, before)
	if plainSpace != "" {
		t.Fatalf("ASSERTION: the session made from e2e-loose is in space %q, want none", plainSpace)
	}
	clientShows(t, base, plain)
	saveArtifact(t, lterm, dir, "plain")
	alive(t, lterm, "after new_session_in_space in no space")
}

// TestSessionMenuNewSessionRow: the session row's "New session" and blank
// rail's make a plain session, as N does.
//
// How it could pass wrongly, and what stops it:
//   - the row missing or unwired: a session must be made.
//   - the right-clicked session's space leaking in: the right-clicked row is
//     in beta, and the new session must be in no space.
func TestSessionMenuNewSessionRow(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	rightClickRail(t, term, "e2e-b1", "New session")
	saveArtifact(t, term, dir, "session-menu")
	clickMenuRow(t, term, "New session")
	name, space := waitNewSession(t, base, spaceFixtureSpaces)
	if space != "" {
		t.Fatalf("ASSERTION: the session row's New session made %s in space %q, want none", name, space)
	}
	clientShows(t, base, name)

	alive(t, term, "after the session row's New session")
}

// TestBlankRailNewSessionRow: blank rail's menu offers "New session", which
// makes a plain session as N does. The rail draws the sessions section alone,
// so the blank rail under it is no other section's.
//
// How it could pass wrongly, and what stops it:
//   - the row missing or unwired: a session must be made.
//   - the attached session's space leaking in: the client is in alpha, and
//     the new session must be in no space.
func TestBlankRailNewSessionRow(t *testing.T) {
	dir := artifactDir(t)
	base := t.TempDir()
	writeConfig(t, base, spaceUIRail+"sections = \"sessions\"\n")
	spaceUIFixture(t, base, true)
	term := attachSpaceUI(t, base, "e2e-a1", 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "alpha", "beta", "e2e-loose")
	windowManagementMode(t, term)
	before := spaceFixtureSpaces
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
	if !strings.Contains(term.Screen().Text(), "New session") {
		t.Fatalf("ASSERTION: blank rail's menu offered no New session\n%s", term.Snapshot())
	}
	saveArtifact(t, term, dir, "rail-menu")
	clickMenuRow(t, term, "New session")
	name, space := waitNewSession(t, base, before)
	if space != "" {
		t.Fatalf("ASSERTION: blank rail's New session made %s in space %q, want none", name, space)
	}
	clientShows(t, base, name)
	alive(t, term, "after blank rail's New session")
}

// railNameAt is the screen cell of name's row on the rail, failing when the
// rail does not draw it.
func railNameAt(t *testing.T, term *tuitest.Terminal, name string) (int, int) {
	t.Helper()
	s := term.Screen()
	row := railRowOf(s, name)
	col := nameColOf(s, row, name)
	if col < 0 {
		t.Fatalf("%s is not on the rail\n%s", name, term.Snapshot())
	}
	return col + 1, row
}

// dragRailRow presses on from's rail row, moves through every row to to's row
// (a session row or a space heading), saves the screen mid-drag as artifact,
// and releases there.
func dragRailRow(t *testing.T, term *tuitest.Terminal, dir, artifact, from, to string) {
	t.Helper()
	col, fromRow := railNameAt(t, term, from)
	_, toRow := railNameAt(t, term, to)
	mousePress(t, term, col, fromRow, tuitest.MouseLeft, 0)
	step := 1
	if toRow < fromRow {
		step = -1
	}
	for r := fromRow + step; ; r += step {
		mouseMotion(t, term, col, r, tuitest.MouseLeft, 0)
		if r == toRow {
			break
		}
	}
	_ = term.WaitStable(500 * time.Millisecond)
	saveArtifact(t, term, dir, artifact)
	mouseRelease(t, term, col, toRow, tuitest.MouseLeft, 0)
}

// railOrder fails unless the rail draws every name in names, top to bottom.
func waitRailOrder(t *testing.T, term *tuitest.Terminal, what string, names ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		rows := make([]int, len(names))
		for i, n := range names {
			rows[i] = railRowOf(s, n)
		}
		return strictlyIncreasing(rows)
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: the rail never drew %v in that order: %v\n%s", what, names, err, term.Snapshot())
	}
}

// TestRailDragSessionOntoSpaceHeading: dragging e2e-a2's row onto beta's
// heading, with the client in alpha, files e2e-a2 under beta, last in its
// group.
//
// How it could pass wrongly, and what stops it:
//   - the attached session filed instead of the dragged one: e2e-a1 must stay
//     in alpha, and e2e-a2 must end in beta.
//   - the drop only reordering: ls --json must name beta for e2e-a2.
//   - the rail left as it was: e2e-a2 must be drawn under beta, after e2e-b1.
func TestRailDragSessionOntoSpaceHeading(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	dragRailRow(t, term, dir, "mid-drag", "e2e-a2", "beta")
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return sessionSpaces(t, base)["e2e-a2"] == "beta"
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: e2e-a2 was not filed under beta: %v", sessionSpaces(t, base))
	}
	wantSessionSpaces(t, base, "after dropping e2e-a2 on beta", withSpaces(map[string]string{"e2e-a2": "beta"}))
	waitRailOrder(t, term, "after dropping e2e-a2 on beta", "alpha", "e2e-a1", "beta", "e2e-b1", "e2e-a2", "e2e-loose")
	saveArtifact(t, term, dir, "rail-after")
	alive(t, term, "after dropping a session on a space heading")
}

// TestRailDragSessionIntoNoSpaceRun: dragging e2e-b1 onto e2e-loose, the run
// of sessions in no space, takes e2e-b1 out of beta.
//
// How it could pass wrongly, and what stops it:
//   - the clear not sent: ls --json must give e2e-b1 no space, and beta's
//     heading, which had one member, must go.
//   - the attached session cleared: e2e-a1 must stay in alpha.
func TestRailDragSessionIntoNoSpaceRun(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	dragRailRow(t, term, dir, "mid-drag", "e2e-b1", "e2e-loose")
	if err := term.WaitFor(func(tuitest.Screen) bool {
		return sessionSpaces(t, base)["e2e-b1"] == ""
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: e2e-b1 is still in a space: %v", sessionSpaces(t, base))
	}
	wantSessionSpaces(t, base, "after dropping e2e-b1 in no space", withSpaces(map[string]string{"e2e-b1": ""}))
	waitRailHeadings(t, term, "after dropping e2e-b1 in no space", []string{"alpha", "e2e-b1", "e2e-loose"}, []string{"beta"})
	saveArtifact(t, term, dir, "rail-after")
	alive(t, term, "after dropping a session in no space")
}

// TestRailDragWithinSpaceReorders: dragging e2e-a2 onto e2e-a1, both in alpha,
// reorders them and leaves every space as it was.
//
// How it could pass wrongly, and what stops it:
//   - the drop treated as a move: ls --json must be as it was.
//   - the reorder lost: e2e-a2 must be drawn above e2e-a1.
func TestRailDragWithinSpaceReorders(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceMenusFixture(t, "e2e-a1", nil)

	dragRailRow(t, term, dir, "mid-drag", "e2e-a2", "e2e-a1")
	waitRailOrder(t, term, "after dragging e2e-a2 above e2e-a1", "alpha", "e2e-a2", "e2e-a1", "beta")
	saveArtifact(t, term, dir, "rail-after")
	time.Sleep(time.Second)
	wantSessionSpaces(t, base, "after a drag inside alpha", spaceFixtureSpaces)
	alive(t, term, "after reordering inside a space")
}

// TestRailDragGlobalSessionOntoHeadingChangesNothing: a global session's row
// dropped on a space heading files nothing, and Escape mid-drag lets a session
// go without filing it.
//
// How it could pass wrongly, and what stops it:
//   - the drop sent and refused by the verb: the heading must not be lit
//     while the global row is over it, which only the client's own check does.
//   - Escape ignored: e2e-loose, dragged onto beta and let go after Escape,
//     must stay in no space.
func TestRailDragGlobalSessionOntoHeadingChangesNothing(t *testing.T) {
	dir := artifactDir(t)
	makeGlobal := func(base string) {
		if out, err := tuiosCLI(t, base, "new", "e2e-g", "--global"); err != nil {
			t.Fatalf("create the global session: %v: %s", err, out)
		}
		if out, err := tuiosCLI(t, base, "new-window", "-s", "e2e-g"); err != nil {
			t.Fatalf("open a window in the global session: %v: %s", err, out)
		}
	}
	base, term := spaceMenusFixture(t, "e2e-a1", makeGlobal)
	waitSpaceUIRail(t, term, "the rail with the global session", "e2e-g")
	want := withSpaces(map[string]string{"e2e-g": ""})

	col, gRow := railNameAt(t, term, "e2e-g")
	betaCol, betaRow := railNameAt(t, term, "beta")
	unlit := term.Screen().Cell(betaCol, betaRow).Bg
	mousePress(t, term, col, gRow, tuitest.MouseLeft, 0)
	step := 1
	if betaRow < gRow {
		step = -1
	}
	for r := gRow + step; ; r += step {
		mouseMotion(t, term, col, r, tuitest.MouseLeft, 0)
		if r == betaRow {
			break
		}
	}
	_ = term.WaitStable(500 * time.Millisecond)
	saveArtifact(t, term, dir, "global-over-beta")
	if term.Screen().Cell(betaCol, betaRow).Bg != unlit {
		t.Fatalf("ASSERTION: beta's heading is lit under a global session's row\n%s", term.Snapshot())
	}
	mouseRelease(t, term, col, betaRow, tuitest.MouseLeft, 0)
	time.Sleep(time.Second)
	wantSessionSpaces(t, base, "after dropping the global session on beta", want)

	col, looseRow := railNameAt(t, term, "e2e-loose")
	betaCol, betaRow = railNameAt(t, term, "beta")
	mousePress(t, term, col, looseRow, tuitest.MouseLeft, 0)
	for r := looseRow - 1; r >= betaRow; r-- {
		mouseMotion(t, term, col, r, tuitest.MouseLeft, 0)
	}
	_ = term.WaitStable(500 * time.Millisecond)
	saveArtifact(t, term, dir, "loose-over-beta")
	if term.Screen().Cell(betaCol, betaRow).Bg == unlit {
		t.Fatalf("ASSERTION: beta's heading is not lit under e2e-loose's row\n%s", term.Snapshot())
	}
	sendKeys(t, term, tuitest.Esc)
	mouseRelease(t, term, col, betaRow, tuitest.MouseLeft, 0)
	time.Sleep(time.Second)
	wantSessionSpaces(t, base, "after Escape mid-drag", want)
	alive(t, term, "after the refused and cancelled drops")
}
