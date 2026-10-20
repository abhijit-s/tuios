package app

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Moving a pane from the UI, to another workspace of this session or to
// another session.
//
// The move itself is the daemon's move-window, the same call the CLI makes:
// without to_session for this session, with it for another one. The pane
// keeps its process and scrollback, and the daemon stays the one place that
// decides what can move. The UI only picks the two things the call needs, with
// the two pickers that already exist: the session switcher, in a mode that
// lists this session and then the others, and then the workspace switcher,
// fed the workspaces of the session that was picked.
//
// The user stays where they are. Following the pane is one keypress away (the
// rail, the switcher), and a flow that moved the user too would make "put this
// out of my way" impossible to ask for.

// paneMove is a move in progress: the pane it was opened on and, once the
// first step has an answer, the session picked.
type paneMove struct {
	windowID string
	label    string
	// session is the picked session's name, empty during the first step.
	session string
	// sessionLabel is what the user calls it, for the title and the notice.
	sessionLabel string
	// here is set when the picked session is this one: the move is to
	// another workspace, and the call names no session to move to.
	here bool
}

// PaneMovedMsg is move-window's answer for a move made from the UI. Session
// is empty for a move to another workspace of this session.
type PaneMovedMsg struct {
	Pane      string
	Session   string
	Workspace int
	Err       error
}

// paneMoveTimeout bounds the call. The move is a handover inside one daemon,
// so a call that takes longer than this is not going to land.
const paneMoveTimeout = 10 * time.Second

// MovePaneRefusal is why the pane at windowIndex cannot be moved from here,
// or "" when it can. The menu dims its row on it, and the action says it, so
// neither offers a move the daemon would refuse.
func (m *OS) MovePaneRefusal(windowIndex int) string {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return "Moving a pane needs the daemon"
	}
	if windowIndex < 0 || windowIndex >= len(m.Windows) || m.Windows[windowIndex] == nil {
		return "No pane to move"
	}
	win := m.Windows[windowIndex]
	switch {
	case isScratch(win):
		return "A scratch pane stays in its scratch group"
	case win.IsPopup:
		return "A popup cannot move"
	case win.Host != "":
		return "A pane on " + win.Host + " cannot move from here"
	}
	return ""
}

// moveTargetSessions are the sessions a pane can move to: this one first, for
// a move to another workspace, then every other session of this daemon. A
// session on another machine is not one, because the daemon moves a pane
// between its own sessions only.
func (m *OS) moveTargetSessions() []sessiontree.Node {
	var here, out []sessiontree.Node
	for _, n := range m.BuildSessionTree().Sessions {
		if n.Kind != sessiontree.KindSession || n.Host != "" {
			continue
		}
		if n.IsCurrent {
			here = append(here, n)
		} else {
			out = append(out, n)
		}
	}
	return append(here, out...)
}

// OpenMovePane starts the flow for the focused pane: the menu row focuses
// the pane it was opened on before it runs, so this is that pane too.
func (m *OS) OpenMovePane() {
	if why := m.MovePaneRefusal(m.FocusedWindow); why != "" {
		m.ShowNotification(why, "warning", m.Settings.NotificationDuration)
		return
	}
	win := m.Windows[m.FocusedWindow]
	m.OpenSessionSwitcher()
	if !m.ShowSessionSwitcher {
		return
	}
	m.paneMove = &paneMove{windowID: win.ID, label: paneMoveLabel(win)}
	m.SessionSwitcherItems = m.sessionSwitcherItems()
}

// paneMoveLabel is the pane's name as the titles and the notice give it.
func paneMoveLabel(win *terminal.Window) string {
	if win.CustomName != "" {
		return win.CustomName
	}
	return windowRowTitle(win)
}

// MovingPane reports whether the session switcher is picking where to move a
// pane rather than where to go.
func (m *OS) MovingPane() bool {
	return m.paneMove != nil
}

// MoveToSessionChosen takes the first step's answer and opens the second:
// the workspace in that session.
func (m *OS) MoveToSessionChosen(target sessiontree.Node) {
	mv := m.paneMove
	m.CloseSessionSwitcher()
	if mv == nil {
		return
	}
	mv.session = target.ID
	mv.sessionLabel = sessionSwitcherLabel(target)
	mv.here = target.IsCurrent
	m.OpenWorkspaceSwitcher()
	m.paneMove = mv
	m.WorkspaceSwitcherItems = m.moveTargetWorkspaces()
	m.WorkspaceSwitcherSelected = 0
}

// moveTargetWorkspaces lists the workspaces of the picked session worth moving
// to. Any other workspace is a digit away.
func (m *OS) moveTargetWorkspaces() []WorkspaceItem {
	if m.paneMove.here {
		return m.moveTargetWorkspacesHere()
	}
	return m.moveTargetWorkspacesIn(m.paneMove.session)
}

