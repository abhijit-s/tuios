package app

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// RenameKind names what an in-progress rename points at. There is one rename
// editor and one dialog for all three, so a user who has renamed a pane already
// knows how to rename a session.
type RenameKind int

const (
	// RenameNone means no rename is running.
	RenameNone RenameKind = iota
	// RenameWindow targets a window's custom name.
	RenameWindow
	// RenameSession targets a session's name, which the daemon owns. The
	// rename goes to the daemon, and the new name comes back on a state push.
	RenameSession
	// RenameWorkspace targets a workspace's name. The number stays its identity.
	RenameWorkspace
	// RenameSpace targets a space's name, which every member session carries.
	// The daemon's rename-space moves the members, so the dialog renames a
	// space exactly as tuios rename-space does, merging into a space that
	// already has the name.
	RenameSpace
	// RenameSessionSpace targets the space a session is filed under. The
	// buffer is the space, not a name for the session: typing a space that
	// exists joins it, a new name makes that space, and an empty field takes
	// the session out of every space. The daemon's set-session-space applies
	// it, so the dialog refuses what tuios set-session-space refuses.
	RenameSessionSpace
	// RenameNewSpace names a space that a new session is made in. It targets
	// nothing that exists yet: the buffer starts empty, and committing it makes
	// a session in the typed space and switches to it, as new_session_in_space
	// does for a space that exists.
	RenameNewSpace
)

// Renaming reports whether a rename editor is open, whatever it targets.
func (m *OS) Renaming() bool { return m.RenameKind != RenameNone }

// BeginRenameWindow starts an inline rename of a window, seeded with the name it
// already carries. There is one rename in flight at a time and it names the
// window it targets, so the editor can be drawn wherever that window shows: the
// pane's title bar, its sidebar row, or both at once.
func (m *OS) BeginRenameWindow(w *terminal.Window) {
	if w == nil {
		return
	}
	m.RenameKind = RenameWindow
	m.RenameTargetID = w.ID
	m.RenameBuffer = w.CustomName
	w.InvalidateCache()
}

// BeginRenameSession starts a rename of a session, seeded with what the
// session shows now: its name, or its display label when a script set one.
func (m *OS) BeginRenameSession(name string) {
	if name == "" {
		return
	}
	m.RenameKind = RenameSession
	m.RenameTargetID = name
	m.RenameBuffer = m.SessionLabel(name)
}

// BeginRenameWorkspace starts a rename of a workspace, seeded with its current
// name. An unnamed workspace opens empty: its number is not a name it was given.
func (m *OS) BeginRenameWorkspace(ws int) {
	// A scratch group's workspace has no name to give: its box shows the
	// group's name.
	if ws <= 0 || session.IsScratchWorkspace(ws) {
		return
	}
	m.RenameKind = RenameWorkspace
	m.RenameTargetID = strconv.Itoa(ws)
	m.RenameBuffer = m.WorkspaceNames[ws]
}

// BeginRenameSpace starts a rename of a space, seeded with its name. A space is
// only ever named, so there is nothing to open on for "".
func (m *OS) BeginRenameSpace(space string) {
	if space == "" {
		return
	}
	m.RenameKind = RenameSpace
	m.RenameTargetID = space
	m.RenameBuffer = space
}

// BeginSetSessionSpace opens the dialog that files a session under a space,
// seeded with the space it is in now. A global session cannot be in a space,
// so it gets the daemon's refusal as a notice instead of a dialog that could
// only fail.
func (m *OS) BeginSetSessionSpace(name string) {
	if name == "" {
		return
	}
	if m.sessionIsGlobal(name) {
		m.ShowNotification("Not moved: "+session.GlobalSpaceRefusal, "info", m.Settings.NotificationDuration)
		return
	}
	m.RenameKind = RenameSessionSpace
	m.RenameTargetID = name
	m.RenameBuffer = m.sessionSpaceOf(name)
}

