package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A workspace's number is its position. Moving one, by the move keys or by
// dragging its dock pill, renumbers it: the pill's {index}, opt+N, the daemon's
// numbering and a second client all follow the place it was moved to. A herdr
// tab id is the one thing that does not follow the position: it names the tab,
// so an id read before a move names the same tab after it.
//
// The session under test holds three workspaces with different pane counts, so
// the dock's "<workspace>:<count>" status says which one is on screen without
// reading a name: EDIT holds one pane, REVW two and DPLY three.

const renumberSession = "renum"

// renumberConfig shows the number on every pill, the way the report's config
// does, and binds the move keys under the workspace prefix.
const renumberConfig = `[appearance]
dock_workspace_tab_format = '{index}·{name}'

[keybindings.workspace_prefix]
move_workspace_left = ['<']
move_workspace_right = ['>']
`

// renumberPanes is how many panes each named workspace holds.
var renumberPanes = map[string]int{"EDIT": 1, "REVW": 2, "DPLY": 3}

// renumberSetup builds the three workspaces through the daemon and attaches a
// client to workspace 1.
func renumberSetup(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, renumberConfig)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", renumberSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	for _, ws := range []struct {
		num, name string
	}{{"1", "EDIT"}, {"2", "REVW"}, {"3", "DPLY"}} {
		have := 0
		if ws.num == "1" {
			have = 1
		}
		for i := have; i < renumberPanes[ws.name]; i++ {
			if out, err := tuiosCLI(t, base, "new-window", "-s", renumberSession, "--workspace", ws.num, "--no-focus"); err != nil {
				t.Fatalf("new-window on %s: %v: %s", ws.num, err, out)
			}
		}
		if out, err := tuiosCLI(t, base, "set-workspace-name", ws.num, ws.name, "--session", renumberSession); err != nil {
			t.Fatalf("name workspace %s: %v: %s", ws.num, err, out)
		}
	}
	term := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, term, "1·EDIT", "2·REVW", "3·DPLY")
	return term, base
}

// waitPills waits for the dock to draw every one of the labels.
func waitPills(t *testing.T, term *tuitest.Terminal, labels ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, labels...)
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never drew %v: %v\n%s", labels, err, term.Snapshot())
	}
}

// renumberInfo is what session-info says about the numbering.
type renumberInfo struct {
	CurrentWorkspace int               `json:"current_workspace"`
	WorkspaceNames   map[string]string `json:"workspace_names"`
	WorkspaceOrder   []int             `json:"workspace_order"`
}

// wantNumbering checks the daemon's numbering: the name each number holds, and
// that no display order is left over beside it.
func wantNumbering(t *testing.T, base string, want map[string]string) renumberInfo {
	t.Helper()
	info, err := daemonJSON[renumberInfo](base, "session-info", "-s", renumberSession)
	if err != nil {
		t.Fatalf("session-info: %v", err)
	}
	for num, name := range want {
		if info.WorkspaceNames[num] != name {
			t.Fatalf("the daemon has workspace %s named %q, want %q (names %v)", num, info.WorkspaceNames[num], name, info.WorkspaceNames)
		}
	}
	if len(info.WorkspaceOrder) > 0 {
		t.Fatalf("the daemon kept a display order %v beside the numbering", info.WorkspaceOrder)
	}
	return info
}

