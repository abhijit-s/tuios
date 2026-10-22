package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestSelfExecKeepsAgentClaims runs a stand-in claude through the real Claude
// Code hook in a shell pane, lets it finish a turn, hands the panes over to a
// new image, and then ends the agent the way a crashed Claude Code, or one
// whose SessionEnd hook did not land, ends: the process exits and the shell
// has the pane again, with no report.
//
// The pane must go to none and leave list-agents. What clears it is the exit
// sweep letting go of a report's claim once the harness process the hook
// recorded has exited, and both halves of that live outside the window state:
// the claim, which says a report holds the state, and the harness process,
// with the start time that tells it from a process given its pid later. The
// handoff carried neither, so the new image held the pane's idle as nobody's
// and never cleared it: the pane stayed idle, in the agent list, until the
// daemon restarted.
//
// How this could pass wrongly, written down first:
//   - A pane that was never held by the report would clear for another
//     reason. The test reads source report and state idle before the handoff,
//     and the same after it, before the agent exits.
//   - A handoff that did not happen would pass. handOver checks the image
//     changed and the daemon kept its pid.
//   - A pane that leaves the listing and comes back would pass one read. The
//     listing is read for a further four seconds once it is gone.
//
// Negative controls: NEGATIVE_CONTROLS.md, "Agent claims across a live
// handoff".
func TestSelfExecKeepsAgentClaims(t *testing.T) {
	fakeClaudeBin := filepath.Join(t.TempDir(), "claude")
	if out, err := exec.Command("go", "build", "-o", fakeClaudeBin, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	base := t.TempDir()
	useShippedLooks(base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeClaudeBin, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{bin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	writeConfig(t, base, "[daemon]\nagent_detect_seconds = 1\n")
	startHandoffDaemon(t, base)
	const session = "e2e-claims"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", session, err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "-w", "0", "--name", "pilot"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	win := windowID(t, base, session, "pilot")

	script := filepath.Join(base, "fake-claude.sh")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(fakeClaudeScript, "__TUIOS__", tuiosBin)), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		cc("", `"hook_event_name":"SessionStart","source":"startup"`),
		cc("", `"hook_event_name":"UserPromptSubmit","prompt":"Run the tests."`),
		cc("", `"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Done.","background_tasks":[],"session_crons":[]`),
		"wait rest",
	}
	scenario := filepath.Join(base, "scenario.txt")
	if err := os.WriteFile(scenario, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hookLog := filepath.Join(base, "hooks.log")

	var trace []string
	began := time.Now()
	read := func(step string) (agentStateJSON, bool, string) {
		st := readAgentState(t, base, session, win)
		ids, out := listedAgents(t, base, session)
		in := slices.Contains(ids, win)
		trace = append(trace, fmt.Sprintf("%s %-14s listed=%v state=%s source=%s harness=%q", time.Since(began).Round(10*time.Millisecond), step, in, st.State, st.Source, st.Harness))
		return st, in, out
	}
	defer func() {
		hooks, _ := os.ReadFile(hookLog)
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
		writeHandoffArtifact(t, map[string]any{"trace": trace, "hooks": string(hooks), "pane": pane})
	}()
	send := func(line string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", win, line+"\n"); err != nil {
			t.Fatalf("send %q: %v\n%s", line, err, out)
		}
	}

	send("claude " + script + " " + scenario + " " + hookLog)
	waitCapture(t, base, session, "0", "FAKE-AT rest")
	// The finished turn reads done, and idle once seen, so either is the
	// hook's state at rest.
	atRest := func(st agentStateJSON) bool {
		return (st.State == "done" || st.State == "idle") && st.Source == "report"
	}
	if st, in, out := read("before"); !atRest(st) || !in {
		t.Fatalf("before the handoff the pane is %+v, listed=%v, want the hook's finished turn, listed\n%s", st, in, out)
	}

	handOver(t, base, map[string]any{})
	if st, in, out := read("after-handoff"); !atRest(st) || !in {
		t.Fatalf("ASSERTION: after the handoff the pane is %+v, listed=%v, want the hook's finished turn, listed\n%s", st, in, out)
	}

	// The agent goes with no SessionEnd, and the shell has the pane again.
	send("")
	waitCapture(t, base, session, "0", "FAKE-DONE")
	send("echo SHELL-BACK")
	waitCapture(t, base, session, "0", "SHELL-BACK")
	var (
		st  agentStateJSON
		in  bool
		out string
	)
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		if st, in, out = read("after-exit"); st.State == "none" && !in {
			break
		}
	}
	if st.State != "none" || in {
		t.Fatalf("ASSERTION: the agent exited after the handoff and the pane is still %+v, listed=%v\n%s", st, in, out)
	}
	for end := time.Now().Add(4 * time.Second); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		if st, in, out = read("held"); st.State != "none" || in {
			t.Fatalf("ASSERTION: the pane came back as %+v, listed=%v\n%s", st, in, out)
		}
	}
}
