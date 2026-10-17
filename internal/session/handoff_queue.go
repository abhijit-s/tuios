package session

// The delivery queue across a live handoff. A queue is memory only, and a
// prompt queued for an agent that is still working is what the person expects
// to reach it once it rests, upgrade or not. So the handoff carries every
// pane's entries and the time an entry was last typed into each pane, and the
// new image looks at each pane again.
//
// From before the panes stop until the exec, or the put-back, the queues are
// held: nothing is typed and nothing is queued (queue-prompt is answered
// daemon_upgrading, and the caller calls again). An entry typed after the
// manifest was written would be typed again by the new image, and one queued
// then would be lost at the exec. An entry already being typed when the
// queues are held is in the prompt gate's hands, and whether the agent took
// it is not known: the new image keeps it as stalled, which the pane's next
// working state drops, and opens the Inbox question a stall opens. See
// docs/design/live-handoff.md, section 7.3.

// handoffQueue is the queues' state.
type handoffQueue struct {
	Seq   uint64                       `json:"seq,omitempty"`
	Panes map[string]handoffPaneQueue  `json:"panes,omitempty"`
	Typed map[string]handoffTypedStamp `json:"typed,omitempty"`
}

// handoffPaneQueue is one pane's queue.
type handoffPaneQueue struct {
	Session string              `json:"session"`
	Entries []handoffQueueEntry `json:"entries"`
}

// handoffQueueEntry is a queueEntry.
type handoffQueueEntry struct {
	ID     string             `json:"id"`
	Text   string             `json:"text"`
	By     string             `json:"by"`
	From   string             `json:"from,omitempty"`
	Origin handoffQueueOrigin `json:"origin"`
	At     int64              `json:"at"`
	State  string             `json:"state"`
	Parts  []queuePart        `json:"parts,omitempty"`
	Size   int                `json:"size"`
	Typed  []string           `json:"typed,omitempty"`
}

// handoffQueueOrigin is a queueOrigin.
type handoffQueueOrigin struct {
	Kind    string `json:"kind"`
	Window  string `json:"window,omitempty"`
	Session string `json:"session,omitempty"`
	Hosted  bool   `json:"hosted,omitempty"`
	Peer    string `json:"peer,omitempty"`
	Conn    string `json:"conn,omitempty"`
}

// handoffTypedStamp is a typedStamp.
type handoffTypedStamp struct {
	At      int64  `json:"at"`
	Session string `json:"session"`
}

// errQueueHeld is queue-prompt's answer while a handoff holds the queues.
func errQueueHeld() *verbError {
	return hintedVerbError(ErrVerbDaemonUpgrading, "a live upgrade holds the delivery queues, so nothing is queued", &VerbHint{
		Detail: "Nothing was queued. Call again: the daemon answers once the upgrade is through, with every queued message still queued.",
	})
}

// hold stops every delivery and every new entry until release.
func (q *agentQueues) hold() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.held = true
}

// release ends hold, and looks again at each pane whose next entry waits.
func (q *agentQueues) release(d *Daemon) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.held = false
	for window, pq := range q.panes {
		if !pq.delivering && len(pq.entries) > 0 && pq.entries[0].state == queueWaiting {
			q.armLocked(d, window, pq, 0)
		}
	}
}

// handoffState is every pane's queue and stamp.
func (q *agentQueues) handoffState() handoffQueue {
	q.mu.Lock()
	defer q.mu.Unlock()
	hq := handoffQueue{Seq: q.seq}
	for window, pq := range q.panes {
		rec := handoffPaneQueue{Session: pq.session}
		for _, e := range pq.entries {
			o := e.origin
			rec.Entries = append(rec.Entries, handoffQueueEntry{
				ID: e.id, Text: e.text, By: e.by, From: e.from, At: e.at, State: e.state,
				Parts: e.parts, Size: e.size, Typed: e.typed,
				Origin: handoffQueueOrigin{Kind: o.kind, Window: o.window, Session: o.session, Hosted: o.hosted, Peer: o.peer, Conn: o.conn},
			})
		}
		if len(rec.Entries) == 0 {
			continue
		}
		if hq.Panes == nil {
			hq.Panes = make(map[string]handoffPaneQueue)
		}
		hq.Panes[window] = rec
	}
	for window, st := range q.typed {
		if hq.Typed == nil {
			hq.Typed = make(map[string]handoffTypedStamp)
		}
		hq.Typed[window] = handoffTypedStamp{At: st.at, Session: st.session}
	}
	return hq
}

// restoreQueue puts carried queues into the empty ones, for the panes the
// sessions hold, and returns the panes whose first entry was being typed,
// now stalled. Nothing is typed until resumeQueue.
func (d *Daemon) restoreQueue(hq handoffQueue) (stalled []string) {
	q := &d.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq = hq.Seq
	for window, st := range hq.Typed {
		q.stampLocked(window, st.Session, st.At)
	}
	for window, rec := range hq.Panes {
		pq := &paneQueue{session: rec.Session}
		for _, r := range rec.Entries {
			e := &queueEntry{
				id: r.ID, text: r.Text, by: r.By, from: r.From, at: r.At, state: r.State,
				parts: r.Parts, size: r.Size, typed: r.Typed,
				origin: queueOrigin{kind: r.Origin.Kind, window: r.Origin.Window, session: r.Origin.Session, hosted: r.Origin.Hosted, peer: r.Origin.Peer, conn: r.Origin.Conn},
			}
			if e.state == queueDelivering {
				e.state = queueStalled
				stalled = append(stalled, window)
			}
			pq.entries = append(pq.entries, e)
			pq.bytes += e.size
		}
		if q.panes == nil {
			q.panes = make(map[string]*paneQueue)
		}
		q.panes[window] = pq
		q.bytes += pq.bytes
		q.total.Add(int64(len(pq.entries)))
	}
	return stalled
}

// resumeQueue starts the carried queues once the new image is up: the Inbox
// question of each entry that was being typed, and a look at each pane whose
// next entry waits, after the rest a fresh one waits for.
func (d *Daemon) resumeQueue(stalled []string) {
	q := &d.queue
	q.mu.Lock()
	var windows []string
	for window, pq := range q.panes {
		windows = append(windows, window)
		if len(pq.entries) > 0 && pq.entries[0].state == queueWaiting {
			q.armLocked(d, window, pq, q.restFor())
		}
	}
	q.mu.Unlock()
	for _, window := range stalled {
		if sess := d.sessionHolding(window); sess != nil {
			if w, ok := findWindowState(sess.GetState(), window); ok {
				d.attention.openQueueStalled(sess.Name(), w)
			}
		}
	}
	d.publishQueued(windows...)
}