// moveTargetWorkspacesHere lists this session's workspaces that hold a pane,
// but the one the pane is on: moving there would do nothing.
func (m *OS) moveTargetWorkspacesHere() []WorkspaceItem {
	from := m.paneMoveFrom()
	panes := map[int]int{}
	for _, w := range m.Windows {
		if w != nil && !isScratch(w) && w.Workspace > 0 && w.Workspace != from {
			panes[w.Workspace]++
		}
	}
	nums := make([]int, 0, len(panes))
	for ws := range panes {
		nums = append(nums, ws)
	}
	slices.Sort(nums)
	items := make([]WorkspaceItem, 0, len(nums))
	for _, ws := range nums {
		items = append(items, WorkspaceItem{Number: ws, Name: m.WorkspaceNames[ws], Panes: panes[ws]})
	}
	return items
}

// moveTargetWorkspacesIn lists the workspaces of session name: first the one
// it shows, which is where the daemon puts the pane when the call names none,
// then every other one holding a pane.
func (m *OS) moveTargetWorkspacesIn(name string) []WorkspaceItem {
	shown := m.DaemonClient.SessionCurrentWorkspace(name)
	panes := map[int]int{}
	for _, w := range m.DaemonClient.SessionWindows(name) {
		if !w.Scratch && w.Workspace > 0 {
			panes[w.Workspace]++
		}
	}
	items := []WorkspaceItem{{Number: shown, Panes: panes[shown], IsCurrent: true}}
	others := make([]int, 0, len(panes))
	for ws := range panes {
		if ws != shown {
			others = append(others, ws)
		}
	}
	slices.Sort(others)
	for _, ws := range others {
		items = append(items, WorkspaceItem{Number: ws, Panes: panes[ws]})
	}
	return items
}

// paneMoveFrom is the workspace the pane being moved is on, or 0 when it has
// gone.
func (m *OS) paneMoveFrom() int {
	if i := m.windowIndexByID(m.paneMove.windowID); i >= 0 {
		return m.Windows[i].Workspace
	}
	return 0
}

// MoveToWorkspace ends the flow by moving the pane to workspace ws of the
// picked session. ws 0 leaves the workspace to the daemon: the one the other
// session shows when the call lands, which its first row stands for.
func (m *OS) MoveToWorkspace(ws int) tea.Cmd {
	mv := m.paneMove
	if mv == nil || mv.session == "" || m.DaemonClient == nil {
		m.CloseWorkspaceSwitcher()
		return nil
	}
	from := m.paneMoveFrom()
	m.CloseWorkspaceSwitcher()
	params := map[string]any{
		"session": m.SessionName,
		"window":  mv.windowID,
	}
	sessionLabel := mv.sessionLabel
	if mv.here {
		if ws == from {
			m.ShowNotification(printableTitle(mv.label)+" is already on workspace "+strconv.Itoa(ws), "info", m.Settings.NotificationDuration)
			return nil
		}
		sessionLabel = ""
		params["follow"] = false
	} else {
		params["to_session"] = mv.session
	}
	if ws > 0 {
		params["workspace"] = ws
	}
	build := m.DaemonClient.ClientVersion()
	return func() tea.Msg {
		msg := PaneMovedMsg{Pane: mv.label, Session: sessionLabel}
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			msg.Err = err
			return msg
		}
		defer func() { _ = client.Close() }()
		raw, err := client.CallWithTimeout("move-window", params, paneMoveTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res struct {
			Workspace int `json:"workspace"`
		}
		msg.Err = json.Unmarshal(raw, &res)
		msg.Workspace = res.Workspace
		return msg
	}
}

// MoveToWorkspaceRow ends the flow with the workspace step's row idx.
func (m *OS) MoveToWorkspaceRow(idx int) tea.Cmd {
	target, ok := m.WorkspaceSwitcherTarget(idx)
	if !ok {
		return nil
	}
	if target.IsCurrent {
		return m.MoveToWorkspace(0)
	}
	return m.MoveToWorkspace(target.Number)
}

// handlePaneMoved reports the move. A refusal is shown as the daemon worded
// it, as an error, because the pane did not move.
func (m *OS) handlePaneMoved(msg PaneMovedMsg) {
	if msg.Err != nil {
		m.ShowNotification("Move failed: "+plainCause(msg.Err), "error", m.Settings.NotificationDuration*2)
		return
	}
	where := "workspace " + strconv.Itoa(msg.Workspace)
	if msg.Session != "" {
		where = printableTitle(msg.Session) + " · " + where
	}
	m.ShowNotification("Moved "+printableTitle(msg.Pane)+" to "+where, "success", m.Settings.NotificationDuration)
}
