package session

// A session's subagent bookkeeping across a live handoff. The window state
// carries what the subagents look like (AgentSubagents, AgentSubagentList and
// the subagents metadata key), but not the records they are kept from: when
// each was last heard of and when it ended. Without them the new image could
// neither drop a subagent that went quiet or finished fading, nor put a held
// tool change on its window, and an agent at rest, which reports nothing
// more, kept a stale list for good. See docs/design/live-handoff.md,
// section 7.3.

// handoffPaneSubagents is one pane's paneSubagents.
type handoffPaneSubagents struct {
	Subagents []handoffSubagent  `json:"subagents,omitempty"`
	Spawns    []handoffSpawnHint `json:"spawns,omitempty"`
	NextSeq   uint64             `json:"next_seq,omitempty"`
	Held      bool               `json:"held,omitempty"`
}

// handoffSubagent is one subagent.
type handoffSubagent struct {
	ID          string `json:"id"`
	AgentType   string `json:"agent_type,omitempty"`
	Description string `json:"description,omitempty"`
	CallID      string `json:"call_id,omitempty"`
	Seq         uint64 `json:"seq"`
	StartedAt   int64  `json:"started_at"`
	Seen        int64  `json:"seen"`
	EndedAt     int64  `json:"ended_at,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Target      string `json:"target,omitempty"`
	Last        string `json:"last,omitempty"`
	Tools       int    `json:"tools,omitempty"`
	Stopping    bool   `json:"stopping,omitempty"`
	State       string `json:"state,omitempty"`
	Result      string `json:"result,omitempty"`
}

// handoffSpawnHint is one launch whose subagent has not started.
type handoffSpawnHint struct {
	AgentType   string `json:"agent_type,omitempty"`
	Description string `json:"description,omitempty"`
	CallID      string `json:"call_id,omitempty"`
	At          int64  `json:"at"`
}

// handoffSubagents is the session's subagents by window id. The caller holds
// stateMu.
func (s *Session) handoffSubagentsLocked() map[string]handoffPaneSubagents {
	if len(s.agentSubagents) == 0 {
		return nil
	}
	out := make(map[string]handoffPaneSubagents, len(s.agentSubagents))
	for window, ps := range s.agentSubagents {
		if ps.empty() {
			continue
		}
		rec := handoffPaneSubagents{NextSeq: ps.nextSeq, Held: ps.held}
		for _, sa := range ps.byID {
			rec.Subagents = append(rec.Subagents, handoffSubagent{
				ID: sa.id, AgentType: sa.agentType, Description: sa.description, CallID: sa.callID, Seq: sa.seq,
				StartedAt: sa.startedAt, Seen: sa.seen, EndedAt: sa.endedAt, Tool: sa.tool, Target: sa.target,
				Last: sa.last, Tools: sa.tools, Stopping: sa.stopping, State: sa.state, Result: sa.result,
			})
		}
		for _, h := range ps.spawns {
			rec.Spawns = append(rec.Spawns, handoffSpawnHint{AgentType: h.agentType, Description: h.description, CallID: h.callID, At: h.at})
		}
		out[window] = rec
	}
	return out
}

// restoreSubagentsLocked installs carried subagents, for the windows the
// session holds. The caller holds stateMu.
func (s *Session) restoreSubagentsLocked(recs map[string]handoffPaneSubagents, shownAt int64) {
	s.subagentShownAt = shownAt
	for window, rec := range recs {
		if _, ok := findWindowState(s.state, window); !ok {
			continue
		}
		ps := &paneSubagents{byID: make(map[string]*subagent, len(rec.Subagents)), nextSeq: rec.NextSeq, held: rec.Held}
		for _, r := range rec.Subagents {
			ps.byID[r.ID] = &subagent{
				id: r.ID, agentType: r.AgentType, description: r.Description, callID: r.CallID, seq: r.Seq,
				startedAt: r.StartedAt, seen: r.Seen, endedAt: r.EndedAt, tool: r.Tool, target: r.Target,
				last: r.Last, tools: r.Tools, stopping: r.Stopping, state: r.State, result: r.Result,
			}
		}
		for _, h := range rec.Spawns {
			ps.spawns = append(ps.spawns, spawnHint{agentType: h.AgentType, description: h.Description, callID: h.CallID, at: h.At})
		}
		if ps.empty() {
			continue
		}
		if s.agentSubagents == nil {
			s.agentSubagents = make(map[string]*paneSubagents)
		}
		s.agentSubagents[window] = ps
	}
}

// subagentTimersLocked is when the session's subagent prune and flush are
// due, 0 for none, as the carried records say. The caller holds stateMu.
func (s *Session) subagentTimersLocked() (prune, flush int64) {
	prune = s.subagentExpiryLocked()
	for _, ps := range s.agentSubagents {
		if ps.held {
			flush = s.subagentShownAt + int64(subagentPublishGap)
			break
		}
	}
	return prune, flush
}