// wantPanesOn checks how many panes the daemon holds on each named workspace,
// so a renumber is seen to have moved the panes and not only the names.
func wantPanesOn(t *testing.T, base string, want map[int]string) {
	t.Helper()
	out, err := tuiosOut(base, "list-windows", "-s", renumberSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var payload struct {
		WorkspaceWindows []int `json:"workspace_windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	got := payload.WorkspaceWindows
	for num, name := range want {
		if num > len(got) || got[num-1] != renumberPanes[name] {
			t.Fatalf("workspace %d holds the wrong panes, want %s's %d (by workspace %v)", num, name, renumberPanes[name], got)
		}
	}
}

// altReaches presses opt+N and checks the workspace that comes up is the one
// called name, by its pane count.
func altReaches(t *testing.T, term *tuitest.Terminal, n rune, name string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Alt(n)); err != nil {
		t.Fatalf("press alt+%c: %v", n, err)
	}
	status := fmt.Sprintf("%c:%d", n, renumberPanes[name])
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == status
	}, uiTimeout); err != nil {
		t.Fatalf("alt+%c did not reach %s (%d panes): %v\n%s", n, name, renumberPanes[name], err, term.Snapshot())
	}
}

// renumberDockStatus is the dock's "<workspace>:<count>" status field, read the way
// countWindows reads it, or nil when the dock is not on screen.
func renumberDockStatus(s tuitest.Screen) []string {
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-3); r-- {
		if m := dockStatus.FindStringSubmatch(s.Line(r)); m != nil {
			return m
		}
	}
	return nil
}

// moveWorkspaceKey presses the bound move key under the workspace prefix.
func moveWorkspaceKey(t *testing.T, term *tuitest.Terminal, key string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", key); err != nil {
		t.Fatalf("press the move key %s: %v", key, err)
	}
}

// dragPill drags the pill labelled from onto the pill labelled onto.
func dragPill(t *testing.T, term *tuitest.Terminal, from, onto string, all ...string) {
	t.Helper()
	row, line := dockPillRow(t, term, all...)
	mouseDrag(t, term, pillColumn(line, from)+1, row, pillColumn(line, onto)+1, row, tuitest.MouseLeft, 0)
}

// herdrTabIDByLabel is the herdr tab id of the tab labelled label.
func herdrTabIDByLabel(t *testing.T, base, label string) string {
	t.Helper()
	tabs := herdrCall(t, base, "tab.list", map[string]any{})["tabs"].([]any)
	for _, raw := range tabs {
		if tab := raw.(map[string]any); tab["label"] == label {
			return tab["tab_id"].(string)
		}
	}
	t.Fatalf("no herdr tab labelled %s in %v", label, tabs)
	return ""
}

// wantHerdrTab checks that a tab id still names the tab labelled label.
func wantHerdrTab(t *testing.T, base, id, label string) {
	t.Helper()
	tab := herdrCall(t, base, "tab.get", map[string]any{"tab_id": id})["tab"].(map[string]any)
	if tab["label"] != label {
		t.Fatalf("herdr tab %s names %v after the move, want %s", id, tab["label"], label)
	}
}

// TestWorkspaceKeyMoveRenumbers is the move key: moving EDIT right swaps it
// with REVW, the pills read 1·REVW 2·EDIT, opt+N reaches each one by its new
// number, and the herdr tab ids read before the move still name the same tabs.
//
// Negative control: see NEGATIVE_CONTROLS.md.
func TestWorkspaceKeyMoveRenumbers(t *testing.T) {
	term, base := renumberSetup(t)
	editID := herdrTabIDByLabel(t, base, "EDIT")
	revwID := herdrTabIDByLabel(t, base, "REVW")

	moveWorkspaceKey(t, term, ">")
	waitPills(t, term, "1·REVW", "2·EDIT", "3·DPLY")
	info := wantNumbering(t, base, map[string]string{"1": "REVW", "2": "EDIT", "3": "DPLY"})
	if info.CurrentWorkspace != 2 {
		t.Fatalf("the moved workspace is not followed: current is %d, want 2", info.CurrentWorkspace)
	}
	wantPanesOn(t, base, map[int]string{1: "REVW", 2: "EDIT", 3: "DPLY"})
	wantHerdrTab(t, base, editID, "EDIT")
	wantHerdrTab(t, base, revwID, "REVW")

	altReaches(t, term, '1', "REVW")
	altReaches(t, term, '2', "EDIT")
	altReaches(t, term, '3', "DPLY")

	// And back, from the other end: DPLY to the left swaps it with EDIT.
	moveWorkspaceKey(t, term, "<")
	waitPills(t, term, "1·REVW", "2·DPLY", "3·EDIT")
	wantNumbering(t, base, map[string]string{"1": "REVW", "2": "DPLY", "3": "EDIT"})
	wantPanesOn(t, base, map[int]string{1: "REVW", 2: "DPLY", 3: "EDIT"})
	altReaches(t, term, '3', "EDIT")
	wantHerdrTab(t, base, editID, "EDIT")
	alive(t, term, "after moving workspaces by key")
}

// TestWorkspaceDragLeftRenumbers drags DPLY's pill onto EDIT's, the first one:
// DPLY becomes workspace 1 and the two it passed move up one.
func TestWorkspaceDragLeftRenumbers(t *testing.T) {
	term, base := renumberSetup(t)
	dpltID := herdrTabIDByLabel(t, base, "DPLY")

	dragPill(t, term, "3·DPLY", "1·EDIT", "1·EDIT", "2·REVW", "3·DPLY")
	waitPills(t, term, "1·DPLY", "2·EDIT", "3·REVW")
	wantNumbering(t, base, map[string]string{"1": "DPLY", "2": "EDIT", "3": "REVW"})
	wantPanesOn(t, base, map[int]string{1: "DPLY", 2: "EDIT", 3: "REVW"})
	wantHerdrTab(t, base, dpltID, "DPLY")

	altReaches(t, term, '1', "DPLY")
	altReaches(t, term, '2', "EDIT")
	altReaches(t, term, '3', "REVW")
	alive(t, term, "after dragging a pill left")
}

// TestWorkspaceDragThenKeyMove drags first and moves by key second: the key
// moves the workspace past the neighbour drawn beside it.
func TestWorkspaceDragThenKeyMove(t *testing.T) {
	term, base := renumberSetup(t)

	dragPill(t, term, "3·DPLY", "1·EDIT", "1·EDIT", "2·REVW", "3·DPLY")
	waitPills(t, term, "1·DPLY", "2·EDIT", "3·REVW")

	// EDIT is on screen, now as workspace 2, and the pill left of it is DPLY's.
	altReaches(t, term, '2', "EDIT")
	moveWorkspaceKey(t, term, "<")
	waitPills(t, term, "1·EDIT", "2·DPLY", "3·REVW")
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "DPLY", "3": "REVW"})
	wantPanesOn(t, base, map[int]string{1: "EDIT", 2: "DPLY", 3: "REVW"})
	altReaches(t, term, '2', "DPLY")
	altReaches(t, term, '1', "EDIT")
	alive(t, term, "after a drag and a key move")
}

// TestWorkspaceRenumberReachesEveryClient moves by key and by drag with a
// second client attached, then detaches and attaches again: both clients, and
// the client that comes back, draw the numbering the daemon holds.
func TestWorkspaceRenumberReachesEveryClient(t *testing.T) {
	term, base := renumberSetup(t)
	second := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, second, "1·EDIT", "2·REVW", "3·DPLY")

	moveWorkspaceKey(t, term, ">")
	waitPills(t, term, "1·REVW", "2·EDIT", "3·DPLY")
	waitPills(t, second, "1·REVW", "2·EDIT", "3·DPLY")

	dragPill(t, second, "3·DPLY", "1·REVW", "1·REVW", "2·EDIT", "3·DPLY")
	waitPills(t, second, "1·DPLY", "2·REVW", "3·EDIT")
	waitPills(t, term, "1·DPLY", "2·REVW", "3·EDIT")
	wantNumbering(t, base, map[string]string{"1": "DPLY", "2": "REVW", "3": "EDIT"})
	altReaches(t, second, '1', "DPLY")
	altReaches(t, term, '3', "EDIT")

	// The first client goes away and a new one comes back to the session.
	term.Close()
	again := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, again, "1·DPLY", "2·REVW", "3·EDIT")
	wantPanesOn(t, base, map[int]string{1: "DPLY", 2: "REVW", 3: "EDIT"})
	altReaches(t, again, '2', "REVW")
	alive(t, again, "after attaching again")
	alive(t, second, "beside the client that came back")
}

// TestHerdrTabMoveKeepsTabIDs moves a tab through herdr's tab.move: the tab
// list comes back in the new order with the ids it had, the tuios numbering
// follows the move, and a tab id read before the move still names its tab.
func TestHerdrTabMoveKeepsTabIDs(t *testing.T) {
	term, base := renumberSetup(t)
	ids := map[string]string{}
	for name := range renumberPanes {
		ids[name] = herdrTabIDByLabel(t, base, name)
	}

	res := herdrCall(t, base, "tab.move", map[string]any{"tab_id": ids["EDIT"], "insert_index": 3})
	var order []string
	for _, raw := range res["tabs"].([]any) {
		tab := raw.(map[string]any)
		if want := ids[tab["label"].(string)]; tab["tab_id"] != want {
			t.Fatalf("tab %v came back as %v, want the id it had, %s", tab["label"], tab["tab_id"], want)
		}
		order = append(order, tab["label"].(string))
	}
	if strings.Join(order, " ") != "REVW DPLY EDIT" {
		t.Fatalf("tab.move left the tabs in order %v, want REVW DPLY EDIT", order)
	}
	waitPills(t, term, "1·REVW", "2·DPLY", "3·EDIT")
	wantNumbering(t, base, map[string]string{"1": "REVW", "2": "DPLY", "3": "EDIT"})
	wantPanesOn(t, base, map[int]string{1: "REVW", 2: "DPLY", 3: "EDIT"})
	for name, id := range ids {
		wantHerdrTab(t, base, id, name)
	}
	if _, err := tuiosCLI(t, base, "list-windows", "-s", renumberSession); err != nil {
		t.Fatalf("list-windows after tab.move: %v", err)
	}
	alive(t, term, "after herdr's tab.move")
}

// TestWorkspaceMoveKeyTwiceQuickly presses the move key twice in one burst.
// The second press reads the numbering from before the first move lands, and
// sent as it stands it would swap the same two workspaces back. EDIT must end
// up moved, never back on workspace 1, with the pills and the daemon agreeing.
func TestWorkspaceMoveKeyTwiceQuickly(t *testing.T) {
	term, base := renumberSetup(t)
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", ">", tuitest.Ctrl('b'), "w", ">"); err != nil {
		t.Fatalf("press the move key twice: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !screenHas(s, "1·EDIT") && strings.Contains(s.Text(), "·EDIT")
	}, uiTimeout); err != nil {
		t.Fatalf("EDIT never left workspace 1: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	info, err := daemonJSON[renumberInfo](base, "session-info", "-s", renumberSession)
	if err != nil {
		t.Fatalf("session-info: %v", err)
	}
	at := 0
	for num, name := range info.WorkspaceNames {
		if name == "EDIT" {
			fmt.Sscan(num, &at)
		}
	}
	if at != 2 && at != 3 {
		t.Fatalf("EDIT is workspace %d after two moves right, want 2 (second press dropped) or 3 (both taken): names %v", at, info.WorkspaceNames)
	}
	if !screenHas(term.Screen(), fmt.Sprintf("%d·EDIT", at)) {
		t.Fatalf("the daemon has EDIT on %d and the dock does not\n%s", at, term.Snapshot())
	}
	alive(t, term, "after two quick moves")
}

// TestWorkspaceCloseRenumbersTheRest closes a workspace in the middle, from its
// dock tab, and one at the start, through the close-workspace verb: each time
// the workspaces above it move down one, so the numbers stay 1 to the count, the
// pills and opt+N agree with the daemon, and a herdr tab id read before the
// close still names the same tab.
func TestWorkspaceCloseRenumbersTheRest(t *testing.T) {
	term, base := renumberSetup(t)
	dplyID := herdrTabIDByLabel(t, base, "DPLY")

	openTabClose(t, term, "2·REVW", "Close every pane on workspace 2 (REVW)?")
	confirmClose(t, term)
	waitPills(t, term, "1·EDIT", "2·DPLY")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "3·") && !screenHas(s, "REVW") }, uiTimeout); err != nil {
		t.Fatalf("the closed workspace or its old number is still on the dock: %v\n%s", err, term.Snapshot())
	}
	info := wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "DPLY"})
	if len(info.WorkspaceNames) != 2 {
		t.Fatalf("names after the close are %v, want EDIT and DPLY only", info.WorkspaceNames)
	}
	wantPanesOn(t, base, map[int]string{1: "EDIT", 2: "DPLY"})
	wantHerdrTab(t, base, dplyID, "DPLY")
	altReaches(t, term, '2', "DPLY")
	altReaches(t, term, '1', "EDIT")

	// The verb, on the workspace that is not in view: EDIT goes, DPLY is 1.
	altReaches(t, term, '2', "DPLY")
	if out, err := tuiosCLI(t, base, "close-workspace", "1", "-s", renumberSession); err != nil {
		t.Fatalf("close-workspace 1: %v: %s", err, out)
	}
	waitPills(t, term, "1·DPLY")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "2·") && !screenHas(s, "EDIT") }, uiTimeout); err != nil {
		t.Fatalf("the dock still shows a second workspace: %v\n%s", err, term.Snapshot())
	}
	wantNumbering(t, base, map[string]string{"1": "DPLY"})
	wantPanesOn(t, base, map[int]string{1: "DPLY"})
	wantHerdrTab(t, base, dplyID, "DPLY")
	alive(t, term, "after closing workspaces")
}

// TestWorkspaceSwitchPastTheLastOpensTheNext presses opt+5 with three
// workspaces: it opens workspace 4, a pane made there stays on 4, and an empty
// workspace left behind goes away without moving the others.
func TestWorkspaceSwitchPastTheLastOpensTheNext(t *testing.T) {
	term, base := renumberSetup(t)

	if err := term.SendKeys(tuitest.Alt('5')); err != nil {
		t.Fatalf("press alt+5: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == "4:0"
	}, uiTimeout); err != nil {
		t.Fatalf("opt+5 with three workspaces did not open workspace 4: %v\n%s", err, term.Snapshot())
	}
	newWindow(t, term)
	out, err := tuiosOut(base, "list-windows", "-s", renumberSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var payload struct {
		WorkspaceWindows []int `json:"workspace_windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload.WorkspaceWindows) < 5 {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	if got := payload.WorkspaceWindows; got[3] != 1 || got[4] != 0 {
		t.Fatalf("panes by workspace are %v, want the new pane on 4 and none on 5", got)
	}
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY"})

	// An empty workspace that is left stops existing, and nothing moves.
	if err := term.SendKeys(tuitest.Alt('7')); err != nil {
		t.Fatalf("press alt+7: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := renumberDockStatus(s)
		return m != nil && m[0] == "5:0"
	}, uiTimeout); err != nil {
		t.Fatalf("opt+7 with four workspaces did not open workspace 5: %v\n%s", err, term.Snapshot())
	}
	altReaches(t, term, '1', "EDIT")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "5·") }, uiTimeout); err != nil {
		t.Fatalf("the empty workspace 5 stayed on the dock after it was left: %v\n%s", err, term.Snapshot())
	}
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY"})
	alive(t, term, "after opening workspaces past the last")
}

