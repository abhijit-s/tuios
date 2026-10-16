package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The space rename dialog: the rename_space action and the Rename row of a
// space heading's menu on the rail. Both open the one rename dialog seeded with
// the space's name, and both commit through the daemon's rename-space verb, the
// one tuios rename-space calls. Every claim about what was renamed is read off
// tuios ls --json, which is the daemon's word, and the rail is read for what a
// person sees. The screens are saved under artifactDir.
//
// The fixture is spaceUIFixture's: e2e-loose in no space, e2e-b1 in beta,
// e2e-a1 and e2e-a2 in alpha.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Space rename dialog").

// spaceRenameConfig opens the rail and binds rename_space to ctrl+b %, which
// the default config leaves unbound.
const spaceRenameConfig = spaceUIRail + "\n[keybindings.prefix_mode]\nrename_space = [\"%\"]\n"

// spaceRenameFixture is the spaced fixture with a client attached to session,
// in window management mode, with the rail drawn.
func spaceRenameFixture(t *testing.T, session string) (string, *tuitest.Terminal) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, spaceRenameConfig)
	spaceUIFixture(t, base, true)
	term := attachSpaceUI(t, base, session, 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "alpha", "beta", "e2e-loose")
	windowManagementMode(t, term)
	return base, term
}

// openSpaceRenameByKey presses the bound key and waits for the dialog to name
// the space it opened on.
func openSpaceRenameByKey(t *testing.T, term *tuitest.Terminal, space string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "%")
	if err := term.WaitForText("rename space "+space, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+b %% did not open the dialog on space %s: %v\n%s", space, err, term.Snapshot())
	}
}

