package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// sidebarSpaceDrop is where a session row being dragged would be filed if it
// were let go now: a space other than its own, or out of every space. It
// rides on the drag gesture and is worked out again on every motion, so a
// release only ever acts on the target the rail showed last.
type sidebarSpaceDrop struct {
	// Active says the pointer is over a row of a group other than the dragged
	// session's own, so the release files the session there.
	Active bool
	// Space is the target space, "" for the run of sessions in no space.
	Space string
	// Heading says the pointer is on the space's heading rather than on a row
	// inside the group. The release then puts the session last in the group,
	// because a heading names no position among the members.
	Heading bool
	// Row is the session row under the pointer when the target is the run of
	// sessions in no space, which has no heading to light. It is lit instead.
	Row string
}

// sidebarTrackSpaceDrop works out the drop target for the session row being
// dragged, with the pointer at (x, y). A space is a target only where the rail
// draws its heading over its group, which is the grouped layout; a global
// session is in no space and cannot be put in one; and a remote machine's rows
// are not session rows, so they are never a target.
func (m *OS) sidebarTrackSpaceDrop(d *sidebarDragState, x, y int) {
	d.SpaceDrop = sidebarSpaceDrop{}
	if d.Host || m.sidebarSpacesMode() != config.SidebarSpacesGrouped || m.sessionIsGlobal(d.SessionID) {
		return
	}
	hit, ok := m.sidebarSpaceDropHitAt(x, y)
	if !ok {
		return
	}
	var drop sidebarSpaceDrop
	switch hit.Kind {
	case sidebarRowSpace:
		drop = sidebarSpaceDrop{Space: hit.SessionID, Heading: true}
	case sidebarRowRepo:
		// A repository's header inside a group carries the group's space in
		// its window slot. See the sessions section's renderer.
		drop = sidebarSpaceDrop{Space: hit.WindowID}
	case sidebarRowSession:
		if hit.SessionID == d.SessionID || m.sessionIsGlobal(hit.SessionID) {
			return
		}
		drop = sidebarSpaceDrop{Space: m.sessionSpaceOf(hit.SessionID)}
		if drop.Space == "" {
			drop.Row = hit.SessionID
		}
	default:
		return
	}
	if drop.Space == m.sessionSpaceOf(d.SessionID) {
		return
	}
	drop.Active = true
	d.SpaceDrop = drop
}

// sidebarSpaceDropHitAt is the sessions-section row the pointer is on: a
// space's heading, a repository's header or a session row. Nothing answers for
// a pointer off the rail, or on a row of another section, which is how a drop
// outside the sessions section files nothing.
func (m *OS) sidebarSpaceDropHitAt(x, y int) (sidebarRowHit, bool) {
	for _, h := range m.SidebarHits {
		switch h.Kind {
		case sidebarRowSpace, sidebarRowRepo, sidebarRowSession:
			if h.Contains(x, y) {
				return h, true
			}
		}
	}
	return sidebarRowHit{}, false
}

// sidebarSpaceDropLit reports whether a sessions-section row is the drop
// target being shown: the heading of the target space, or the row in no space
// under the pointer.
func (m *OS) sidebarSpaceDropLit(kind sidebarRowKind, id string) bool {
	d := m.SidebarDrag
	if !d.Dragging || !d.SpaceDrop.Active {
		return false
	}
	switch kind {
	case sidebarRowSpace:
		return d.SpaceDrop.Heading && id == d.SpaceDrop.Space
	case sidebarRowSession:
		return d.SpaceDrop.Row != "" && id == d.SpaceDrop.Row
	}
	return false
}

// commitSpaceDrop files the dropped session under the target space through
// set-session-space, the verb the move dialog commits through, and returns the
// order the rail keeps. A drop on a heading puts the session after the last
// member of the space; a drop on a row keeps the draft order, which already
// holds the session where that row was.
func (m *OS) commitSpaceDrop(d sidebarDragState) []string {
	order := d.Order
	if d.SpaceDrop.Heading {
		order = moveAfterLast(order, d.SessionID, func(id string) bool {
			return id != d.SessionID && m.sessionSpaceOf(id) == d.SpaceDrop.Space
		})
	}
	verb, params, _ := renameVerb(RenameSessionSpace, d.SessionID, m.SessionName, d.SpaceDrop.Space)
	m.queueSidebarCmd(labelVerbCmd(m.verbDialer(), "Move", verb, params))
	return order
}

// moveAfterLast moves id to just after the last entry that matches, or leaves
// order as it is when none does.
func moveAfterLast(order []string, id string, match func(string) bool) []string {
	out := make([]string, 0, len(order))
	for _, o := range order {
		if o != id {
			out = append(out, o)
		}
	}
	last := -1
	for i, o := range out {
		if match(o) {
			last = i
		}
	}
	if last < 0 || len(out) == len(order) {
		return order
	}
	return append(out[:last+1], append([]string{id}, out[last+1:]...)...)
}

// CancelSidebarDrag drops a session-row drag in flight without committing its
// order or its space, which is what Escape means while one is held.
func (m *OS) CancelSidebarDrag() bool {
	if !m.SidebarDrag.Dragging && !m.secondaryRailView.drag.Dragging {
		return false
	}
	m.SidebarDrag = sidebarDragState{}
	m.secondaryRailView.drag = sidebarDragState{}
	m.MarkAllDirty()
	return true
}
