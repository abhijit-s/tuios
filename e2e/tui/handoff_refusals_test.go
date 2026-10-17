package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// What a live handoff refuses, with nothing changed: a pane on another
// machine, a pane this machine runs for another one, a capturing popup, a
// second upgrade while one runs, a call that came over a link, and a daemon
// that runs inside another program (docs/design/live-handoff.md, sections 2.3,
// 2.7 and 7). Windows, the last refusal, cannot be driven on macOS or Linux.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// upgradeRefused runs `tuios daemon upgrade --binary <copy>` on the daemon
// under base with env, and fails the test unless it was refused with want in
// what it said and the daemon's image the same.
func upgradeRefused(t *testing.T, base string, env []string, want string) string {
	t.Helper()
	img := helloImageEnv(t, base, env)
	out, err := tuiosCLIEnv(t, base, env, "daemon", "upgrade", "--binary", copyTuios(t))
	after := helloImageEnv(t, base, env)
	if err == nil || !strings.Contains(out, want) {
		t.Errorf("ASSERTION: the upgrade was not refused with %q: %v\n%s", want, err, out)
	}
	if after.Instance != img.Instance || after.PID != img.PID {
		t.Errorf("ASSERTION: a refused upgrade changed the daemon: %+v then %+v", img, after)
	}
	return out
}

// helloImageEnv is helloImage for a daemon run with env.
func helloImageEnv(t *testing.T, base string, env []string) daemonImage {
	t.Helper()
	out, err := tuiosCLIEnv(t, base, env, "call", "hello", "{}")
	if err != nil {
		t.Fatalf("hello: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var img daemonImage
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &img); err != nil || img.Instance == "" {
		t.Fatalf("hello carried no instance: %v\n%s", err, out)
	}
	return img
}

// TestDaemonUpgradeRefusesPanesOfOtherMachines opens a window in a session
// of this machine whose process runs on alpha, a second machine with a daemon
// of its own. This machine's daemon has no descriptor for that pane, and
// alpha's runs it for this machine, outside any session of its own: neither
// can hand it over, and both must refuse, naming why.
func TestDaemonUpgradeRefusesPanesOfOtherMachines(t *testing.T) {
	f := startGlobalFleet(t)
	before := sessionWindowHosts(t, f.here, f.env, "home")
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new-window", "-s", "home", "--host", "alpha"); err != nil {
		t.Fatalf("new-window on alpha: %v\n%s", err, out)
	}
	if _, _, ok := waitNewWindow(t, f.here, f.env, "home", before, 10*time.Second); !ok {
		t.Fatal("the window on alpha never appeared")
	}
	here := upgradeRefused(t, f.here, f.env, "a pane running on alpha")
	alpha := upgradeRefused(t, f.alpha, f.env, "here for another machine")
	writeHandoffArtifact(t, map[string]any{"here": here, "alpha": alpha})
}

// TestDaemonUpgradeRefusesWhileAPopupCaptures opens a popup that captures its
// command's output, which the daemon reads from a pipe until the command
// exits. The exec would close that pipe and the command would die of SIGPIPE
// on its next write, so an upgrade must be refused while it runs, and the
// popup's output must still arrive.
func TestDaemonUpgradeRefusesWhileAPopupCaptures(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-popup"
	newCommandWindow(t, base, sess, "a", false, "sh", "-c", "exec sleep 600")
	attachIn(t, base, sess, startOpts{})
	popup := dialEarly(t, base)
	type result struct {
		raw string
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := popup.CallWithTimeout("popup", map[string]any{"session": sess, "wait": true, "capture_stdout": true,
			"command": []string{"sh", "-c", "sleep 4; echo captured-output"}}, 30*time.Second)
		done <- result{string(raw), err}
	}()
	time.Sleep(time.Second)
	out := upgradeRefused(t, base, nil, "capturing its command's output")
	r := <-done
	writeHandoffArtifact(t, map[string]any{"upgrade": out, "popup": r.raw, "popup_error": errString(r.err)})
	if r.err != nil || !strings.Contains(r.raw, "captured-output") {
		t.Errorf("ASSERTION: the capturing popup's output did not arrive: %v %s", r.err, r.raw)
	}
}

// TestDaemonUpgradeRefusesASecondWhileOneRuns holds a handoff 2.5 s between
// the manifest and the exec, and asks for a second upgrade in the hold, on a
// connection opened before the handoff. It must be refused as busy.
func TestDaemonUpgradeRefusesASecondWhileOneRuns(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	newCommandWindow(t, base, "e2e-busy", "a", false, "sh", "-c", "exec sleep 600")
	early := dialEarly(t, base)
	img := helloImage(t, base)
	_ = mustCall(t, base, "test-handoff", map[string]any{"hold_ms": 2500})
	_, err := early.Call("daemon-upgrade", map[string]any{"binary": tuiosBin})
	awaitNewImage(t, base, img, 20*time.Second)
	writeHandoffArtifact(t, map[string]any{"second": errString(err)})
	if verr, ok := err.(*session.VerbCallError); !ok || verr.Code != session.ErrVerbBusy {
		t.Errorf("ASSERTION: a second upgrade while one ran was answered %v, want %s", err, session.ErrVerbBusy)
	}
}

// TestDaemonUpgradeRefusedOverALink calls daemon-upgrade on the daemon's link
// socket, where a hub's proxy delivers a call from another machine. An
// upgrade is run on the machine whose daemon it is: it must be refused as
// forbidden, with nothing changed.
func TestDaemonUpgradeRefusedOverALink(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	newCommandWindow(t, base, "e2e-link", "a", false, "sh", "-c", "exec sleep 600")
	img := helloImage(t, base)
	link, err := session.DialVerbClientAt(session.LinkSocketPath(frameSocket(base)), "e2e")
	if err != nil {
		t.Fatalf("dial the link socket: %v", err)
	}
	defer link.Close()
	_, callErr := link.Call("daemon-upgrade", map[string]any{"binary": tuiosBin})
	time.Sleep(500 * time.Millisecond)
	after := helloImage(t, base)
	writeHandoffArtifact(t, map[string]any{"over_link": errString(callErr)})
	if verr, ok := callErr.(*session.VerbCallError); !ok || verr.Code != session.ErrVerbForbidden {
		t.Errorf("ASSERTION: daemon-upgrade over a link was answered %v, want %s", callErr, session.ErrVerbForbidden)
	}
	if after.Instance != img.Instance {
		t.Errorf("ASSERTION: daemon-upgrade over a link replaced the daemon's image")
	}
}

// TestDaemonUpgradeRefusesAnEmbeddedDaemon starts tuios-web with no daemon
// running, so it runs the daemon inside its own process. Execing a new image
// there would replace the web server, so an upgrade must be refused, naming
// the program the daemon runs in.
func TestDaemonUpgradeRefusesAnEmbeddedDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	startTuiosWeb(t, base)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := tuiosCLIEnv(t, base, nil, "call", "hello", "{}"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tuios-web's daemon never answered")
		}
		time.Sleep(100 * time.Millisecond)
	}
	out := upgradeRefused(t, base, nil, "runs inside another program")
	writeHandoffArtifact(t, map[string]any{"upgrade": out})
}
