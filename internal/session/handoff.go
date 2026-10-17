package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"tuios.dev/vt"
)

// Live handoff: the daemon execs a new image of itself, and every pane's
// process keeps running under it with the same pid, the same terminal and the
// same screen. See docs/design/live-handoff.md.
//
// The daemon's pid does not change, so it stays the parent of every pane and
// the listening sockets are never closed: a caller that dials during the swap
// waits in the kernel's backlog and is answered by the new image. What crosses
// the exec is descriptors (every pane's PTY master, the sockets, the start
// lock) and a manifest on one more descriptor saying what they are.
//
// The order matters. Everything that can fail (stopping the readers, writing
// the manifest) comes before the clients are told, and a failure there puts
// every part back as it was. After the clients are closed only the exec is
// left, and an exec that fails puts the daemon back too, minus its clients.

// HandoffVersion is the manifest version this build writes, and
// MinHandoffVersion the oldest it reads. A change to what a record means, or
// to a field a record carries, moves HandoffVersion.
//
// Version 3 added the window and session records version 2 dropped: a pane's
// close_on_exit; a session's creation and last activity, workspace return,
// report colours, old names and stash index; and the daemon's plugin panes,
// protocol panes, pane creators and moved windows. See
// docs/design/live-handoff.md, section 7.3. A version 2 manifest reads with
// each of them empty, which is what a version 2 image adopted, so
// MinHandoffVersion stays at 2. An image of
// version 2 refuses a version 3 manifest, and its preflight answer says so
// before anything pauses.
const (
	HandoffVersion    = 3
	MinHandoffVersion = 2
)

// ErrHandoffUnsupported is a platform with no live handoff.
var ErrHandoffUnsupported = errors.New("live upgrade is not supported on this platform. 'tuios kill-server' restarts the daemon, which ends running programs")

var (
	errHandoffBusy     = errors.New("a live upgrade is already running")
	errHandoffEmbedded = errors.New("this daemon runs inside another program (tuios-web or the SSH server), and replacing its image would replace that program")
)

// handoffQuiesceTimeout bounds how long the panes may take to stop.
const handoffQuiesceTimeout = 10 * time.Second

// handoffCommitTimeout is how long a prepared handoff waits for the reply
// that announced it to be written before it gives up and puts everything back.
const handoffCommitTimeout = 5 * time.Second

// handoffWorkers bounds how many panes are stopped and written at once.
const handoffWorkers = 8

// handoffRequest is one handoff.
type handoffRequest struct {
	// binary is the image to exec. The preflight runs it first.
	binary string
	// holdAfterWrite and inWindow are for tests: a wait after the manifest
	// is written, and a function run while the descriptors are exposed.
	holdAfterWrite time.Duration
	inWindow       func()
}

// handoffRun is a handoff that has stopped the daemon's parts, holding what it
// needs to put each one back.
type handoffRun struct {
	d    *Daemon
	req  handoffRequest
	bin  handoffBinary
	prev *prevBinary
	file *os.File
	fds  []int
	// undo puts back what has been stopped, last first.
	undo []func()

	mu    sync.Mutex
	state int // 0 prepared, 1 committing, 2 abandoned
	timer *time.Timer

	// What the run measured, for the caller and for the next image.
	panes         int
	manifestBytes int64
	started       time.Time
	stats         HandoffStats
}

