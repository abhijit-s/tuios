package tuie2e

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Ending a space from the client and from the CLI:
//
//   - a space heading's "Remove space" (remove_space) takes every session out
//     of the space through remove-space, the verb tuios remove-space calls, and
//     the sessions keep running;
//   - a space heading's "Close space..." (close_space) opens the close
//     confirmation on the space, and only its Close row ends the sessions,
//     through kill-space, the verb tuios kill-space --force calls.
//
// Every claim about what changed is read off tuios ls --json, which is the
// daemon's word, and the screens are saved under artifactDir.
//
// The fixture is spaceUIFixture's: e2e-loose in no space, e2e-b1 in beta,
// e2e-a1 and e2e-a2 in alpha. The client is attached to e2e-a1 unless a test
// says otherwise, so a row that acts on the attached session's space instead
// of its heading's ends the wrong space.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Remove and close a space").

// spaceEndKeys binds remove_space to ctrl+b * and close_space to ctrl+b #, both
// free in the default prefix table.
const spaceEndKeys = "\n[keybindings.prefix_mode]\nremove_space = [\"*\"]\nclose_space = [\"#\"]\n"

// spaceEndFixture is the spaced fixture with a client attached to session, in
// window management mode, with the rail drawn.
func spaceEndFixture(t *testing.T, session string) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, spaceUIRail+spaceEndKeys)
	spaceUIFixture(t, base, true)
	term := attachSpaceUI(t, base, session, 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "alpha", "beta", "e2e-loose")
	windowManagementMode(t, term)
	return base, term
}

// waitSessionSpaces waits until ls --json lists exactly want.
func waitSessionSpaces(t *testing.T, base, when string, want map[string]string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		got := sessionSpaces(t, base)
		equal := len(got) == len(want)
		for name, space := range want {
			sp, ok := got[name]
			equal = equal && ok && sp == space
		}
		if equal {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	wantSessionSpaces(t, base, when, want)
}

// without is spaceFixtureSpaces less the named sessions.
func without(names ...string) map[string]string {
	out := withSpaces(nil)
	for _, n := range names {
		delete(out, n)
	}
	return out
}

// waitCloseSpaceDialog waits for the close confirmation to name space and each
// of members.
func waitCloseSpaceDialog(t *testing.T, term *tuitest.Terminal, space string, members ...string) {
	t.Helper()
	question := "Close every session in space '" + space + "'?"
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if !strings.Contains(text, question) {
			return false
		}
		for _, m := range members {
			if !strings.Contains(text, m) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the close dialog did not ask %q naming %v: %v\n%s", question, members, err, term.Snapshot())
	}
}

// waitDialogGone waits for the close confirmation to come down.
func waitDialogGone(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Close every session in space")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s, the close dialog stayed up: %v\n%s", what, err, term.Snapshot())
	}
}

// TestSpaceHeadingRemoveSpace: "Remove space" on a heading takes that space's
// sessions out of it, they keep running, and the heading leaves the rail.
//
// How it could pass wrongly, and what stops it:
//   - the row removing the attached session's space (the bug class of a row
//     that names one thing and acts on another): the client is in alpha and
//     the heading is beta's, so alpha's members must stay in alpha.
//   - the sessions killed instead of moved: e2e-b1 must still be listed.
//   - the remove shown and never sent: ls --json is read, not the screen.
func TestSpaceHeadingRemoveSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceEndFixture(t, "e2e-a1")

	rightClickRail(t, term, "beta", "Remove space")
	saveArtifact(t, term, dir, "heading-menu")
	clickMenuRow(t, term, "Remove space")

	waitSessionSpaces(t, base, "after removing beta from its heading", withSpaces(map[string]string{"e2e-b1": ""}))
	waitRailHeadings(t, term, "after removing beta", []string{"alpha", "e2e-b1", "e2e-loose"}, []string{"beta"})
	saveArtifact(t, term, dir, "rail-removed")
	alive(t, term, "after removing a space from its heading")
}

// TestRemoveSpaceKeyRemovesTheAttachedSessionsSpace: remove_space reached by
// key takes every session out of the attached session's space, the client
// stays on its session, and no other space changes.
//
// How it could pass wrongly, and what stops it:
//   - only the attached session taken out: alpha has two members, and e2e-a2,
//     which no client shows, must leave it too.
//   - every space removed: e2e-b1 must stay in beta.
//   - the client thrown off its session: it must stay alive.
func TestRemoveSpaceKeyRemovesTheAttachedSessionsSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceEndFixture(t, "e2e-a1")

	sendKeys(t, term, tuitest.Ctrl('b'), "*")
	waitSessionSpaces(t, base, "after remove_space by key", withSpaces(map[string]string{"e2e-a1": "", "e2e-a2": ""}))
	waitRailHeadings(t, term, "after removing alpha", []string{"beta", "e2e-a1", "e2e-a2"}, []string{"alpha"})
	saveArtifact(t, term, dir, "rail-removed")
	alive(t, term, "after removing the attached session's space")
}

