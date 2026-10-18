package app

import (
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
//	 ▾ payments ─────────────
//	^^ ^
//	|| the name, where a session's name would be at this level
//	|the fold mark: ▾ open, ▸ folded
//	gutter
//
// It is drawn like a machine's heading, one level in: secondary ink, no bold,
// and the rule out to the spine, which is what makes it read as a heading with
// colour off. It is never lit. A folded heading ends in the count of the
// sessions it holds, since a shut group with no number reads as an empty one.
func (m *OS) sidebarSpaceRow(node sessiontree.Node, cw int, pal overlay.Palette) string {
	collapsed := m.SidebarSpaceCollapsed(node.ID)
	mark := m.Settings.GetRailFoldOpenGlyph()
	right, rightW := "", 0
	if collapsed {
		mark = m.Settings.GetRailFoldShutGlyph()
		if node.WindowCount > 0 {
			count := strconv.Itoa(node.WindowCount)
			right = sidebarStyle(nil, pal.FgMute).Render(count)
			rightW = len(count)
		}
	}
	indent := m.sidebarNodeIndent(node)
	name := sidebarStyle(nil, pal.FgDim).
		Render(overlay.Truncate(printableTitle(node.Title), sidebarNameAvailIn(cw, rightW, indent)))
	gutter := sidebarGutter(false, "", nil, pal, &m.Settings)
	glyph := sidebarStyle(nil, pal.FgMute).Render(mark)
	return sidebarComposeRuledRow(indent, gutter, glyph, name, right, cw, nil, pal, &m.Settings)
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
