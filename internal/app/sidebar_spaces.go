package app

import (
	"image/color"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The rail's space headings.
//
// A space is a name a person files related sessions under (tuios new --space,
// set-session-space). The sessions section draws a heading per space over its
// sessions, in the order and grouping tuios ls prints, and steps the sessions in
// under it. The sessions in no space come after every space at the section's
// own indent, with no heading. A rail with no session in any space draws exactly
// the rows it always did. See sessiontree.GroupBySpace.
//
// A click on a heading folds its space shut, or opens it again. The fold is
// keyed by the space's name and kept in sidebar.json. A heading is not a nav
// row, so the cursor passes over it.

// sidebarNodeIndent is the step a sessions-section row takes: the section's own
// step, and one group step more for a row drawn under a space's heading.
func (m *OS) sidebarNodeIndent(node sessiontree.Node) int {
	indent := m.sidebarRowIndent()
	if node.Space != "" && node.Kind != sessiontree.KindSpace {
		indent += sidebarGroupIndent
	}
	return indent
}

// sidebarSpaceRow renders one space's heading.
//
//	 ▾ 1 payments ───────────
//	^^ ^ ^
//	|| | the name, where a session's name would be at this level
//	|| the number switch_space_N opens, with show_numbers on
//	|the fold mark: ▾ open, ▸ folded
//	gutter
//
// It is drawn like a machine's heading, one level in: secondary ink, no bold,
// and the rule out to the spine, which is what makes it read as a heading with
// colour off. It is never lit. A folded heading ends in the count of the
// sessions it holds, since a shut group with no number reads as an empty one.
func (m *OS) sidebarSpaceRow(node sessiontree.Node, number, cw int, pal overlay.Palette, st sidebarRowState) string {
	var bg color.Color
	if st.lit() {
		bg = sidebarRowBg(st, pal)
	}
	badge, badgeW := m.sidebarSpaceNumber(number, bg, pal, false)
	collapsed := m.SidebarSpaceCollapsed(node.ID)
	mark := m.Settings.GetRailFoldOpenGlyph()
	right, rightW := "", 0
	if collapsed {
		mark = m.Settings.GetRailFoldShutGlyph()
		if node.WindowCount > 0 {
			count := strconv.Itoa(node.WindowCount)
			right = sidebarStyle(bg, pal.FgMute).Render(count)
			rightW = len(count)
		}
	}
	indent := m.sidebarNodeIndent(node)
	name := badge + sidebarStyle(bg, pal.FgDim).
		Render(overlay.Truncate(printableTitle(node.Title), max(sidebarNameAvailIn(cw, rightW, indent)-badgeW, 1)))
	gutter := sidebarGutter(false, "", bg, pal, &m.Settings)
	glyph := sidebarStyle(bg, pal.FgMute).Render(mark)
	return st.mark(pal, sidebarComposeRuledRow(indent, gutter, glyph, name, right, cw, bg, pal, &m.Settings))
}

// sidebarSpaceNumber is the number that leads a space's heading, or its row in
// the spaces section, and its width: the one switch_space_N opens, in the
// session number's muted ink, and only while show_numbers draws the session
// numbers. number is 0 for a row with none (the sessions in no space), and a
// space past 9 has no action and so wears none either.
func (m *OS) sidebarSpaceNumber(number int, bg color.Color, pal overlay.Palette, bold bool) (string, int) {
	if !m.Settings.SidebarShowNumbers || number < 1 || number > 9 {
		return "", 0
	}
	return sidebarStyle(bg, pal.FgMute).Bold(bold).Render(strconv.Itoa(number) + " "), 2
}

// SidebarSpaceCollapsed reports whether a space's group is folded shut.
func (m *OS) SidebarSpaceCollapsed(space string) bool {
	return m.SidebarCollapsedSpaces[space]
}

// SidebarToggleSpaceCollapsed folds a space's group shut, or opens it again,
// and remembers which it is, keyed by space name the way repositories are.
func (m *OS) SidebarToggleSpaceCollapsed(space string) {
	if space == "" {
		return
	}
	if m.SidebarCollapsedSpaces[space] {
		delete(m.SidebarCollapsedSpaces, space)
	} else {
		if m.SidebarCollapsedSpaces == nil {
			m.SidebarCollapsedSpaces = make(map[string]bool, 1)
		}
		m.SidebarCollapsedSpaces[space] = true
	}
	m.saveSidebarState()
}