// HandoffStats is what a live handoff measured. The image that hands off
// fills in its half and carries it in the manifest; the image that adopts
// adds the other half.
type HandoffStats struct {
	// From is the build the panes were handed over by.
	From  string `json:"from"`
	Panes int    `json:"panes"`
	// ManifestBytes is the size of the manifest.
	ManifestBytes int64 `json:"manifest_bytes"`
	// PreflightMS is the run of the new binary before anything paused.
	PreflightMS float64 `json:"preflight_ms"`
	// QuiesceMS is stopping every pane, and CaptureMS writing the manifest.
	QuiesceMS float64 `json:"quiesce_ms"`
	CaptureMS float64 `json:"capture_ms"`
	// PendingMax and PendingTotal are the bytes the kernel held unread in
	// the panes' PTYs once they stopped: what the programs wrote meanwhile,
	// which the new image reads. PendingMax is the most for one pane.
	PendingMax   int `json:"pending_max"`
	PendingTotal int `json:"pending_total"`
	// StoppedAt is when the first pane stopped, in Unix nanoseconds.
	StoppedAt int64 `json:"stopped_at"`

	// ApplyMS is rebuilding every pane's emulator in the new image.
	// ApplyHeapStart, ApplyPeakHeapInuse and ApplyHeapEnd are the heap in
	// use before, at its most during, and after it, with the manifest read.
	ApplyMS            float64 `json:"apply_ms,omitempty"`
	ApplyHeapStart     uint64  `json:"apply_heap_start,omitempty"`
	ApplyPeakHeapInuse uint64  `json:"apply_peak_heap_inuse,omitempty"`
	ApplyHeapEnd       uint64  `json:"apply_heap_end,omitempty"`
	// PausedMS is from the first pane stopping to every pane reading again.
	PausedMS float64 `json:"paused_ms,omitempty"`
	// FromBackend is the emulator the panes were written down from, and
	// Backend the one this image rebuilt them in: pure or ghostty. A
	// handoff may change it.
	FromBackend string `json:"from_backend,omitempty"`
	Backend     string `json:"backend,omitempty"`
	// Fallback says the new image could not adopt the panes, and the
	// previous binary did.
	Fallback bool `json:"fallback,omitempty"`
	// Recovered says the new image died before it was up, and the keeper
	// started the previous binary in its place, with a pid of its own.
	Recovered bool `json:"recovered,omitempty"`
}

// HandoffFailure is why a handoff that answered its caller was put back, as
// the hello of the image that carried on reports it. The caller was told the
// upgrade had started, so it learns the outcome from the image that answers
// next.
type HandoffFailure struct {
	// Stage is HandoffFailedPreflight, HandoffFailedExec or
	// HandoffFailedAbandoned.
	Stage  string `json:"stage"`
	Binary string `json:"binary"`
	Error  string `json:"error"`
	// ClientsKept says the clients were not closed: a failure before the
	// exec's step that closes them.
	ClientsKept bool `json:"clients_kept"`
}

// The stages a handoff that answered can be put back at.
const (
	// HandoffFailedPreflight is a binary replaced since its preflight, whose
	// second preflight failed. Nothing was changed.
	HandoffFailedPreflight = "preflight"
	// HandoffFailedExec is an exec that returned. The clients were closed.
	HandoffFailedExec = "exec"
	// HandoffFailedAbandoned is a handoff not committed in time. Nothing
	// was changed.
	HandoffFailedAbandoned = "abandoned"
)

// handoffPaneRef is a pane the manifest carries.
type handoffPaneRef struct {
	sess   *Session
	pty    *PTY
	window string
}

// handoffDir is where the manifest is made on a platform without memfd.
func handoffDir() string {
	return filepath.Join(getResurrectionDir(), "handoff")
}

// prepareHandoff stops the daemon at a point where nothing of a pane is in
// its hands, and writes the manifest. On error nothing has changed. On success
// the daemon is stopped until commit or abandon.
func (d *Daemon) prepareHandoff(req handoffRequest) (*handoffRun, error) {
	if err := d.handoffRefusal(); err != nil {
		return nil, err
	}
	if !d.handoffMu.TryLock() {
		return nil, errHandoffBusy
	}
	d.handoffFailed.Store(nil)
	run := &handoffRun{d: d, req: req, started: time.Now()}
	run.stats.From = d.version
	// Both checks run before anything is paused, so a refusal changes
	// nothing.
	prev, err := d.checkPrevBinary()
	if err != nil {
		d.handoffMu.Unlock()
		return nil, err
	}
	run.prev = prev
	t0 := time.Now()
	if run.bin, err = preflightBinary(req.binary); err != nil {
		d.handoffMu.Unlock()
		return nil, err
	}
	run.stats.PreflightMS = msSince(t0)
	if err := run.prepare(); err != nil {
		run.putBack()
		d.handoffMu.Unlock()
		return nil, err
	}
	run.timer = time.AfterFunc(handoffCommitTimeout, func() {
		if run.abandon() {
			log.Printf("Live upgrade abandoned: it was not committed within %s", handoffCommitTimeout)
		}
	})
	return run, nil
}

