package app

import (
	"image/color"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// sessionCloseInnerWidth is the dialog's preferred inner width, sized so the
// toll line still fits with both agent clauses on it. The blocked call-out is
// last and would be the part cut, and it is the one the dialog exists to show.
const sessionCloseInnerWidth = 54

// renderSessionClose draws the close confirmation: the question, what closing
// would take down counted as the frame is built, and the two rows.
//
// A dialog rather than a panel because it is one focused question with two
// answers, which is exactly what the micro-dialog is for.
func (m *OS) renderSessionClose() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(sessionCloseInnerWidth, m.GetRenderWidth())
	m.SessionCloseSelected = clampInt(m.SessionCloseSelected, 0, sessionCloseRowCount-1)

	toll := m.SessionTollFor(m.SessionCloseTarget)
	var members []string
	switch {
	case m.SessionCloseWindow != "":
		toll = m.windowToll(m.windowIndexByID(m.SessionCloseWindow))
	case m.SessionCloseWorkspace != 0:
		toll = m.workspaceToll(m.SessionCloseWorkspace)
	case m.SessionCloseSpace != "":
		members = m.spaceMembers(m.SessionCloseSpace)
		toll = m.spaceToll(members)
	}
	// The toll is warn-coloured only when an agent is in it. Colouring it always
	// would spend the one loud thing in the dialog on "three shells are open",
	// and then have nothing left for the case that matters.
	tollColor := pal.FgDim
	if toll.Working > 0 || toll.Blocked > 0 {
		tollColor = pal.Warn
	}

	line := func(text string, fg color.Color, bold bool) string {
		return overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(fg).Bold(bold).Render(overlay.Truncate(text, max(width-1, 1))), width, bg)
	}
	body := []string{line(m.sessionCloseQuestion(), pal.Fg, true)}
	if m.SessionCloseSpace != "" {
		// A space is closed whole, so the dialog names what is in it: the
		// sessions as the rail labels them, then their panes and agents.
		labels := make([]string, len(members))
		for i, name := range members {
			labels[i] = printableTitle(m.sessionTitle(name))
		}
		body = append(body, line(countOf(len(members), "session")+": "+strings.Join(labels, ", "), pal.FgDim, false))
	}
	body = append(body, line(toll.Line(), tollColor, false))
	if m.SessionCloseSpace != "" && slices.Contains(members, m.SessionName) {
		body = append(body, line("This client quits: its session is in the space.", pal.Warn, false))
	}
	body = append(body,
		overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg),
		m.sessionCloseRow(SessionCloseRowCancel, width, pal),
		m.sessionCloseRow(SessionCloseRowClose, width, pal),
	)
	firstRow := len(body) - sessionCloseRowCount

	content, geo := overlay.Dialog{
		Title: m.sessionCloseTitle(),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "run"},
			{Key: "esc", Label: "cancel"},
		},
	}.Render(pal)

	// One rect per drawn row, in drawn order, so a click answers the dialog the
	// user is looking at.
	hits := make([]overlayRowHit, 0, sessionCloseRowCount)
	for i := range sessionCloseRowCount {
		y := geo.BodyY + firstRow + i
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1},
			Idx:  i,
		})
	}
	return content, geo, hits
}

// sessionCloseRow draws one answer. The destructive row takes the warn colour
// on the cursor and stays muted off it, which is the same weight split the dock
// controls use, so the button and the row that confirms it read as one thing.
func (m *OS) sessionCloseRow(idx, width int, pal overlay.Palette) string {
	bg := pal.Canvas
	cursor := m.SessionCloseSelected == idx
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}

	label, labelColor := "Cancel", pal.FgDim
	if idx == SessionCloseRowClose {
		label, labelColor = m.sessionCloseLabel(), pal.FgMute
		if cursor {
			labelColor = pal.Warn
		}
	} else if cursor {
		labelColor = pal.Fg
	}

	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(labelColor).Bold(cursor).Render(label)
	return overlay.Fill(row, width, bg)
}