// typeSpaceName replaces the dialog's seed, old, with name and commits it.
func typeSpaceName(t *testing.T, term *tuitest.Terminal, old, name string) {
	t.Helper()
	sendKeys(t, term, strings.Repeat("\x7f", len([]rune(old))), name)
	if err := term.WaitForText(name, uiTimeout); err != nil {
		t.Fatalf("the dialog did not take %q: %v\n%s", name, err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Enter)
}

// waitRailHeadings waits until the rail draws every name in want and none in
// gone.
func waitRailHeadings(t *testing.T, term *tuitest.Terminal, what string, want, gone []string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, n := range want {
			if railRowOf(s, n) < 0 {
				return false
			}
		}
		for _, n := range gone {
			if railRowOf(s, n) >= 0 {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s: the rail never drew %v without %v: %v\n%s", what, want, gone, err, term.Snapshot())
	}
}

// TestRenameSpaceKeyRenamesTheAttachedSessionsSpace: rename_space reached by
// key renames the space of the session the client is attached to, and every
// member of it moves.
//
// How it could pass wrongly, and what stops it:
//   - the dialog renaming only the attached session's space field: alpha has
//     two members, and e2e-a2, which no client shows, must move too.
//   - every session moved: e2e-b1 must stay in beta and e2e-loose in none.
//   - the rename shown and never sent: ls --json is read, not the screen.
//   - the rail left on the old name: the heading must read gamma, and alpha
//     must be gone from the rail.
func TestRenameSpaceKeyRenamesTheAttachedSessionsSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceRenameFixture(t, "e2e-a1")

	openSpaceRenameByKey(t, term, "alpha")
	saveArtifact(t, term, dir, "dialog-alpha")
	typeSpaceName(t, term, "alpha", "gamma")

	waitRailHeadings(t, term, "after renaming alpha to gamma", []string{"gamma", "beta", "e2e-a1", "e2e-a2"}, []string{"alpha"})
	saveArtifact(t, term, dir, "rail-gamma")
	wantSessionSpaces(t, base, "after renaming alpha to gamma by key", map[string]string{
		"e2e-loose": "", "e2e-b1": "beta", "e2e-a1": "gamma", "e2e-a2": "gamma",
	})
	alive(t, term, "after renaming a space by key")
}

// TestRailSpaceHeadingMenuRenamesThatSpace: the Rename row of a heading's menu
// renames the heading's space, which here is not the attached session's.
//
// How it could pass wrongly, and what stops it:
//   - the row renaming the attached session's space, the bug class of a row
//     that names one thing and acts on another: the client is in alpha and the
//     heading clicked is beta's. The dialog must name beta, and alpha's
//     members must stay in alpha.
//   - the right-click opening the rail's settings, as it did before the
//     heading had a menu: the menu must offer Rename.
func TestRailSpaceHeadingMenuRenamesThatSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceRenameFixture(t, "e2e-a1")

	s := term.Screen()
	row := railRowOf(s, "beta")
	col := nameColOf(s, row, "beta")
	if col < 0 {
		t.Fatalf("beta's heading is not on the rail\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Rename", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the beta heading's menu offered no Rename: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "heading-menu")

	col, row = findOnScreen(t, term, "Rename")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("rename space beta", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dialog did not name the heading's space: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "dialog-beta")
	typeSpaceName(t, term, "beta", "ops")

	waitRailHeadings(t, term, "after renaming beta to ops", []string{"ops", "alpha", "e2e-b1"}, []string{"beta"})
	saveArtifact(t, term, dir, "rail-ops")
	wantSessionSpaces(t, base, "after renaming beta from its heading", map[string]string{
		"e2e-loose": "", "e2e-b1": "ops", "e2e-a1": "alpha", "e2e-a2": "alpha",
	})
	alive(t, term, "after renaming a space from its heading")
}

// TestRenameSpaceDialogMergesIntoAnExistingSpace: renaming alpha to beta, a
// name a space already holds, files alpha's members under beta beside
// e2e-b1, as tuios rename-space does.
//
// How it could pass wrongly, and what stops it:
//   - a refusal of a name in use: every alpha member must end in beta.
//   - only the attached member moving: e2e-a2 must move too.
//   - two beta headings: the rail must draw beta once, over all three.
func TestRenameSpaceDialogMergesIntoAnExistingSpace(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceRenameFixture(t, "e2e-a1")

	openSpaceRenameByKey(t, term, "alpha")
	typeSpaceName(t, term, "alpha", "beta")

	waitRailHeadings(t, term, "after merging alpha into beta", []string{"beta", "e2e-a1", "e2e-a2", "e2e-b1"}, []string{"alpha"})
	wantSessionSpaces(t, base, "after merging alpha into beta", map[string]string{
		"e2e-loose": "", "e2e-b1": "beta", "e2e-a1": "beta", "e2e-a2": "beta",
	})
	_ = term.WaitStable(time.Second)
	s := term.Screen()
	saveArtifact(t, term, dir, "rail-merged")
	headings := 0
	_, rows := s.Size()
	for r := range rows {
		if col := nameColOf(s, r, "beta"); col >= 0 && strings.Contains(railLine(s, r), "▾") {
			headings++
		}
	}
	if headings != 1 {
		t.Fatalf("ASSERTION: the rail draws %d beta headings after the merge, want 1\n%s", headings, term.Snapshot())
	}
	alive(t, term, "after merging spaces from the dialog")
}

// TestRenameSpaceDialogRefusesAnInvalidName: a name the verb refuses is refused
// with the verb's own words, and nothing moves.
//
// How it could pass wrongly, and what stops it:
//   - the refusal swallowed: the notice must carry the verb's message.
//   - the name applied anyway: ls --json must be as it was.
func TestRenameSpaceDialogRefusesAnInvalidName(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceRenameFixture(t, "e2e-a1")

	openSpaceRenameByKey(t, term, "alpha")
	typeSpaceName(t, term, "alpha", "bad/name")
	if err := term.WaitForText("Rename failed: a space name follows the session", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the refusal did not carry the verb's message: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "refused")
	wantSessionSpaces(t, base, "after a refused rename", map[string]string{
		"e2e-loose": "", "e2e-b1": "beta", "e2e-a1": "alpha", "e2e-a2": "alpha",
	})
	alive(t, term, "after a refused space rename")
}

// TestRenameSpaceKeyWithNoSpaceSaysSo: on a session in no space rename_space
// opens no dialog, says why, and changes nothing.
//
// How it could pass wrongly, and what stops it:
//   - the key unbound: an unbound key also opens nothing. The notice is what
//     only the action shows.
//   - a dialog opened on some other space: no "rename space" may show.
func TestRenameSpaceKeyWithNoSpaceSaysSo(t *testing.T) {
	dir := artifactDir(t)
	base, term := spaceRenameFixture(t, "e2e-loose")

	sendKeys(t, term, tuitest.Ctrl('b'), "%")
	if err := term.WaitForText("No space to rename", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: rename_space on a session in no space never said so: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "no-space")
	if strings.Contains(term.Screen().Text(), "rename space") {
		t.Fatalf("ASSERTION: a dialog opened for a session in no space\n%s", term.Snapshot())
	}
	wantSessionSpaces(t, base, "after rename_space on a session in no space", map[string]string{
		"e2e-loose": "", "e2e-b1": "beta", "e2e-a1": "alpha", "e2e-a2": "alpha",
	})
	alive(t, term, "after rename_space with no space")
}
