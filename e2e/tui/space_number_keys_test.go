package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Switching space by its number: switch_space_1 .. switch_space_9 jump
// straight to the Nth named space in the rail's order, landing on the session
// of it used last, as next_space does. The sessions in no space are not
// numbered. Every landing is read from the daemon's own list of clients, by
// the client process's pid, and the screens are saved under artifactDir.

// spaceNumberKeys binds three of the numbered space actions to direct chords.
const spaceNumberKeys = "\n[keybindings.window_management]\n" +
	"switch_space_1 = [\"alt+shift+q\"]\n" +
	"switch_space_2 = [\"alt+shift+w\"]\n" +
	"switch_space_3 = [\"alt+shift+e\"]\n"

// spaceNumberFixture makes five detached sessions in a fresh daemon:
// e2e-loose in no space, e2e-b2 and then e2e-b1 in beta, e2e-a1 and e2e-a2 in
// alpha, and then uses e2e-b2 and e2e-a2. So beta's session used last is not
// the one made last, and alpha's is not the first the rail draws.
func spaceNumberFixture(t *testing.T, base string) {
	t.Helper()
	killDaemon(t, base)
	sessions := []struct{ name, space string }{
		{"e2e-loose", ""}, {"e2e-b2", "beta"}, {"e2e-b1", "beta"}, {"e2e-a1", "alpha"}, {"e2e-a2", "alpha"},
	}
	for _, s := range sessions {
		args := []string{"new", s.name, "--detach"}
		if s.space != "" {
			args = append(args, "--space", s.space)
		}
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("create %s: %v: %s", s.name, err, out)
		}
	}
	typeInSession(t, base, "e2e-b2", 0)
	typeInSession(t, base, "e2e-a2", 1)
}

// pressSpaceNumber sends the chord bound to switch_space_N, written as the
// shifted letter Alt sends.
func pressSpaceNumber(t *testing.T, term *tuitest.Terminal, letter rune) {
	t.Helper()
	if err := term.SendKeys(tuitest.Alt(letter)); err != nil {
		t.Fatalf("press alt+%c: %v", letter, err)
	}
}

// TestSwitchSpaceByNumber: from e2e-loose, in no space, switch_space_2 lands
// on beta's session used last (e2e-b2), switch_space_1 on alpha's (e2e-a2),
// and switch_space_1 again, pressed in alpha after e2e-a1 became its session
// used last, stays put.
//
// How it could pass wrongly, and what stops it:
//   - the number ignored, always the first space: switch_space_2 must reach
//     beta, which sorts after alpha.
//   - the sessions in no space counted first: 2 would then be alpha.
//   - the client picking a session itself rather than the daemon's pick by
//     use: alpha's first row is e2e-a1 and beta's session made last is
//     e2e-b1, and the landings must be e2e-a2 and e2e-b2.
//   - the key walking spaces like next_space: from none, next_space wraps to
//     alpha, not beta.
//   - the presses landing in terminal mode: a switch can leave the client
//     there, so each press is made after entering window-management mode.
//
// The third press is the "already there" half. It cannot tell a re-pick from
// no pick: the press is itself a use of e2e-a2, so pick-space would answer
// e2e-a2 anyway. It holds the behaviour, not the check behind it.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Numbered space switching").
func TestSwitchSpaceByNumber(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail+spaceNumberKeys)
	spaceNumberFixture(t, base)

	term := attachSpaceUI(t, base, "e2e-loose", 120, 40)
	windowManagementMode(t, term)
	pressSpaceNumber(t, term, 'W')
	clientOnPid(t, base, term.Pid(), "e2e-b2")
	settleAfterSwitch(t, term)
	saveArtifact(t, term, dir, "space-2-beta")

	windowManagementMode(t, term)
	pressSpaceNumber(t, term, 'Q')
	clientOnPid(t, base, term.Pid(), "e2e-a2")
	settleAfterSwitch(t, term)
	saveArtifact(t, term, dir, "space-1-alpha")

	typeInSession(t, base, "e2e-a1", 2)
	windowManagementMode(t, term)
	pressSpaceNumber(t, term, 'Q')
	stillOnPid(t, base, term.Pid(), "e2e-a2", "switch_space_1 pressed in alpha")
	saveArtifact(t, term, dir, "space-1-again")
	alive(t, term, "after switching spaces by number")
}