// BeginNewSpace opens the dialog that names a new space, which committing makes
// by making a session in it. A space exists only as a label on sessions, so a
// session is what the new space is made of. Without the daemon no session can
// be made, so it says so instead of opening a dialog that could only fail.
func (m *OS) BeginNewSpace() {
	if m.learnOff(learnNoteSessions) {
		return
	}
	if !m.CanCreateSession() {
		m.ShowNotification("Sessions need the daemon", "info", m.Settings.NotificationDuration)
		return
	}
	m.RenameKind = RenameNewSpace
	m.RenameTargetID = ""
	m.RenameBuffer = ""
}

// sessionSpaceOf is the space a session is filed under. The attached session's
// comes from its own state, which a push keeps current; any other comes from
// the cached listing.
func (m *OS) sessionSpaceOf(name string) string {
	if name == m.SessionName {
		return m.SessionSpace
	}
	if m.DaemonClient == nil {
		return ""
	}
	return m.DaemonClient.SessionSpace(name)
}

// sessionIsGlobal reports whether a session is a global one, which no space
// can hold.
func (m *OS) sessionIsGlobal(name string) bool {
	if name == m.SessionName && m.SessionGlobal {
		return true
	}
	return m.DaemonClient != nil && m.DaemonClient.SessionGlobal(name)
}

// RenameAppend adds typed text to the rename buffer, keeping only what the
// chrome will actually draw. The gate is printableRune, the same rule the rail,
// title bar, palette and dock launder names through, so the editor can never
// take a codepoint that would vanish or tofu the moment the name was shown.
func (m *OS) RenameAppend(text string) {
	if !m.Renaming() || text == "" {
		return
	}
	add := printableRunes(text)
	// A keypress that is nothing but combining marks has no base to sit on, so
	// it would stack an accent on whatever the field already ends with, or on
	// the cursor. Terminals send composed text as one precomposed rune.
	if add == "" || combiningOnly(add) {
		return
	}
	m.RenameBuffer += add
	if t := m.RenameTarget(); t != nil {
		t.InvalidateCache()
	}
}

// RenameBackspace drops the last rune of the buffer.
func (m *OS) RenameBackspace() {
	if !m.Renaming() || m.RenameBuffer == "" {
		return
	}
	m.RenameBuffer = dropLastRune(m.RenameBuffer)
	if t := m.RenameTarget(); t != nil {
		t.InvalidateCache()
	}
}