func (r *handoffRun) prepare() error {
	d := r.d

	// New callers wait in the backlog from here on.
	d.pauseAccepting()
	r.undo = append(r.undo, d.resumeAccepting)

	// The daemon's other children go before the panes stop. Each would stay
	// this pid's child across the exec, cut off from the pipes it talks over
	// and never waited for, and the new image starts its own: a second copy
	// of every plugin, and an ssh link per host that nothing reads.
	d.plugins.stop()
	d.fleet.stop()
	if d.federation != nil {
		d.federation.Stop()
	}
	// A hook fired from here on is held, not started: one started after the
	// wait would still run at the exec, a child the new image never waits
	// for. A put-back starts the held ones; the exec drops them.
	if d.hooks != nil {
		d.hooks.Hold()
		d.hooks.WaitTimeout(hookDrainTimeout)
	}
	r.undo = append(r.undo, func() {
		if d.hooks != nil {
			d.hooks.Release()
		}
		if d.federation != nil {
			d.federation.Resume(d.ctx)
		}
		d.fleet.start(d.ctx)
		d.plugins.restart()
	})

	// The reaps first: a reap in progress runs the pane's exit, which may
	// close the pane, and that needs paneLife.
	shut := map[*PTY]bool{}
	shutAll := func() {
		for _, sess := range d.manager.AllSessions() {
			for _, p := range sess.allPTYs() {
				if !shut[p] {
					p.reap.shut()
					shut[p] = true
				}
			}
		}
	}
	shutAll()
	r.undo = append(r.undo, func() {
		for p := range shut {
			p.reap.open()
		}
	})
	d.manager.paneLife.Lock()
	r.undo = append(r.undo, d.manager.paneLife.Unlock)
	// A pane started between the two is in now, and its gate shuts too.
	shutAll()
	// Asked again with no pane able to start: a check or a popup that
	// started after the first answer holds a pipe by now.
	if err := d.handoffRefusal(); err != nil {
		return err
	}

	sessions := d.manager.AllSessions()
	// Before any pane stops, so the record of each session names the colours
	// its emulators were given.
	for _, sess := range sessions {
		sess.holdReportColors()
	}
	r.undo = append(r.undo, func() {
		for _, sess := range sessions {
			sess.releaseReportColors()
		}
	})
	var refs []handoffPaneRef
	for _, sess := range sessions {
		for _, w := range sess.GetState().Windows {
			if w.PTYID == "" {
				continue
			}
			p := sess.GetPTY(w.PTYID)
			if p == nil {
				continue
			}
			if p.host != "" {
				return fmt.Errorf("window %s runs on %s, and a pane on another machine cannot be handed over. Close it first", shortID(w.ID), p.host)
			}
			refs = append(refs, handoffPaneRef{sess: sess, pty: p, window: w.ID})
		}
	}
	r.panes = len(refs)

	// The manifest's descriptor is made now and written once the panes have
	// stopped, so the keeper can hold a copy of it from the start. The keeper
	// starts before any pane stops, and its start is no part of the pause.
	f, err := newHandoffFile(handoffDir())
	if err != nil {
		return fmt.Errorf("making the handoff manifest: %w", err)
	}
	r.file = f
	r.undo = append(r.undo, func() { _ = f.Close() })
	restore := append([]string{r.prev.path}, d.handoffArgs(int(f.Fd()))...) //nolint:gosec // a descriptor number
	k, err := startKeeper(r.prev.path, r.keptDescriptors(refs), restore)
	if err != nil {
		return fmt.Errorf("starting the handoff keeper: %w", err)
	}
	d.setKeeper(k)
	r.undo = append(r.undo, d.releaseKeeper)

	ctx, cancel := context.WithTimeout(d.ctx, handoffQuiesceTimeout)
	defer cancel()
	quiesced := make([]*PTY, len(refs))
	r.undo = append(r.undo, func() {
		for _, p := range quiesced {
			if p != nil {
				p.Resume()
			}
		}
	})
	t0 := time.Now()
	r.stats.StoppedAt = t0.UnixNano()
	if err := forEachBounded(len(refs), handoffWorkers, func(i int) error {
		quiesced[i] = refs[i].pty
		if err := refs[i].pty.Quiesce(ctx); err != nil {
			return fmt.Errorf("stopping pane %s: %w", shortID(refs[i].window), err)
		}
		return nil
	}); err != nil {
		return err
	}
	r.stats.QuiesceMS = msSince(t0)
	for _, ref := range refs {
		ptyspawn.ControlMaster(ref.pty.pty, func(fd int) {
			if n := pendingBytes(fd); n > 0 {
				r.stats.PendingMax = max(r.stats.PendingMax, n)
				r.stats.PendingTotal += n
			}
		})
	}

	if err := r.writeManifest(sessions, refs); err != nil {
		return fmt.Errorf("writing the handoff manifest: %w", err)
	}
	return nil
}

