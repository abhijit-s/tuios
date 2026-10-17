package session

import "slices"

// What the daemon holds only in memory, carried across a live handoff so the
// processes that kept running see one daemon: the key every pane's
// TUIOS_PANE_TOKEN is signed with, the event stream's boot id, numbering and
// replay ring, and the agents' mailboxes.

// handoffEvents is an eventHub's state.
type handoffEvents struct {
	BootID string `json:"boot_id"`
	Seq    uint64 `json:"seq"`
	// Ring is the replay ring, oldest first.
	Ring              []streamEvent `json:"ring,omitempty"`
	EvictedSeq        uint64        `json:"evicted_seq,omitempty"`
	LastOutputSeq     uint64        `json:"last_output_seq,omitempty"`
	LastActivitySeq   uint64        `json:"last_activity_seq,omitempty"`
	LastTranscriptSeq uint64        `json:"last_transcript_seq,omitempty"`
	LastProgressSeq   uint64        `json:"last_progress_seq,omitempty"`
	LastFileSeq       uint64        `json:"last_file_seq,omitempty"`
	LastLayoutSeq     uint64        `json:"last_layout_seq,omitempty"`
}

// handoffMailbox is one session's agent mailbox.
type handoffMailbox struct {
	Msgs    []*AgentMessage `json:"msgs,omitempty"`
	Bytes   int             `json:"bytes,omitempty"`
	Evicted uint64          `json:"evicted,omitempty"`
}

// handoffMail is the agentBus's state. The send buckets and the open asks
// are not carried: an ask in flight ends with the old image's connections.
type handoffMail struct {
	NextID uint64                    `json:"next_id"`
	Boxes  map[string]handoffMailbox `json:"boxes,omitempty"`
}

// handoffState is the hub's state.
func (h *eventHub) handoffState() handoffEvents {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev := handoffEvents{
		BootID: h.bootID, Seq: h.seq, EvictedSeq: h.evictedSeq,
		LastOutputSeq: h.lastOutputSeq, LastActivitySeq: h.lastActivitySeq,
		LastTranscriptSeq: h.lastTranscriptSeq, LastProgressSeq: h.lastProgressSeq,
		LastFileSeq: h.lastFileSeq, LastLayoutSeq: h.lastLayoutSeq,
	}
	for i := range h.ringLen {
		ev.Ring = append(ev.Ring, h.ring[(h.ringStart+i)%len(h.ring)])
	}
	return ev
}

// restore makes a hub with no subscribers continue the carried one: the same
// boot id, and numbering and replay that go on from where it stopped.
func (h *eventHub) restore(ev handoffEvents) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ev.BootID != "" {
		h.bootID = ev.BootID
	}
	h.seq, h.evictedSeq = ev.Seq, ev.EvictedSeq
	h.lastOutputSeq, h.lastActivitySeq = ev.LastOutputSeq, ev.LastActivitySeq
	h.lastTranscriptSeq, h.lastProgressSeq = ev.LastTranscriptSeq, ev.LastProgressSeq
	h.lastFileSeq, h.lastLayoutSeq = ev.LastFileSeq, ev.LastLayoutSeq
	ring := ev.Ring
	if len(ring) > len(h.ring) {
		// A smaller ring than the old image's: the oldest go, as they would
		// have there.
		h.evictedSeq = ring[len(ring)-len(h.ring)-1].Seq
		ring = ring[len(ring)-len(h.ring):]
	}
	h.ringStart, h.ringLen = 0, copy(h.ring, ring)
}

// handoffState is the bus's mailboxes.
func (b *agentBus) handoffState() handoffMail {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := handoffMail{NextID: b.nextID, Boxes: make(map[string]handoffMailbox, len(b.boxes))}
	for name, mb := range b.boxes {
		box := handoffMailbox{Bytes: mb.bytes, Evicted: mb.evicted}
		for _, msg := range mb.msgs {
			c := *msg
			c.Attachments = slices.Clone(msg.Attachments)
			box.Msgs = append(box.Msgs, &c)
		}
		m.Boxes[name] = box
	}
	return m
}

// restore puts carried mailboxes into an empty bus.
func (b *agentBus) restore(m handoffMail) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID = m.NextID
	for name, box := range m.Boxes {
		b.boxes[name] = &agentMailbox{msgs: box.Msgs, bytes: box.Bytes, evicted: box.Evicted, buckets: map[string]*sendBucket{}}
	}
}

// handoffReplies is the person's reply history. It rides in the manifest
// with the pane screens and the pane-token key, under the same rules: a
// memfd on Linux, an unlinked owner-only file elsewhere. Losing it would make
// the reply editor forget what was sent, which a live upgrade promises not to.
type handoffReplies struct {
	Targets map[string][]replyHistoryEntry `json:"targets,omitempty"`
	// Order is the targets, least recently used first.
	Order []string `json:"order,omitempty"`
}

// handoffState is the history, every target's entries copied.
func (h *replyHistory) handoffState() handoffReplies {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := handoffReplies{Order: slices.Clone(h.order)}
	if len(h.targets) > 0 {
		r.Targets = make(map[string][]replyHistoryEntry, len(h.targets))
		for t, l := range h.targets {
			r.Targets[t] = slices.Clone(l)
		}
	}
	return r
}

// restore puts a carried history into an empty one, and counts its bytes.
func (h *replyHistory) restore(r handoffReplies) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.targets, h.order, h.bytes = r.Targets, r.Order, 0
	for _, l := range h.targets {
		for _, e := range l {
			h.bytes += entryBytes(e)
		}
	}
}

// handoffActivityRing is one pane's agent activity ring, as agent-activity
// and the recap read it.
type handoffActivityRing struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	// Seq is the newest entry's, which is also how many the pane has had.
	Seq uint64 `json:"seq"`
	// Entries are oldest first, and Turns each one's finished turns, which
	// the entry keeps off the wire.
	Entries []AgentActivityEntry `json:"entries,omitempty"`
	Turns   []uint64             `json:"turns,omitempty"`
}

// handoffState is every pane's ring.
func (a *activityStore) handoffState() []handoffActivityRing {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]handoffActivityRing, 0, len(a.rings))
	for key, ring := range a.rings {
		rec := handoffActivityRing{Session: key.session, Window: key.window, Seq: ring.seq, Entries: ring.ordered()}
		for _, e := range rec.Entries {
			rec.Turns = append(rec.Turns, e.turns)
		}
		out = append(out, rec)
	}
	return out
}

// restore puts carried rings into an empty store, numbering on from where
// each stopped.
func (a *activityStore) restore(rings []handoffActivityRing) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, rec := range rings {
		ring := &activityRing{seq: rec.Seq}
		for i, e := range rec.Entries {
			if i < len(rec.Turns) {
				e.turns = rec.Turns[i]
			}
			if len(ring.entries) < activityRingSize {
				ring.entries = append(ring.entries, e)
				continue
			}
			ring.entries[ring.start] = e
			ring.start = (ring.start + 1) % len(ring.entries)
		}
		a.rings[activityKey{rec.Session, rec.Window}] = ring
	}
}