// TestRemoveSpaceWithAnOlderDaemon: attached to a daemon from before
// remove-space, remove_space still takes every session out of the space, one
// set-session-space at a time, so a client upgrade needs no daemon restart for
// it.
//
// How it could pass wrongly, and what stops it:
//   - the daemon is the new build after all: the fixture's CLI starts it from
//     the baseline, which has no remove-space verb.
//   - only the attached session taken out: e2e-a2, which no client shows, must
//     leave alpha too.
//   - the stale-daemon notice shown and nothing done: the spaces must change.
func TestRemoveSpaceWithAnOlderDaemon(t *testing.T) {
	baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN")
	if baseline == "" {
		t.Skip("TUIOS_E2E_BASELINE_BIN is not set: build tuios from a commit with spaces and no remove-space, and name it there")
	}
	dir := artifactDir(t)
	prev := tuiosBin
	tuiosBin = baseline
	restore := func() { tuiosBin = prev }
	defer restore()
	base := t.TempDir()
	writeConfig(t, base, spaceUIRail+spaceEndKeys)
	spaceUIFixture(t, base, true)
	restore()

	term := attachSpaceUI(t, base, "e2e-a1", 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "alpha", "beta", "e2e-loose")
	windowManagementMode(t, term)

	sendKeys(t, term, tuitest.Ctrl('b'), "*")
	waitSessionSpaces(t, base, "after remove_space on an older daemon", withSpaces(map[string]string{"e2e-a1": "", "e2e-a2": ""}))
	saveArtifact(t, term, dir, "removed-on-older-daemon")
	if strings.Contains(term.Screen().Text(), "older than this tuios") {
		t.Fatalf("ASSERTION: the stale-daemon notice showed although the fallback worked\n%s", term.Snapshot())
	}
	alive(t, term, "after removing a space on an older daemon")
}

// TestSpaceKeysWithNoSpaceSaySo: in a session in no space, remove_space and
// close_space each say why they do nothing, and nothing changes.
//
// How it could pass wrongly, and what stops it:
//   - the keys unbound: an unbound key also does nothing. The notices are what
//     only the actions show.
//   - a dialog opened on some other space: no close question may show.
func TestSpaceKeysWithNoSpaceSaySo(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceEndFixture(t, "e2e-loose")

	sendKeys(t, term, tuitest.Ctrl('b'), "*")
	if err := term.WaitForText("No space to remove", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: remove_space on a session in no space never said so: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('b'), "#")
	if err := term.WaitForText("No space to close", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: close_space on a session in no space never said so: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "no-space")
	if strings.Contains(term.Screen().Text(), "Close every session in space") {
		t.Fatalf("ASSERTION: a close dialog opened for a session in no space\n%s", term.Snapshot())
	}
	wantSessionSpaces(t, base, "after the space keys on a session in no space", spaceFixtureSpaces)
	alive(t, term, "after the space keys with no space")
}

// TestSpaceHeadingCloseSpaceConfirms: "Close space..." on a heading asks first,
// naming the heading's sessions; Esc and Enter on the row it opens on change
// nothing, and the Close row ends that space's sessions and no other. The
// client is in another space and stays where it is.
//
// How it could pass wrongly, and what stops it:
//   - a kill without the confirmation: the dialog must be up before anything
//     dies, and Esc must leave every session alive.
//   - the destructive row as the default: Enter on the freshly opened dialog
//     must kill nothing.
//   - the row closing the attached session's space: the client is in alpha and
//     the heading is beta's; the dialog must name e2e-b1, and alpha's members
//     must survive.
//   - sessions in no space or another space caught up: e2e-loose, e2e-a1 and
//     e2e-a2 must stay, in their spaces.
func TestSpaceHeadingCloseSpaceConfirms(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceEndFixture(t, "e2e-a1")

	openBetaClose := func() {
		rightClickRail(t, term, "beta", "Close space")
		clickMenuRow(t, term, "Close space")
		waitCloseSpaceDialog(t, term, "beta", "1 session: e2e-b1")
	}

	openBetaClose()
	saveArtifact(t, term, dir, "dialog")
	sendKeys(t, term, tuitest.Esc)
	waitDialogGone(t, term, "after Esc")
	wantSessionSpaces(t, base, "after Esc on the close dialog", spaceFixtureSpaces)

	openBetaClose()
	sendKeys(t, term, tuitest.Enter)
	waitDialogGone(t, term, "after Enter on Cancel")
	time.Sleep(500 * time.Millisecond)
	wantSessionSpaces(t, base, "after Enter on the row the dialog opens on", spaceFixtureSpaces)

	openBetaClose()
	sendKeys(t, term, tuitest.Down, tuitest.Enter)
	waitSessionSpaces(t, base, "after closing beta from its heading", without("e2e-b1"))
	waitRailHeadings(t, term, "after closing beta", []string{"alpha", "e2e-a1", "e2e-loose"}, []string{"beta", "e2e-b1"})
	saveArtifact(t, term, dir, "rail-closed")
	alive(t, term, "after closing another space from its heading")
}