// msSince is the milliseconds since t.
func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

// writeManifest writes every record and collects the descriptors the exec
// keeps.
func (r *handoffRun) writeManifest(sessions []*Session, refs []handoffPaneRef) error {
	d := r.d
	mw, err := newManifestWriter(r.file)
	if err != nil {
		return err
	}
	if err := mw.json(handoffRecHeader, handoffHeader{
		Version:  HandoffVersion,
		Writer:   d.version,
		Backend:  vt.Backend,
		Sessions: len(sessions),
		Panes:    len(refs),
	}); err != nil {
		return err
	}
	for _, sess := range sessions {
		if err := mw.json(handoffRecSession, sess.handoffRecord()); err != nil {
			return err
		}
	}
	// The panes are captured and encoded handoffWorkers at a time, and
	// written in order, so at most that many records are in memory at once.
	t0 := time.Now()
	recs := make([][]byte, len(refs))
	fds := make([]int, len(refs))
	for start := 0; start < len(refs); start += handoffWorkers {
		end := min(start+handoffWorkers, len(refs))
		if err := forEachBounded(end-start, handoffWorkers, func(i int) error {
			ref := refs[start+i]
			rec, ok := ref.pty.handoffRecord(ref.sess, ref.window)
			if !ok {
				return fmt.Errorf("pane %s has no terminal device here", shortID(ref.window))
			}
			rec.State, rec.Live = ref.pty.handoffCapture()
			var err error
			recs[start+i], err = encodeGob(rec)
			fds[start+i] = rec.FD
			return err
		}); err != nil {
			return err
		}
		for i := start; i < end; i++ {
			if err := mw.record(handoffRecPane, recs[i]); err != nil {
				return err
			}
			recs[i] = nil
			r.fds = append(r.fds, fds[i])
		}
	}
	r.stats.CaptureMS = msSince(t0)
	r.stats.Panes = len(refs)

	dr := handoffDaemon{
		SocketPath:   d.manager.SocketPath(),
		Listeners:    map[string]int{},
		LockFD:       -1,
		PaneTokenKey: d.manager.paneTokenKey,
		Events:       d.events.handoffState(),
		Mail:         d.agents.handoffState(),
		Replies:      d.replyHistory.handoffState(),
		Activity:     d.activity.handoffState(),
		Fallback:     r.prev.path,
		Stats:        r.stats,
	}
	d.handoffWindowRecords(&dr)
	d.handoffSessionRecords(&dr)
	for name, l := range d.namedListeners() {
		var fd int
		if !listenerFD(l, func(n int) { fd = n }) {
			return fmt.Errorf("the %s socket has no descriptor to hand over", name)
		}
		dr.Listeners[name] = fd
		r.fds = append(r.fds, fd)
	}
	if d.startLock != nil {
		dr.LockFD = int(d.startLock.Fd()) //nolint:gosec // a descriptor number
		r.fds = append(r.fds, dr.LockFD)
	}
	// The daemon's end of the keeper's socket crosses the exec: it closes
	// only when the daemon's process is gone.
	if dr.Keeper = d.keeperRecord(); dr.Keeper.PID > 0 {
		r.fds = append(r.fds, dr.Keeper.FD)
	}
	if err := mw.json(handoffRecDaemon, dr); err != nil {
		return err
	}
	n, err := mw.finish()
	if err != nil {
		return err
	}
	r.manifestBytes = n
	if err := r.file.Sync(); err != nil {
		return err
	}
	if _, err := r.file.Seek(0, 0); err != nil {
		return err
	}
	r.fds = append(r.fds, int(r.file.Fd())) //nolint:gosec // a descriptor number
	return nil
}