// compactKeyConfig is renumberConfig with compact_workspaces bound under the
// workspace prefix. The action ships with no key.
const compactKeyConfig = renumberConfig + `compact_workspaces = ['g']
`

// gappedSetup builds the three workspaces on 1, 3 and 4 with nothing on 2, the
// shape a session saved by an older build comes back in, and names the empty
// workspace 2 too, so a name left on a gap can be seen to go. The session is
// left detached.
func gappedSetup(t *testing.T, config string) string {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, config)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", renumberSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	for _, ws := range []struct {
		num, name string
	}{{"1", "EDIT"}, {"3", "REVW"}, {"4", "DPLY"}} {
		have := 0
		if ws.num == "1" {
			have = 1
		}
		for i := have; i < renumberPanes[ws.name]; i++ {
			if out, err := tuiosCLI(t, base, "new-window", "-s", renumberSession, "--workspace", ws.num, "--no-focus"); err != nil {
				t.Fatalf("new-window on %s: %v: %s", ws.num, err, out)
			}
		}
		if out, err := tuiosCLI(t, base, "set-workspace-name", ws.num, ws.name, "--session", renumberSession); err != nil {
			t.Fatalf("name workspace %s: %v: %s", ws.num, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "set-workspace-name", "2", "beta", "--session", renumberSession); err != nil {
		t.Fatalf("name the empty workspace 2: %v: %s", err, out)
	}
	wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "beta", "3": "REVW", "4": "DPLY"})
	wantPanesOn(t, base, map[int]string{1: "EDIT", 3: "REVW", 4: "DPLY"})
	return base
}

