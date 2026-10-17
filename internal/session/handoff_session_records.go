package session

// What the daemon keeps about a session outside its state, carried across a
// live handoff (docs/design/live-handoff.md, section 7.3): the names sessions
// were renamed from, and each session's stash index.
//
// A pane started before a rename still has the old name in TUIOS_SESSION, and
// a call naming it reaches the session only through the alias. The stashed
// files stay on disk through the exec, since the new image runs no start-time
// sweep, but the index is memory: without it stash list and get answer empty,
// and the files are never evicted or removed with their session. The index is
// carried rather than rebuilt from the directory, because the files hold
// neither the path each was copied from nor the order of use eviction walks,
// and rebuilding the hashes would read up to the whole store in the pause.

// handoffStashBox is a stashBox.
type handoffStashBox struct {
	Dir     string       `json:"dir"`
	Entries []stashEntry `json:"entries"`
	Bytes   int64        `json:"bytes"`
	Evicted uint64       `json:"evicted"`
}

// handoffSessionRecords fills the daemon record's session maps.
func (d *Daemon) handoffSessionRecords(dr *handoffDaemon) {
	m := d.manager
	m.mu.RLock()
	for name, id := range m.aliases {
		if dr.Aliases == nil {
			dr.Aliases = map[string]string{}
		}
		dr.Aliases[name] = id
	}
	m.mu.RUnlock()

	if d.stash == nil {
		return
	}
	d.stash.mu.Lock()
	defer d.stash.mu.Unlock()
	for id, b := range d.stash.boxes {
		rec := handoffStashBox{Dir: b.dir, Bytes: b.bytes, Evicted: b.evicted, Entries: make([]stashEntry, 0, len(b.entries))}
		for _, e := range b.entries {
			rec.Entries = append(rec.Entries, *e)
		}
		if dr.Stash == nil {
			dr.Stash = map[string]handoffStashBox{}
		}
		dr.Stash[id] = rec
	}
}

// restoreSessionRecords puts the daemon record's session maps back once the
// sessions are adopted, before any call is served. A record whose session was
// not adopted is dropped, and an alias never shadows a session of its name.
func (d *Daemon) restoreSessionRecords(dr handoffDaemon) {
	m := d.manager
	m.mu.Lock()
	for name, id := range dr.Aliases {
		if m.byID[id] != nil && m.sessions[name] == nil {
			m.aliases[name] = id
		}
	}
	m.mu.Unlock()

	if d.stash == nil || len(dr.Stash) == 0 {
		return
	}
	d.stash.mu.Lock()
	defer d.stash.mu.Unlock()
	for id, rec := range dr.Stash {
		if m.GetSessionByID(id) == nil || rec.Dir == "" {
			continue
		}
		b := &stashBox{dir: rec.Dir, bytes: rec.Bytes, evicted: rec.Evicted, entries: make([]*stashEntry, 0, len(rec.Entries))}
		for _, e := range rec.Entries {
			b.entries = append(b.entries, &e)
		}
		d.stash.boxes[id] = b
	}
}
