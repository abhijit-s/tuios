package tuie2e

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"
)

// The failure paths of a live handoff that put the daemon back in place: an
// exec that returns and a prepared handoff never committed
// (docs/design/live-handoff.md, sections 2.5 and 7). Each test checks the
// daemon carries on as the same image with every pane reading, the keeper
// gone and reaped, the next upgrade possible, and the cause reported by hello.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// upgradeFailed is hello's upgrade_failed: why the last handoff was put back.
func upgradeFailed(t *testing.T, base string) map[string]any {
	t.Helper()
	res := mustCall(t, base, "hello", map[string]any{})
	f, _ := res["upgrade_failed"].(map[string]any)
	return f
}

// counterPane opens a pane printing a numbered counter, prefix then n, and
// returns its window.
func counterPane(t *testing.T, base, sess, prefix string) string {
	t.Helper()
	win := newCommandWindow(t, base, sess, "counter", false, "sh", "-c",
		`echo pid-$$; i=0; while :; do i=$((i+1)); echo `+prefix+`$i; sleep 0.02; done`)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
	return win
}

// counterMoves reports whether the counter went on over a second, and the
// first gap in what it printed, -1 for none.
func counterMoves(t *testing.T, base, sess, win, prefix string) (bool, int) {
	t.Helper()
	first := counterLines(mustScrollback(t, base, sess, win), prefix)
	time.Sleep(time.Second)
	ticks := counterLines(mustScrollback(t, base, sess, win), prefix)
	moved := len(first) > 0 && len(ticks) > 0 && ticks[len(ticks)-1] > first[len(first)-1]
	return moved, firstGap(ticks)
}

// checkCarriedOn fails the test unless the daemon under base is image img
// still, its counter pane reading on with no gap, no keeper left, no zombie
// child, and a handoff after it going through.
func checkCarriedOn(t *testing.T, base, sess, win string, img daemonImage, art map[string]any) {
	t.Helper()
	after := helloImage(t, base)
	moved, gap := counterMoves(t, base, sess, win, "x-")
	_, keepers := awaitNoKeeper(t, base, 5*time.Second)
	zombies := defunctChildren(t, after.PID)
	next := mustCall(t, base, "test-handoff", map[string]any{})
	again := awaitNewImage(t, base, after, 20*time.Second)
	art["after"], art["counter_moved"], art["gap_at"], art["keepers_left"], art["defunct"] = after, moved, gap, keepers, zombies
	art["next_handoff"], art["next_image"] = next, again
	writeHandoffArtifact(t, art)
	if after.Instance != img.Instance || after.PID != img.PID {
		t.Fatalf("ASSERTION: the daemon changed (%+v then %+v), so nothing was put back", img, after)
	}
	if !moved || gap >= 0 {
		t.Errorf("ASSERTION: the counter pane did not read on after the put-back (moved %v, gap at %d)", moved, gap)
	}
	if len(keepers) > 0 || len(zombies) > 0 {
		t.Errorf("ASSERTION: the put-back left keepers %v and zombie children %v", keepers, zombies)
	}
	if again.PID != img.PID {
		t.Errorf("ASSERTION: the handoff after the put-back changed the daemon's pid from %d to %d", img.PID, again.PID)
	}
}

// TestHandoffExecFailureCarriesOn makes the exec return an error, with every
// carried descriptor exposed (test-handoff exec_fail). The daemon has closed
// its clients by then, and must put the rest back: the panes read again, the
// sockets accept, the keeper is released and reaped, and hello says the exec
// failed.
func TestHandoffExecFailureCarriesOn(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-execfail"
	win := counterPane(t, base, sess, "x-")
	early := dialEarly(t, base)
	img := helloImage(t, base)
	_ = mustCall(t, base, "test-handoff", map[string]any{"exec_fail": true})
	time.Sleep(500 * time.Millisecond)
	failed := upgradeFailed(t, base)
	_, earlyErr := early.Call("hello", map[string]any{})

	if failed["stage"] != "exec" || failed["clients_kept"] != false {
		t.Errorf("ASSERTION: hello reports the put-back as %v, want stage exec with the clients dropped", failed)
	}
	if earlyErr == nil {
		t.Errorf("ASSERTION: a client connected before the exec was not closed")
	}
	checkCarriedOn(t, base, sess, win, img, map[string]any{"upgrade_failed": failed, "early_client": errString(earlyErr)})
}

