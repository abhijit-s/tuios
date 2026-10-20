package app

import (
	"image/color"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	workspaceSwitcherWidth = 44
	// workspaceSwitcherRows is the requested row count, shared with the wheel so
	// it scrolls by the same page the renderer draws.
	workspaceSwitcherRows = 9
)

// renderWorkspaceSwitcher renders the workspace switcher on the shared overlay
// grammar, returning the panel, geometry and hit rows.
func (m *OS) renderWorkspaceSwitcher() (string, overlay.Geometry, []overlayRowHit) {
	filtered := FilterWorkspaceItems(m.WorkspaceSwitcherItems, m.WorkspaceSwitcherQuery)
	if len(filtered) > 0 {
		m.WorkspaceSwitcherSelected = clampInt(m.WorkspaceSwitcherSelected, 0, len(filtered)-1)
	}

	title, tag, empty := "Workspaces", "current", "No workspace matches"
	hints := []overlay.Hint{
		{Key: overlay.EnterGlyph, Label: "go"},
		{Key: "ctrl+r", Label: "rename"},
		{Key: "esc", Label: "close"},
	}
	if mv := m.paneMove; mv != nil {
		// The rows are the picked session's. Another session's first row is
		// the workspace it shows, which is where the pane lands when no
		// number is sent. This session's rows leave out the pane's own
		// workspace, so with no other workspace holding a pane a digit is
		// the way to name one.
		title, tag = "Move "+printableTitle(mv.label)+" to "+printableTitle(mv.sessionLabel), "shown"
		if mv.here && m.WorkspaceSwitcherQuery == "" {
			empty = "No other workspace has a pane"
		}
		hints = []overlay.Hint{
			{Key: overlay.EnterGlyph, Label: "move"},
			{Key: "1-9", Label: "workspace"},
			{Key: "esc", Label: "cancel"},
		}
	}

	return m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      workspaceSwitcherWidth,
		MaxVisible: workspaceSwitcherRows,
		Search:     true,
		Query:      m.WorkspaceSwitcherQuery,
		Count:      len(filtered),
		Selected:   m.WorkspaceSwitcherSelected,
		Scroll:     &m.WorkspaceSwitcherScroll,
		EmptyMsg:   empty,
		Hints:      hints,
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return workspaceSwitcherRow(filtered[i], tag, selected, rowBg, pal, width)
		},
	})
}

// workspaceSwitcherRow draws one workspace: its number, the name when it has
// one, and its pane count. The number leads every row because it is the
// workspace's identity and the key its jump bindings use, so it has to be
// readable whether or not the workspace has been named. tag is the word the
// current row wears.
func workspaceSwitcherRow(w WorkspaceItem, tag string, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	right := overlay.Style(rowBg).Foreground(pal.FgMute).Render(panePlural(w.Panes))
	if w.IsCurrent {
		right = overlay.Style(rowBg).Foreground(pal.Success).Render(tag+"  ") + right
	}
	// Another session's shown workspace is 0 until its listing says, and the
	// row still stands for wherever that session is.
	number := strconv.Itoa(w.Number)
	if w.Number == 0 {
		number = "-"
	}

	numColor := pal.FgMute
	if selected {
		numColor = pal.Accent
	}
	left := overlay.Style(rowBg).Foreground(numColor).Bold(true).Render(number)
	if w.Name != "" {
		labelColor := pal.FgDim
		if selected {
			labelColor = pal.Fg
		}
		left += overlay.Style(rowBg).Foreground(labelColor).Bold(selected).
			Render("  " + overlay.Truncate(printableTitle(w.Name), max(width-12, 1)))
	}
	return listRowSpans(width, listRowMarker(selected), left, right, rowBg, pal)
}