// TestCloseSpaceKeyQuitsWhenTheClientIsInIt: close_space reached by key asks
// about the attached session's space with the rail shut and one zoomed pane
// on screen, says the client will go with it, and Close ends every session of the
// space and then this client, as closing the attached session does.
//
// How it could pass wrongly, and what stops it:
//   - the dialog never drawn over a lone pane (the compositor's fast path
//     draws no overlay it does not know of): the question must be on screen.
//   - only the attached session killed: e2e-a2 must go too.
//   - other spaces caught up: e2e-b1 and e2e-loose must stay.
//   - the client left on a dead session: it must exit, with code 0, since the
//     person asked for it.
func TestCloseSpaceKeyQuitsWhenTheClientIsInIt(t *testing.T) {
	dir := artifactDir(t)
	base := t.TempDir()
	writeConfig(t, base, spaceUINoRail+spaceEndKeys)
	spaceUIFixture(t, base, true)
	term := attachSpaceUI(t, base, "e2e-a1", 140, 40)
	windowManagementMode(t, term)
	// Zoomed, the one pane fills the content area: the frame the compositor's
	// fast path draws, which skips every overlay it is not told about.
	sendKeys(t, term, "z")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.HasPrefix(s.Line(0), "╭")
	}, uiTimeout); err != nil {
		t.Fatalf("the pane never filled the screen: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(time.Second)

	sendKeys(t, term, tuitest.Ctrl('b'), "#")
	waitCloseSpaceDialog(t, term, "alpha", "2 sessions: e2e-a1, e2e-a2", "This client quits")
	saveArtifact(t, term, dir, "dialog")
	wantSessionSpaces(t, base, "with the close dialog up", spaceFixtureSpaces)

	sendKeys(t, term, tuitest.Down, tuitest.Enter)
	code, err := term.WaitExit(uiTimeout)
	if err != nil {
		t.Fatalf("ASSERTION: the client did not quit after closing its own space: %v\n%s", err, term.Snapshot())
	}
	if code != 0 {
		t.Fatalf("ASSERTION: the client exited with code %d after closing its own space, want 0\n%s", code, term.Snapshot())
	}
	waitSessionSpaces(t, base, "after closing alpha by key", without("e2e-a1", "e2e-a2"))
}

// TestRemoveSpaceCLI: tuios remove-space takes every session of a space out
// of it and no other, they keep running, and running it again after a remove
// that stopped part way finishes it.
//
// How it could pass wrongly, and what stops it:
//   - only the first member taken out: the space has two.
//   - every space removed: a session in another space must stay in it.
//   - the sessions killed: ls must still list them.
//   - a missing space answered as success: it must be refused, naming the
//     spaces there are.
//   - a split left by a crash not repaired: one member is put back by hand,
//     and the same remove must take it out.
func TestRemoveSpaceCLI(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	newInSpace(t, base, "e2e-a", "payments")
	newInSpace(t, base, "e2e-b", "payments")
	newInSpace(t, base, "e2e-c", "search")
	if out, err := tuiosCLI(t, base, "new", "e2e-loose", "--detach"); err != nil {
		t.Fatalf("create e2e-loose: %v: %s", err, out)
	}
	after := map[string]string{"e2e-a": "", "e2e-b": "", "e2e-c": "search", "e2e-loose": ""}

	if out, err := tuiosCLI(t, base, "remove-space", "nowhere"); err == nil || !strings.Contains(out, "payments") {
		t.Fatalf("ASSERTION: remove-space of a missing space was not refused with the spaces there are: %v: %s", err, out)
	}
	wantSessionSpaces(t, base, "after the refused remove", map[string]string{"e2e-a": "payments", "e2e-b": "payments", "e2e-c": "search", "e2e-loose": ""})

	out, err := tuiosCLI(t, base, "remove-space", "payments")
	if err != nil {
		t.Fatalf("ASSERTION: remove-space failed: %v: %s", err, out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Fatalf("ASSERTION: remove-space did not report 2 sessions: %s", out)
	}
	wantSessionSpaces(t, base, "after remove-space", after)

	if out, err := tuiosCLI(t, base, "remove-space", "payments"); err == nil {
		t.Fatalf("ASSERTION: remove-space of a space with no session left succeeded: %s", out)
	}

	// What a crash part way through leaves: one member still in the space.
	setSpace(t, base, "e2e-b", "payments")
	out, err = tuiosCLI(t, base, "remove-space", "payments", "--json")
	if err != nil {
		t.Fatalf("ASSERTION: the remove that repairs a split failed: %v: %s", err, out)
	}
	var res struct {
		Space    string   `json:"space"`
		Sessions []string `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Space != "payments" || !slices.Equal(res.Sessions, []string{"e2e-b"}) {
		t.Fatalf("ASSERTION: the repairing remove reported %+v (%v), want payments and [e2e-b]: %s", res, err, out)
	}
	wantSessionSpaces(t, base, "after the remove repaired a split", after)
}
