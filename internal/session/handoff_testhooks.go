package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Test-only verbs for the live handoff, registered only when the daemon starts
// with TUIOS_TEST_HANDOFF=1. The end-to-end tests drive the primitives through
// them before the user-facing command exists, and drive the controls through
// them after: a hold between writing the manifest and the exec, and a fork in
// the span where the descriptors are exposed. No build is meant to ship with
// the variable set, and a daemon without it does not know these verbs.

// handoffTestEnv enables the verbs.
const handoffTestEnv = "TUIOS_TEST_HANDOFF"

// handoffTestsOn says the test verbs and hooks are on.
func handoffTestsOn() bool { return os.Getenv(handoffTestEnv) == "1" }

// registerHandoffTestVerbs adds the verbs and the hooks, once, when the
// variable asks. It runs from NewDaemon: the registry is built by an init of
// its own, which an init here could run before.
func registerHandoffTestVerbs() {
	if !handoffTestsOn() {
		return
	}
	handoffTestOnce.Do(func() {
		addHandoffTestVerbs()
		setHandoffTestHooks()
	})
}

// setHandoffTestHooks sets the test-only pauses the variables ask for:
// TUIOS_TEST_HANDOFF_VERIFY_PAUSE_MS holds every fan check that long between
// its window starting and the check being recorded.
func setHandoffTestHooks() {
	if ms, err := strconv.Atoi(os.Getenv("TUIOS_TEST_HANDOFF_VERIFY_PAUSE_MS")); err == nil && ms > 0 {
		fanVerifySpawnedHook = func() { time.Sleep(time.Duration(ms) * time.Millisecond) }
	}
}

// handoffTestHold is how long daemon-upgrade holds a handoff between the
// manifest and the exec, as test-handoff's hold_ms does:
// TUIOS_TEST_HANDOFF_HOLD_MS, with the test verbs on.
func handoffTestHold() time.Duration {
	if !handoffTestsOn() {
		return 0
	}
	ms, _ := strconv.Atoi(os.Getenv("TUIOS_TEST_HANDOFF_HOLD_MS"))
	return time.Duration(max(ms, 0)) * time.Millisecond
}

// handoffTestAdoptPause holds an adoption that long before anything is
// applied, when a test asks with TUIOS_TEST_HANDOFF_ADOPT_PAUSE_MS, in the
// image a handoff execs but never in its fallback or an image a keeper
// started: a span in which the new image is not up yet.
func handoffTestAdoptPause() {
	if !handoffTestsOn() || os.Getenv(handoffFallbackEnv) != "" || os.Getenv(handoffKeptEnv) != "" {
		return
	}
	if ms, err := strconv.Atoi(os.Getenv("TUIOS_TEST_HANDOFF_ADOPT_PAUSE_MS")); err == nil && ms > 0 {
		LogBasic("Live upgrade: pausing the adoption %d ms (TUIOS_TEST_HANDOFF_ADOPT_PAUSE_MS)", ms)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

var handoffTestOnce sync.Once

func addHandoffTestVerbs() {
	reg := verbRegistry()
	reg["test-handoff"] = verbEntry{
		description: "Test only: hand every pane to a new image of this binary, in place.",
		params: []verbParam{
			{Name: "binary", Type: "string", Description: "The binary to exec. Omit for this one."},
			{Name: "hold_ms", Type: "int", Description: "Wait this long after the manifest is written, before the clients are closed."},
			{Name: "fork_in_window", Type: "bool", Description: "Start a long-lived child while the descriptors are exposed."},
			{Name: "exec_fail", Type: "bool", Description: "Fail the exec, with the descriptors exposed, as an exec that returns does."},
			{Name: "commit_delay_ms", Type: "int", Description: "Wait this long after the answer before committing."},
		},
		handler: (*Daemon).verbTestHandoff,
	}
	reg["test-handoff-stats"] = verbEntry{
		description: "Test only: what the handoff that made this image measured.",
		handler:     (*Daemon).verbTestHandoffStats,
	}
	reg["test-quiesce"] = verbEntry{
		description: "Test only: quiesce one pane and resume it, cycles times.",
		params: []verbParam{
			sessionParam, windowParam,
			{Name: "cycles", Type: "int", Description: "How many times."},
			{Name: "hold_ms", Type: "int", Description: "How long each quiesce lasts."},
		},
		handler: (*Daemon).verbTestQuiesce,
	}
}

func (d *Daemon) verbTestHandoff(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Binary        string `json:"binary"`
		HoldMS        int    `json:"hold_ms"`
		ForkInWindow  bool   `json:"fork_in_window"`
		ExecFail      bool   `json:"exec_fail"`
		CommitDelayMS int    `json:"commit_delay_ms"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, newVerbError(ErrVerbInternal, err.Error())
		}
		p.Binary = exe
	}
	req := handoffRequest{binary: p.Binary, holdAfterWrite: time.Duration(p.HoldMS) * time.Millisecond}
	switch {
	case p.ForkInWindow:
		req.inWindow = func() error {
			//tuios:allow-exec a test-only child that must not inherit the exposed descriptors
			go func() { _ = exec.Command("sleep", "20.4242").Start() }()
			time.Sleep(300 * time.Millisecond)
			return nil
		}
	case p.ExecFail:
		req.inWindow = func() error { return errors.New("the exec failed on purpose (test-handoff exec_fail)") }
	}
	run, err := d.prepareHandoff(req)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, err.Error())
	}
	delay := time.Duration(p.CommitDelayMS) * time.Millisecond
	cs.takeover = func(*bufio.Reader) {
		time.Sleep(delay)
		_ = run.commit()
	}
	return map[string]any{"pid": os.Getpid(), "panes": run.panes, "manifest_bytes": run.manifestBytes}, nil
}

func (d *Daemon) verbTestHandoffStats(_ *connState, _ json.RawMessage) (any, *verbError) {
	st := d.lastHandoff.Load()
	if st == nil {
		return map[string]any{"handoff": false}, nil
	}
	return map[string]any{"handoff": true, "stats": st}, nil
}

func (d *Daemon) verbTestQuiesce(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Cycles  int    `json:"cycles"`
		HoldMS  int    `json:"hold_ms"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	var read int64
	for range max(p.Cycles, 1) {
		ctx, cancel := context.WithTimeout(d.ctx, handoffQuiesceTimeout)
		err := pty.Quiesce(ctx)
		cancel()
		if err != nil {
			pty.Resume()
			return nil, newVerbError(ErrVerbInternal, err.Error())
		}
		before := pty.outputSeqNow()
		time.Sleep(time.Duration(p.HoldMS) * time.Millisecond)
		read += pty.outputSeqNow() - before
		pty.Resume()
	}
	return map[string]any{"cycles": max(p.Cycles, 1), "read_while_quiesced": read}, nil
}

// outputSeqNow is the number of bytes the pane has produced.
func (p *PTY) outputSeqNow() int64 {
	p.outputMu.RLock()
	defer p.outputMu.RUnlock()
	return p.outputSeq
}
