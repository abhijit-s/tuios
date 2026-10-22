package session

// A session's agent claims across a live handoff. The window state carries
// what a pane's agent looks like (its state, harness and conversation id), but
// not who owns that state or what clears it: the claim (agentClaim), the
// harness process a hook named (harnessProc) and the guard of a shared-server
// harness (sharedGuard). Without them the new image held every carried state
// as nobody's. A hooked pane whose agent then exited was never cleared, since
// the exit sweep lets go of a report's claim only once the harness process it
// recorded has exited, and the pane stayed at its last state, in the agent
// list, until the daemon restarted. See docs/design/live-handoff.md, section
// 7.3.
//
// A pid recorded by the old image still names the same process in the new
// one: the handoff execs the daemon in place, and the panes' processes run on
// untouched. Each pid is carried with the start time it was read with, so one
// that exits and is reused is still told from the process it named.

// handoffAgentClaim is one window's agentClaim.
type handoffAgentClaim struct {
	Source       string               `json:"source,omitempty"`
	Harness      string               `json:"harness,omitempty"`
	Identity     string               `json:"identity,omitempty"`
	Misses       int                  `json:"misses,omitempty"`
	Auto         bool                 `json:"auto,omitempty"`
	SawProcess   bool                 `json:"saw_process,omitempty"`
	Blocker      bool                 `json:"blocker,omitempty"`
	Prior        *handoffPriorClaim   `json:"prior,omitempty"`
	Event        bool                 `json:"event,omitempty"`
	HerdrAt      int64                `json:"herdr_at,omitempty"`
	HerdrAnchors []handoffHerdrAnchor `json:"herdr_anchors,omitempty"`
	ScreenWins   bool                 `json:"screen_wins,omitempty"`
}

// handoffPriorClaim is an agentPriorClaim.
type handoffPriorClaim struct {
	Source       string               `json:"source,omitempty"`
	State        string               `json:"state,omitempty"`
	Harness      string               `json:"harness,omitempty"`
	HerdrAt      int64                `json:"herdr_at,omitempty"`
	HerdrAnchors []handoffHerdrAnchor `json:"herdr_anchors,omitempty"`
	ScreenWins   bool                 `json:"screen_wins,omitempty"`
}

// handoffHerdrAnchor is a herdrAnchor.
type handoffHerdrAnchor struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start"`
}

// handoffHarnessProc is a harnessProc.
type handoffHarnessProc struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start,omitempty"`
	Timed bool   `json:"timed,omitempty"`
}

// handoffSharedGuard is a sharedGuard.
type handoffSharedGuard struct {
	Harness string   `json:"harness,omitempty"`
	Owner   string   `json:"owner,omitempty"`
	History []string `json:"history,omitempty"`
	ArmedAt int64    `json:"armed_at,omitempty"`
}

func handoffAnchorsOf(as []herdrAnchor) []handoffHerdrAnchor {
	out := make([]handoffHerdrAnchor, 0, len(as))
	for _, a := range as {
		out = append(out, handoffHerdrAnchor{PID: a.pid, Start: a.start})
	}
	return out
}

func herdrAnchorsOf(as []handoffHerdrAnchor) []herdrAnchor {
	if len(as) == 0 {
		return nil
	}
	out := make([]herdrAnchor, 0, len(as))
	for _, a := range as {
		out = append(out, herdrAnchor{pid: a.PID, start: a.Start})
	}
	return out
}

// handoffAgentClaimsLocked is the session's claims, harness processes and
// shared guards by window id. The caller holds stateMu.
func (s *Session) handoffAgentClaimsLocked() (map[string]handoffAgentClaim, map[string]handoffHarnessProc, map[string]handoffSharedGuard) {
	var claims map[string]handoffAgentClaim
	for window, c := range s.agentClaims {
		rec := handoffAgentClaim{
			Source: string(c.source), Harness: c.harness, Identity: string(c.identity), Misses: c.misses,
			Auto: c.auto, SawProcess: c.sawProcess, Blocker: c.blocker, Event: c.event,
			HerdrAt: c.herdrAt, HerdrAnchors: handoffAnchorsOf(c.herdrAnchors), ScreenWins: c.screenWins,
		}
		if c.blocker {
			rec.Prior = &handoffPriorClaim{
				Source: string(c.prior.source), State: string(c.prior.state), Harness: c.prior.harness,
				HerdrAt: c.prior.herdrAt, HerdrAnchors: handoffAnchorsOf(c.prior.herdrAnchors), ScreenWins: c.prior.screenWins,
			}
		}
		if claims == nil {
			claims = make(map[string]handoffAgentClaim, len(s.agentClaims))
		}
		claims[window] = rec
	}
	var procs map[string]handoffHarnessProc
	for window, h := range s.agentHarnessPIDs {
		if procs == nil {
			procs = make(map[string]handoffHarnessProc, len(s.agentHarnessPIDs))
		}
		procs[window] = handoffHarnessProc{PID: h.pid, Start: h.start, Timed: h.timed}
	}
	var guards map[string]handoffSharedGuard
	for window, g := range s.agentSharedGuards {
		if guards == nil {
			guards = make(map[string]handoffSharedGuard, len(s.agentSharedGuards))
		}
		guards[window] = handoffSharedGuard{Harness: g.harness, Owner: g.owner, History: g.history, ArmedAt: g.armedAt}
	}
	return claims, procs, guards
}

// restoreAgentClaimsLocked installs carried claims, harness processes and
// shared guards, for the windows the session holds. A record from an image
// that carried none leaves the session with none, as before. The caller holds
// stateMu.
func (s *Session) restoreAgentClaimsLocked(rec handoffSession) {
	held := func(window string) bool {
		_, ok := findWindowState(s.state, window)
		return ok
	}
	for window, r := range rec.AgentClaims {
		if !held(window) {
			continue
		}
		c := agentClaim{
			source: AgentSource(r.Source), harness: r.Harness, identity: identityTier(r.Identity), misses: r.Misses,
			auto: r.Auto, sawProcess: r.SawProcess, blocker: r.Blocker, event: r.Event,
			herdrAt: r.HerdrAt, herdrAnchors: herdrAnchorsOf(r.HerdrAnchors), screenWins: r.ScreenWins,
		}
		if r.Prior != nil {
			c.prior = agentPriorClaim{
				source: AgentSource(r.Prior.Source), state: AgentState(r.Prior.State), harness: r.Prior.Harness,
				herdrAt: r.Prior.HerdrAt, herdrAnchors: herdrAnchorsOf(r.Prior.HerdrAnchors), screenWins: r.Prior.ScreenWins,
			}
		}
		s.setAgentClaim(window, c)
	}
	for window, r := range rec.AgentHarnessPIDs {
		if !held(window) || r.PID <= 1 {
			continue
		}
		if s.agentHarnessPIDs == nil {
			s.agentHarnessPIDs = make(map[string]harnessProc)
		}
		s.agentHarnessPIDs[window] = harnessProc{pid: r.PID, start: r.Start, timed: r.Timed}
	}
	for window, r := range rec.AgentSharedGuards {
		if !held(window) {
			continue
		}
		if s.agentSharedGuards == nil {
			s.agentSharedGuards = make(map[string]sharedGuard)
		}
		s.agentSharedGuards[window] = sharedGuard{harness: r.Harness, owner: r.Owner, history: r.History, armedAt: r.ArmedAt}
	}
}
