package app

import (
	"encoding/json"
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// NewSessionInSpace makes a session filed under space and switches to it, which
// is what a space heading's "New session here" row means.
//
// It asks no machine: a space heading groups the sessions of the daemon this
// client is attached to, so that daemon is where a session of the space is
// made. The create goes through the daemon's new-session verb with the space as
// a parameter, the call tuios new NAME --space SPACE makes, so the session is in
// the space from the moment it exists and a name the verb refuses is refused in
// its words. The daemon picks the name, as it does for tuios new with none.
//
// The round trip runs on a goroutine, and the switch lands through
// SessionCreatedMsg the way SidebarNewSession's does.
func (m *OS) NewSessionInSpace(space string) {
	if m.learnOff(learnNoteSessions) {
		return
	}
	if !m.CanCreateSession() {
		m.ShowNotification("Sessions need the daemon", "info", m.Settings.NotificationDuration)
		return
	}
	if space == "" {
		return
	}
	m.clearSidebarReturn()
	dial, client, ch := m.verbDialer(), m.DaemonClient, m.sessionCreateChan()
	params := map[string]any{"space": space, "width": m.GetContentWidth(), "height": m.GetUsableHeight()}
	go func() {
		name, err := createSessionVerb(dial, params)
		if err == nil {
			// The switch reads the listing for the session it lands on, and the
			// verb's connection is not the one that caches it.
			_, _ = client.RefreshSessionList()
		}
		ch <- SessionCreatedMsg{Name: name, Err: err}
	}()
}

// createSessionVerb calls new-session and returns the name the daemon gave the
// session.
func createSessionVerb(dial agentMailDial, params map[string]any) (string, error) {
	c, err := dial()
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	raw, err := c.Call("new-session", params)
	if err != nil {
		if stale := session.StaleDaemonError("new-session", err); stale != nil {
			err = stale
		}
		return "", err
	}
	var res struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Session == "" {
		return "", fmt.Errorf("the daemon did not name the new session: %s", raw)
	}
	return res.Session, nil
}