// TestSwitchSpaceNumberPastTheEnd: with two named spaces, switch_space_3 says
// "No space 3" and leaves the client where it is, and with no space at all it
// says "No spaces".
//
// How it could pass wrongly, and what stops it:
//   - the key unbound or unregistered: that also moves nothing. The notices
//     are what only the action shows.
//   - the sessions in no space numbered too: 3 would be them, and the client
//     would move to e2e-loose.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Numbered space switching").
func TestSwitchSpaceNumberPastTheEnd(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail+spaceNumberKeys)
	spaceNumberFixture(t, base)

	term := attachSpaceUI(t, base, "e2e-a1", 120, 40)
	windowManagementMode(t, term)
	pressSpaceNumber(t, term, 'E')
	if err := term.WaitForText("No space 3", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: switch_space_3 with two spaces never said so: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "no-space-3")
	stillOnPid(t, base, term.Pid(), "e2e-a1", "switch_space_3 with two spaces")

	// A daemon with no session in any space.
	bare := t.TempDir()
	writeConfig(t, bare, spaceUINoRail+spaceNumberKeys)
	spaceUIFixture(t, bare, false)
	term = attachSpaceUI(t, bare, "e2e-a1", 120, 40)
	windowManagementMode(t, term)
	pressSpaceNumber(t, term, 'Q')
	if err := term.WaitForText("No spaces", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: switch_space_1 with no space never said so: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "no-spaces")
	stillOnPid(t, bare, term.Pid(), "e2e-a1", "switch_space_1 with no space")
}

// TestSpaceNumbersOnTheRail: with show_numbers on, the grouped rail leads
// alpha's heading with 1 and beta's with 2, and the split rail's spaces
// section leads their rows the same way while its none row wears no number.
// The heading's menu offers the switch with the bound key as its hint, and
// taking the row on beta lands on beta's session used last.
//
// How it could pass wrongly, and what stops it:
//   - a number drawn in the session's name rather than on the heading: the
//     numbers are read on the rows that name the spaces.
//   - the sessions in no space numbered: the split rail's none row must wear
//     no 3.
//   - a menu row naming an action the dispatcher does not have: the row is
//     taken, and the client must move.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Numbered space switching").
func TestSpaceNumbersOnTheRail(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	numbered := spaceUIRail + "show_numbers = true\n"
	writeConfig(t, base, numbered+spaceNumberKeys)
	spaceNumberFixture(t, base)

	term := attachSpaceUI(t, base, "e2e-a1", 140, 40)
	waitSpaceUIRail(t, term, "the grouped rail", "1 alpha", "2 beta", "e2e-loose")
	saveArtifact(t, term, dir, "grouped-numbers")

	windowManagementMode(t, term)
	rightClickRail(t, term, "beta", "Switch to space")
	_, row := findOnScreen(t, term, "Switch to space")
	if line := term.Screen().Line(row); !strings.Contains(strings.ToLower(line), "alt") {
		t.Fatalf("ASSERTION: the Switch to space row shows no key hint: %q\n%s", line, term.Snapshot())
	}
	saveArtifact(t, term, dir, "heading-menu")
	clickMenuRow(t, term, "Switch to space")
	clientOnPid(t, base, term.Pid(), "e2e-b2")
	settleAfterSwitch(t, term)

	writeConfig(t, base, spaceLayoutRail("split")+"show_numbers = true\n"+spaceNumberKeys)
	split := attachSpaceUI(t, base, "e2e-loose", 140, 40)
	waitSpaceUIRail(t, split, "the split rail", "spaces", "1 alpha", "2 beta", "none")
	s := split.Screen()
	saveArtifact(t, split, dir, "split-numbers")
	none := spaceRowOf(s, "none")
	if none < 0 {
		t.Fatalf("no none row in the spaces section\n%s", split.Snapshot())
	}
	if f := strings.Fields(railRowText(s, none)); len(f) < 2 || f[1] != "none" {
		t.Fatalf("ASSERTION: the none row is not drawn as glyph then none, unnumbered: %q\n%s", railRowText(s, none), split.Snapshot())
	}
	alive(t, term, "after the numbered rail")
	alive(t, split, "after the numbered split rail")
}

// TestSwitchSpaceNumberInTerminalMode: switch_space_2 on its chord works while
// a shell has the keys, twice in a row: from e2e-loose to beta (e2e-b2), then
// switch_space_1 to alpha (e2e-a2).
//
// How it could pass wrongly, and what stops it:
//   - the presses made in window-management mode: each is made after entering
//     terminal mode.
//   - the chord reaching the shell instead: the client would stay put.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Numbered space switching").
func TestSwitchSpaceNumberInTerminalMode(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceUINoRail+spaceNumberKeys)
	spaceNumberFixture(t, base)

	term := attachSpaceUI(t, base, "e2e-loose", 120, 40)
	for _, step := range []struct {
		letter rune
		want   string
	}{{'W', "e2e-b2"}, {'Q', "e2e-a2"}} {
		windowManagementMode(t, term)
		enterTerminalMode(t, term)
		pressSpaceNumber(t, term, step.letter)
		clientOnPid(t, base, term.Pid(), step.want)
		settleAfterSwitch(t, term)
		saveArtifact(t, term, dir, "terminal-"+step.want)
	}
	alive(t, term, "after the numbered space chord in terminal mode")
}