// wantCompacted checks the daemon holds EDIT, REVW and DPLY on 1, 2 and 3, with
// their panes, and no other name: the gap's stale name is gone.
func wantCompacted(t *testing.T, base string) {
	t.Helper()
	info := wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY"})
	if len(info.WorkspaceNames) != 3 {
		t.Fatalf("names after compacting are %v, want EDIT, REVW and DPLY only", info.WorkspaceNames)
	}
	wantPanesOn(t, base, map[int]string{1: "EDIT", 2: "REVW", 3: "DPLY"})
}

// TestCompactWorkspacesClosesTheGaps runs tuios compact-workspaces on a
// detached session with workspaces 1, 3 and 4: they become 1, 2 and 3 with
// their names and panes, the empty workspace's name is dropped, a second run
// has nothing to do, and a client that attaches draws the new numbering.
func TestCompactWorkspacesClosesTheGaps(t *testing.T) {
	base := gappedSetup(t, renumberConfig)

	out, err := tuiosCLI(t, base, "compact-workspaces", "-s", renumberSession)
	if err != nil {
		t.Fatalf("compact-workspaces: %v: %s", err, out)
	}
	if want := "workspace 3 → 2\nworkspace 4 → 3\n"; !strings.Contains(out, want) || strings.Contains(out, "→ 4") {
		t.Fatalf("compact-workspaces printed %q, want %q and no other move", out, want)
	}
	wantCompacted(t, base)

	out, err = tuiosCLI(t, base, "compact-workspaces", "-s", renumberSession)
	if err != nil {
		t.Fatalf("compact-workspaces again: %v: %s", err, out)
	}
	if !strings.Contains(out, "nothing to compact") || strings.Contains(out, "→") {
		t.Fatalf("a second compact-workspaces printed %q, want nothing to compact", out)
	}
	wantCompacted(t, base)

	term := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, term, "1·EDIT", "2·REVW", "3·DPLY")
	if screenHas(term.Screen(), "4·") || screenHas(term.Screen(), "beta") {
		t.Fatalf("the dock still draws a workspace past 3 or the stale name\n%s", term.Snapshot())
	}
	altReaches(t, term, '2', "REVW")
	altReaches(t, term, '3', "DPLY")
	alive(t, term, "after compacting the workspaces")
}