// keptDescriptors is every descriptor the exec carries, which the keeper
// holds a copy of: the manifest, each pane's master, the sockets and the start
// lock.
func (r *handoffRun) keptDescriptors(refs []handoffPaneRef) []int {
	d := r.d
	out := []int{int(r.file.Fd())} //nolint:gosec // a descriptor number
	for _, ref := range refs {
		ptyspawn.ControlMaster(ref.pty.pty, func(fd int) { out = append(out, fd) })
	}
	for _, l := range d.namedListeners() {
		listenerFD(l, func(fd int) { out = append(out, fd) })
	}
	if d.startLock != nil {
		out = append(out, int(d.startLock.Fd())) //nolint:gosec // a descriptor number
	}
	return out
}

// namedListeners is every listening socket, by the name the manifest gives it.
func (d *Daemon) namedListeners() map[string]net.Listener {
	out := map[string]net.Listener{}
	for name, l := range map[string]net.Listener{
		"main": d.listener, "link": d.linkListener, "link-human": d.linkHumanListener, "herdr": d.herdrListener,
	} {
		if l != nil {
			out[name] = l
		}
	}
	return out
}

// commit closes every client and execs the new image. It returns only if
// the exec failed, after putting the daemon back.
func (r *handoffRun) commit() error {
	r.mu.Lock()
	if r.state != 0 {
		r.mu.Unlock()
		return errors.New("the live upgrade was abandoned before it could be committed")
	}
	r.state = 1
	r.mu.Unlock()
	r.timer.Stop()
	d := r.d

	if r.req.holdAfterWrite > 0 {
		time.Sleep(r.req.holdAfterWrite)
	}
	// A binary replaced since the preflight is a file the preflight did not
	// run: run it again, and pay its first exec here rather than after it.
	if !r.bin.stillThere() {
		bin, err := preflightBinary(r.bin.path)
		if err != nil {
			r.fail(HandoffFailedPreflight, err, true)
			return fmt.Errorf("%s was replaced after it was checked, and the file there now failed the check: %w", r.bin.path, err)
		}
		r.bin = bin
	}
	d.closeClientsForHandoff(r.bin.accepts.Version)

	argv := append([]string{r.bin.path}, d.handoffArgs(int(r.file.Fd()))...) //nolint:gosec // a descriptor number
	log.Printf("Live upgrade: exec %s with %d panes (manifest %d bytes, stopped %s)", r.bin.path, r.panes, r.manifestBytes, time.Since(r.started).Round(time.Millisecond))
	err := execHandoff(r.bin.path, argv, withoutEnv(os.Environ(), handoffFallbackEnv), r.fds, r.req.inWindow)
	LogError("Live upgrade: exec %s failed, the daemon carries on: %v", r.bin.path, err)
	err = fmt.Errorf("exec %s: %w", r.bin.path, err)
	r.fail(HandoffFailedExec, err, false)
	return err
}

// fail records why the run was put back, for the next hello to report, and
// puts it back. The record is made first: the put-back resumes accepting, and
// the caller's next hello must find it.
func (r *handoffRun) fail(stage string, err error, clientsKept bool) {
	r.d.handoffFailed.Store(&HandoffFailure{Stage: stage, Binary: r.bin.path, Error: err.Error(), ClientsKept: clientsKept})
	r.putBack()
	r.d.handoffMu.Unlock()
}

