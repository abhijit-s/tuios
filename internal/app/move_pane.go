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

// Moving a pane to another session from the UI.
//
// The move itself is the daemon's move-window with to_session, the same call
// the CLI makes, so the pane keeps its process and scrollback and the daemon
// stays the one place that decides what can move. The UI only picks the two
// things the call needs, with the two pickers that already exist: the session
// switcher, in a mode that lists the other sessions, and then the workspace
// switcher, fed the workspaces of the session that was picked.
//
// The user stays where they are. Following the pane is one keypress away (the
// rail, the switcher), and a flow that moved the user too would make "put this
// out of my way" impossible to ask for.

// paneMove is a move to another session in progress: the pane it was opened
// on and, once the first step has an answer, the session picked.
type paneMove struct {
	windowID string
	label    string
	// session is the picked session's name, empty during the first step.
	session string
	// sessionLabel is what the user calls it, for the title and the notice.
	sessionLabel string
}

// PaneMovedMsg is move-window's answer for a move made from the UI.
type PaneMovedMsg struct {
	Pane      string
	Session   string
	Workspace int
	Err       error
}

// paneMoveTimeout bounds the call. The move is a handover inside one daemon,
// so a call that takes longer than this is not going to land.
const paneMoveTimeout = 10 * time.Second

// MoveToSessionRefusal is why the pane at windowIndex cannot be moved to
// another session from here, or "" when it can. The menu dims its row on it,
// and the action says it, so neither offers a move the daemon would refuse.
func (m *OS) MoveToSessionRefusal(windowIndex int) string {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return "Moving a pane to another session needs the daemon"
	}
	if windowIndex < 0 || windowIndex >= len(m.Windows) || m.Windows[windowIndex] == nil {
		return "No pane to move"
	}
	win := m.Windows[windowIndex]
	switch {
	case isScratch(win):
		return "A scratch pane stays in its session"
	case win.IsPopup:
		return "A popup stays in its session"
	case win.Host != "":
		return "A pane on " + win.Host + " cannot move to another session"
	}
	if len(m.moveTargetSessions()) == 0 {
		return "There is no other session to move the pane to"
	}
	return ""
}

// moveTargetSessions are the sessions a pane can move to: every session of
// this daemon but the one it is in. A session on another machine is not one,
// because the daemon moves a pane between its own sessions only.
func (m *OS) moveTargetSessions() []sessiontree.Node {
	var out []sessiontree.Node
	for _, n := range m.BuildSessionTree().Sessions {
		if n.Kind == sessiontree.KindSession && n.Host == "" && !n.IsCurrent {
			out = append(out, n)
		}
	}
	return out
}

// OpenMoveToSession starts the flow for the focused pane: the menu row focuses
// the pane it was opened on before it runs, so this is that pane too.
func (m *OS) OpenMoveToSession() {
	if why := m.MoveToSessionRefusal(m.FocusedWindow); why != "" {
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
	m.OpenWorkspaceSwitcher()
	m.paneMove = mv
	m.WorkspaceSwitcherItems = m.moveTargetWorkspaces(target.ID)
	m.WorkspaceSwitcherSelected = 0
}

// moveTargetWorkspaces lists the workspaces of session name worth moving to:
// first the one it shows, which is where the daemon puts the pane when the
// call names none, then every other one holding a pane. Any other workspace
// is a digit away.
func (m *OS) moveTargetWorkspaces(name string) []WorkspaceItem {
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

// MoveToWorkspace ends the flow by moving the pane to workspace ws of the
// picked session. ws 0 leaves the workspace to the daemon: the one the
// session shows when the call lands, which the first row stands for.
func (m *OS) MoveToWorkspace(ws int) tea.Cmd {
	mv := m.paneMove
	m.CloseWorkspaceSwitcher()
	if mv == nil || mv.session == "" || m.DaemonClient == nil {
		return nil
	}
	params := map[string]any{
		"session":    m.SessionName,
		"window":     mv.windowID,
		"to_session": mv.session,
	}
	if ws > 0 {
		params["workspace"] = ws
	}
	build := m.DaemonClient.ClientVersion()
	return func() tea.Msg {
		msg := PaneMovedMsg{Pane: mv.label, Session: mv.sessionLabel}
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
	m.ShowNotification("Moved "+printableTitle(msg.Pane)+" to "+printableTitle(msg.Session)+" · workspace "+strconv.Itoa(msg.Workspace), "success", m.Settings.NotificationDuration)
}