// TestCompactWorkspacesKeyAndDockMenu closes the gaps from an attached client:
// first by the compact_workspaces key under the workspace prefix, then, on a new
// gap, from the "Compact workspaces" row of a dock tab's menu, which is offered
// only while there is a gap.
func TestCompactWorkspacesKeyAndDockMenu(t *testing.T) {
	base := gappedSetup(t, compactKeyConfig)
	term := attachIn(t, base, renumberSession, startOpts{})
	waitPills(t, term, "1·EDIT", "3·REVW", "4·DPLY")

	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "g"); err != nil {
		t.Fatalf("press the compact key: %v", err)
	}
	waitPills(t, term, "1·EDIT", "2·REVW", "3·DPLY")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "4·") }, uiTimeout); err != nil {
		t.Fatalf("the dock still draws workspace 4 after the compact key: %v\n%s", err, term.Snapshot())
	}
	wantCompacted(t, base)
	altReaches(t, term, '3', "DPLY")
	altReaches(t, term, '1', "EDIT")

	// With no gap the tab's menu does not offer the row.
	col, row, ok := dockCell(term.Screen(), "1·EDIT")
	if !ok {
		t.Fatalf("no dock tab 1·EDIT\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseRight, 0)
	waitMenu(t, term, "the 1·EDIT tab", "Switch to", "Rename", "Close")
	if screenHas(term.Screen(), "Compact workspaces") {
		t.Fatalf("the tab menu offers Compact workspaces with no gap\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press escape: %v", err)
	}
	waitMenuGone(t, term, "Switch to", "after esc on the 1·EDIT tab menu")

	// A pane put on 5 leaves 4 empty, and the menu row closes it up.
	if out, err := tuiosCLI(t, base, "new-window", "-s", renumberSession, "--workspace", "5", "--no-focus"); err != nil {
		t.Fatalf("new-window on 5: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-workspace-name", "5", "SHIP", "--session", renumberSession); err != nil {
		t.Fatalf("name workspace 5: %v: %s", err, out)
	}
	waitPills(t, term, "1·EDIT", "2·REVW", "3·DPLY", "5·SHIP")
	col, row, ok = dockCell(term.Screen(), "5·SHIP")
	if !ok {
		t.Fatalf("no dock tab 5·SHIP\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseRight, 0)
	waitMenu(t, term, "the 5·SHIP tab", "Switch to", "Compact workspaces", "Close")
	col, row = screenCell(t, term, "Compact workspaces")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitPills(t, term, "1·EDIT", "2·REVW", "3·DPLY", "4·SHIP")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "5·") }, uiTimeout); err != nil {
		t.Fatalf("the dock still draws workspace 5 after the menu row: %v\n%s", err, term.Snapshot())
	}
	info := wantNumbering(t, base, map[string]string{"1": "EDIT", "2": "REVW", "3": "DPLY", "4": "SHIP"})
	if len(info.WorkspaceNames) != 4 {
		t.Fatalf("names after the menu row are %v, want EDIT, REVW, DPLY and SHIP", info.WorkspaceNames)
	}
	altReaches(t, term, '2', "REVW")
	alive(t, term, "after compacting from the key and the dock menu")
}
