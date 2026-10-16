package app

import (
	"slices"
	"sort"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Removing and closing a space, the two ways a space heading's menu ends one.
//
// Remove takes every session out of the space through the daemon's
// remove-space verb, the call tuios remove-space makes: the sessions join the
// ones in no space and keep running, so it asks nothing. Close ends every
// session in the space through kill-space, the call tuios kill-space --force
// makes, and so it is only ever reached through the session-close dialog.

// RemoveSpace takes every session out of space. The round trip runs as a
// command, and its outcome lands as a RenameAppliedMsg, as a space rename's
// does: a refusal is shown in the verb's words, and success refreshes the
// listing the rail draws its headings from.
func (m *OS) RemoveSpace(space string) tea.Cmd {
	if space == "" {
		return nil
	}
	return labelVerbCmd(m.verbDialer(), "Remove space", "remove-space", map[string]any{"space": space})
}

// spaceMembers is the sessions filed under space, by name, sorted as
// kill-space orders them. The attached session's space is read from its own
// state, which a push keeps current; every other one from the cached listing,
// so the dialog never blocks on the daemon to say what it is about.
func (m *OS) spaceMembers(space string) []string {
	if space == "" {
		return nil
	}
	var names []string
	if m.SessionSpace == space && m.SessionName != "" {
		names = append(names, m.SessionName)
	}
	if m.DaemonClient != nil {
		for _, info := range m.DaemonClient.CachedSessions() {
			if info.Name != m.SessionName && info.Space == space {
				names = append(names, info.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// spaceToll counts what closing the sessions in members would take down.
func (m *OS) spaceToll(members []string) sessionToll {
	var t sessionToll
	for _, name := range members {
		one := m.SessionTollFor(name)
		t.Panes += one.Panes
		t.Working += one.Working
		t.Blocked += one.Blocked
	}
	return t
}

// OpenSpaceClose raises the close confirmation for every session in space. A
// space this client knows no session of says so and asks nothing.
func (m *OS) OpenSpaceClose(space string) {
	if len(m.spaceMembers(space)) == 0 {
		m.ShowNotification("No session is in space '"+printableTitle(space)+"'", "info", m.Settings.NotificationDuration)
		return
	}
	m.CloseSessionClose()
	m.SessionCloseSpace = space
	m.ShowSessionClose = true
}

// CloseSpace kills every session in space through kill-space.
//
// When the attached session is one of them this client goes with it, as
// closing the attached session from the same dialog does (QuitSession): the
// kill is made inline, the client is cleaned up, and it quits, with
// QuitRequested set first so the daemon's notice that the session ended reads
// as the person's own doing. A failed kill leaves the client where it is and
// says why. Otherwise the kill runs as a command and the client stays.
func (m *OS) CloseSpace(space string) tea.Cmd {
	params := map[string]any{"space": space, "force": true}
	if !slices.Contains(m.spaceMembers(space), m.SessionName) {
		return labelVerbCmd(m.verbDialer(), "Close space", "kill-space", params)
	}
	m.QuitRequested = true
	if err := callVerb(m.verbDialer(), "kill-space", params); err != nil {
		m.QuitRequested = false
		m.ShowNotification("Close space failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	m.Cleanup()
	return tea.Quit
}

// callVerb makes one verb call and reports only whether it was refused.
func callVerb(dial agentMailDial, verb string, params map[string]any) error {
	c, err := dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Call(verb, params); err != nil {
		if stale := session.StaleDaemonError(verb, err); stale != nil {
			return stale
		}
		return err
	}
	return nil
}