// abandon puts back a prepared run that was never committed. It reports
// whether it did.
func (r *handoffRun) abandon() bool {
	r.mu.Lock()
	if r.state != 0 {
		r.mu.Unlock()
		return false
	}
	r.state = 2
	r.mu.Unlock()
	r.fail(HandoffFailedAbandoned, fmt.Errorf("the upgrade was not committed within %s", handoffCommitTimeout), true)
	return true
}

// putBack undoes every step taken, last first.
func (r *handoffRun) putBack() {
	for i := len(r.undo) - 1; i >= 0; i-- {
		r.undo[i]()
	}
	r.undo = nil
}

// handoffRecord is the session as the next image rebuilds it.
func (s *Session) handoffRecord() handoffSession {
	st := s.GetState()
	rec := handoffSession{
		State:           st,
		ID:              s.ID,
		StartDir:        s.StartDir(),
		WindowStartDirs: make(map[string]string, len(st.Windows)),
	}
	for _, w := range st.Windows {
		if dir := s.windowStartDir(w.ID); dir != "" {
			rec.WindowStartDirs[w.ID] = dir
		}
	}
	if used := s.LastUsed(); !used.IsZero() {
		rec.LastUsed = used.UnixNano()
	}
	rec.ScrollbackLines = s.scrollbackLines()
	rec.Created = s.Created.UnixNano()
	rec.LastActive = s.LastActive().UnixNano()
	s.stateMu.RLock()
	rec.ReturnTo = maps.Clone(s.returnTo)
	rec.WorkspaceTrail = slices.Clone(s.workspaceTrail)
	rec.Subagents = s.handoffSubagentsLocked()
	rec.SubagentShownAt = s.subagentShownAt
	s.stateMu.RUnlock()
	s.ptysMu.RLock()
	rec.ReportColors = handoffReportColors{Bg: s.reportBgHex, Fg: s.reportFgHex, Palette: s.reportPalHex}
	s.ptysMu.RUnlock()
	return rec
}

// handoffRecord is the pane as the next image adopts it, and false for a pane
// whose bytes do not come from a descriptor here. The pane is quiesced.
func (p *PTY) handoffRecord(sess *Session, window string) (handoffPane, bool) {
	rec := handoffPane{ID: p.ID, Session: sess.ID, Window: window, FD: -1, TTY: p.TTYName(), KeepOpen: p.keepOpen != nil, CloseOnExit: p.closeOnExit}
	if !ptyspawn.ControlMaster(p.pty, func(fd int) { rec.FD = fd }) || rec.FD < 0 {
		return rec, false
	}
	if p.proc != nil {
		rec.PID = p.proc.Pid
	}
	p.terminalMu.RLock()
	rec.Width, rec.Height = p.width, p.height
	p.terminalMu.RUnlock()
	rec.SpawnW, rec.SpawnH = p.spawnW, p.spawnH
	if code, exited := p.ExitStatus(); exited {
		rec.Exited, rec.ExitCode, rec.ExitSignal = true, code, p.ExitSignal()
	}
	return rec, true
}

// allPTYs is every pane the session holds.
func (s *Session) allPTYs() []*PTY {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	out := make([]*PTY, 0, len(s.ptys))
	for _, p := range s.ptys {
		out = append(out, p)
	}
	return out
}

// forEachBounded runs fn for 0..n-1 on at most workers goroutines and returns
// the first error. A panic in fn is returned as an error: a recover covers
// only its own goroutine, so one on a worker would not reach the caller's,
// and in an adopting image it would end the process instead of letting the
// fallback run.
func forEachBounded(n, workers int, fn func(i int) error) error {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
		next  = make(chan int)
	)
	run := func(i int) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
			}
		}()
		return fn(i)
	}
	for range min(workers, n) {
		wg.Go(func() {
			for i := range next {
				if err := run(i); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
	return first
}