// dropLastRune removes the last rune of s. It counts in runes because a name
// may hold multi-byte text, and cutting one byte off é leaves the string
// holding a broken sequence that renders as a replacement glyph.
func dropLastRune(s string) string {
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

// combiningOnly reports whether s is nothing but marks that attach to a
// preceding character.
func combiningOnly(s string) bool {
	for _, r := range s {
		if !unicode.In(r, unicode.Mn, unicode.Me) {
			return false
		}
	}
	return true
}

// BeginRenameCurrentWorkspace starts a rename of the workspace the user meant:
// the one whose pill menu the action came from, or the one they are on when it
// came from a key. Both entry points land here so the dialog is the same either
// way.
func (m *OS) BeginRenameCurrentWorkspace() {
	ws := m.TakeMenuWorkspace()
	if ws <= 0 {
		ws = m.CurrentWorkspace
	}
	m.BeginRenameWorkspace(ws)
}

// RenameTarget is the window an in-progress rename applies to, or nil when no
// window rename is running or the window went away under it.
func (m *OS) RenameTarget() *terminal.Window {
	if m.RenameKind != RenameWindow {
		return nil
	}
	if i := m.windowIndexByID(m.RenameTargetID); i >= 0 {
		return m.Windows[i]
	}
	return nil
}

// RenameDialogTitle names what the open editor is renaming, so the dialog says
// which of the three it is about to change.
//
// A session is named as well as typed, the way a workspace already was. The
// editor is reachable from any session's rail row, so a title that said only
// "rename session" left the user to work out whether it had landed on the row
// they pointed at or on the session they happen to be attached to.
func (m *OS) RenameDialogTitle() string {
	switch m.RenameKind {
	case RenameSession:
		if label := m.SessionLabel(m.RenameTargetID); label != "" {
			return "rename session " + label
		}
		return "rename session"
	case RenameWorkspace:
		return "rename workspace " + m.RenameTargetID
	case RenameSpace:
		return "rename space " + printableTitle(m.RenameTargetID)
	case RenameSessionSpace:
		return "move " + printableTitle(m.SessionLabel(m.RenameTargetID)) + " to space"
	case RenameNewSpace:
		return "new space"
	default:
		return "rename"
	}
}

// CommitRename applies the rename in progress and clears the editor. A window
// name is client-owned and applied here; a session or workspace name is
// daemon-owned and returned as a command, because reaching the daemon is a
// blocking round trip that must not run on the Update goroutine.
func (m *OS) CommitRename() tea.Cmd {
	kind, target := m.RenameKind, m.RenameTargetID
	label := strings.TrimSpace(m.RenameBuffer)
	defer m.EndRename()

	if kind == RenameWindow {
		if w := m.RenameTarget(); w != nil {
			_ = m.RenameWindowByID(w.ID, m.RenameBuffer)
			w.InvalidateCache()
		}
		return nil
	}
	if kind == RenameSession && label == m.SessionLabel(target) {
		// Nothing changed, so there is nothing to send. A session that
		// shows a display label keeps it.
		return nil
	}
	if kind == RenameSpace && label == target {
		return nil
	}
	if kind == RenameNewSpace {
		// An empty name backs out, as it does for a space rename. A name that
		// is a space already gets a session in that space, as tuios new
		// --space does: the person asked for a session in a space of that
		// name, and one now exists. Every other name is checked by
		// new-session, so it is refused in that verb's words.
		if label != "" {
			m.NewSessionInSpace(label)
		}
		return nil
	}
	what := "Rename"
	if kind == RenameSessionSpace {
		if label == m.sessionSpaceOf(target) {
			return nil
		}
		what = "Move"
	}
	verb, params, ok := renameVerb(kind, target, m.SessionName, label)
	if !ok {
		return nil
	}
	return labelVerbCmd(m.verbDialer(), what, verb, params)
}

// renameVerb picks the daemon verb a rename goes through and builds its params.
// A session rename changes the session's name with rename-session, so the name
// the UI shows is the name ls lists and attach takes (issue #266). It used to
// send set-session-name, which only set a display label.
// An empty name is not sent: a session cannot be without a name.
func renameVerb(kind RenameKind, target, sessionName, label string) (string, map[string]any, bool) {
	switch kind {
	case RenameSession:
		if target == "" || label == "" {
			return "", nil, false
		}
		return "rename-session", map[string]any{"session": target, "name": label}, true
	case RenameWorkspace:
		ws, err := strconv.Atoi(target)
		if err != nil || ws <= 0 {
			return "", nil, false
		}
		return "set-workspace-name", map[string]any{
			"session": sessionName, "workspace": ws, "name": label,
		}, true
	case RenameSpace:
		// An empty name cancels rather than reaching the verb, which would
		// refuse it: emptying the field is how a person backs out. Every
		// other name goes to the verb as typed and is checked there, so the
		// dialog refuses exactly what tuios rename-space refuses, in its words.
		if target == "" || label == "" {
			return "", nil, false
		}
		return "rename-space", map[string]any{"space": target, "name": label}, true
	case RenameSessionSpace:
		// An empty name is sent, because it is how the verb takes a session
		// out of every space. Every other name is checked by the verb alone.
		if target == "" {
			return "", nil, false
		}
		return "set-session-space", map[string]any{"session": target, "space": label}, true
	default:
		return "", nil, false
	}
}

// EndRename clears the rename state, committed or cancelled.
func (m *OS) EndRename() {
	m.RenameKind = RenameNone
	m.RenameTargetID = ""
	m.RenameBuffer = ""
	m.renameHit = overlay.Rect{}
}

// RenameMouseClick routes a click while the rename dialog is up: inside is a
// no-op (the field has no clickable parts), outside cancels. The dialog is
// modal to the mouse either way, so a stray click can never leave an editor
// open over a pane the user has moved on to. The mouse is never required: the
// same keys that opened it commit or cancel it.
func (m *OS) RenameMouseClick(x, y int) bool {
	if !m.Renaming() {
		return false
	}
	if !m.renameHit.Contains(x, y) {
		m.EndRename()
	}
	return true
}