// TestHandoffAbandonedWhenNotCommitted answers a handoff and holds its commit
// 7 s (test-handoff commit_delay_ms), past the 5 s a prepared handoff waits
// for it. The daemon must put everything back at 5 s, answer a caller that
// waited in the backlog, say the handoff was abandoned, and not exec when the
// late commit comes.
func TestHandoffAbandonedWhenNotCommitted(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-abandon"
	win := counterPane(t, base, sess, "x-")
	img := helloImage(t, base)
	started := time.Now()
	_ = mustCall(t, base, "test-handoff", map[string]any{"commit_delay_ms": 7000})
	back := helloImage(t, base)
	answeredAfter := time.Since(started)
	failed := upgradeFailed(t, base)
	time.Sleep(time.Until(started.Add(8 * time.Second)))
	late := helloImage(t, base)

	if back.Instance != img.Instance || answeredAfter > 6500*time.Millisecond {
		t.Errorf("ASSERTION: a call made during the prepared handoff was answered after %s by %+v, want the same image within the 5 s", answeredAfter.Round(time.Millisecond), back)
	}
	if failed["stage"] != "abandoned" {
		t.Errorf("ASSERTION: hello reports the put-back as %v, want stage abandoned", failed)
	}
	if late.Instance != img.Instance {
		t.Errorf("ASSERTION: the commit that came after the abandon execed the new image")
	}
	raw, _ := json.Marshal(failed)
	checkCarriedOn(t, base, sess, win, img, map[string]any{"upgrade_failed": string(raw), "answered_after_ms": answeredAfter.Milliseconds()})
}

// TestKillServerWhileTheNewImageAdopts stops the daemon with `tuios
// kill-server` while a handoff's new image is still adopting the panes,
// held 2.5 s before it applies anything (TUIOS_TEST_HANDOFF_ADOPT_PAUSE_MS).
// A daemon stopped on purpose is not a crash: it must stop, as kill-server
// says it does, ending the programs in its panes, and the handoff's keeper
// must stand down rather than bring the old binary back.
func TestKillServerWhileTheNewImageAdopts(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base, "TUIOS_TEST_HANDOFF_ADOPT_PAUSE_MS=2500")
	const sess = "e2e-killadopt"
	win := newCommandWindow(t, base, sess, "a", false, "sh", "-c", "echo pid-$$; exec sleep 600")
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
	panePID := panePIDs(t, base, sess)[win]
	img := helloImage(t, base)
	_ = mustCall(t, base, "test-handoff", map[string]any{})
	time.Sleep(700 * time.Millisecond)
	started := time.Now()
	out, killErr := handoffCLI(t, base, 30*time.Second, "kill-server")
	took := time.Since(started)
	time.Sleep(4 * time.Second)
	daemonAlive, paneAlive := processAlive(img.PID), processAlive(panePID)
	keepers := keeperProcesses(t, base)
	var restored []string
	for _, line := range commandLinesContaining(t, base) {
		if regexp.MustCompile(`\bdaemon\b`).MatchString(line) {
			restored = append(restored, line)
		}
	}

	writeHandoffArtifact(t, map[string]any{"kill_server": out, "kill_error": errString(killErr), "took_ms": took.Milliseconds(),
		"daemon_alive": daemonAlive, "pane_alive": paneAlive, "keepers": keepers, "daemons_left": restored})
	if killErr != nil {
		t.Errorf("ASSERTION: kill-server failed while the new image adopted the panes: %v\n%s", killErr, out)
	}
	if daemonAlive || len(restored) > 0 || len(keepers) > 0 {
		t.Errorf("ASSERTION: the daemon stopped on purpose came back: pid %d alive %v, daemons %v, keepers %v", img.PID, daemonAlive, restored, keepers)
	}
	if paneAlive {
		t.Errorf("ASSERTION: the pane's program %d outlived kill-server", panePID)
	}
}
