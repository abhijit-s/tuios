package input

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleWorkspaceSwitcherInput handles keyboard input while the workspace
// switcher is open. It mirrors the session switcher's bindings, so the two
// overlays are driven the same way.
func handleWorkspaceSwitcherInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()
	filtered := app.FilterWorkspaceItems(o.WorkspaceSwitcherItems, o.WorkspaceSwitcherQuery)

	if listKey(o, keyStr, false, listPage, func(d int) { o.WorkspaceSwitcherMove(d, len(filtered)) }) {
		return o, nil
	}

	// Moving a pane, a digit is the workspace itself, listed or not: the list
	// shows only the workspaces with panes, and an empty one is as good a place
	// to put a pane. Otherwise a digit is a search, like any rune.
	if o.MovingPane() && len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
		return o, o.MoveToWorkspace(int(keyStr[0] - '0'))
	}

	switch keyStr {
	case "esc":
		o.CloseWorkspaceSwitcher()
		return o, nil

	case "enter":
		// Same entry point as the mouse click, so the two cannot drift.
		if o.MovingPane() {
			return o, o.MoveToWorkspaceRow(o.WorkspaceSwitcherSelected)
		}
		o.WorkspaceSwitcherActivate(o.WorkspaceSwitcherSelected)
		return o, nil

	case "ctrl+r":
		if o.MovingPane() {
			// The rows are where the pane goes, not workspaces to rename.
			return o, nil
		}
		if target, ok := o.WorkspaceSwitcherTarget(o.WorkspaceSwitcherSelected); ok {
			o.BeginRenameWorkspace(target.Number)
		}
		return o, nil

	case "backspace":
		if len(o.WorkspaceSwitcherQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(o.WorkspaceSwitcherQuery)
			o.WorkspaceSwitcherQuery = o.WorkspaceSwitcherQuery[:len(o.WorkspaceSwitcherQuery)-size]
			o.WorkspaceSwitcherSelected = 0
			o.WorkspaceSwitcherScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.WorkspaceSwitcherQuery = ""
		o.WorkspaceSwitcherSelected = 0
		o.WorkspaceSwitcherScroll = 0
		return o, nil

	default:
		// A space and any typed rune, the way the session switcher takes
		// them. The gate here was printable ASCII only, so a name with a
		// space in it could not be searched for past its first word.
		text := msg.Text
		if keyStr == "space" {
			text = " "
		}
		if text != "" {
			o.WorkspaceSwitcherQuery += text
			o.WorkspaceSwitcherSelected = 0
			o.WorkspaceSwitcherScroll = 0
		}
		return o, nil
	}
}
