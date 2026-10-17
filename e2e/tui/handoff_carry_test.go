package tuie2e

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// What the daemon holds about a pane, a session or itself outside the
// session state, carried across a live handoff or re-armed after it
// (docs/design/live-handoff.md, section 7.3). Each test sets one up, hands the
// panes to a new image with test-handoff, and checks what a person or a
// program sees after.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// focusInReports counts the focus-in reports a cat -v pane printed.
func focusInReports(t *testing.T, base, sess, window string) int {
	t.Helper()
	lines, err := daemonScrollback(base, sess, window, 2000)
	if err != nil {
		t.Fatalf("capture the pane: %v", err)
	}
	return strings.Count(strings.Join(lines, "\n"), "^[[I")
}

// TestSelfExecKeepsFocusReporting hands over a pane whose program turned on
// focus reporting (DECSET 1004) and echoes its input through cat -v, with no
// client attached. A client then attaches to the new image, and reports the
// pane's focus as it does on every attach: the program hears one focus-in.
// Its echo is the pane's first output in the new image, and the daemon sends
// a focus-in of its own when a write finds the mode newly on, so an image
// that started the pane believing the mode off would send a second.
func TestSelfExecKeepsFocusReporting(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-focus"
	win := newCommandWindow(t, base, sess, "probe", false, "sh", "-c", `stty -icanon -echo min 1; printf '\033[?1004h'; echo ready; exec cat -v`)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^ready`), 10*time.Second)
	if n := focusInReports(t, base, sess, win); n != 0 {
		t.Fatalf("the pane heard %d focus-in reports before any client attached", n)
	}

	handOver(t, base, map[string]any{})
	attachIn(t, base, sess, startOpts{})
	deadline := time.Now().Add(10 * time.Second)
	for focusInReports(t, base, sess, win) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	n := focusInReports(t, base, sess, win)

	writeHandoffArtifact(t, map[string]any{"focus_in_reports": n, "screen": mustScrollback(t, base, sess, win)})
	if n == 0 {
		t.Fatalf("the attached client reported no focus to the pane, so the test exercised nothing")
	}
	if n != 1 {
		t.Errorf("ASSERTION: the program heard %d focus-in reports after one client attached to the new image, want 1", n)
	}
}

// TestSelfExecPrunesMetadataWithATimeToLive gives a working agent's pane a
// metadata token that lives 7 s (set-agent-meta ttl_ms), and hands the panes
// over at once. A client attached to the new image draws the token on the
// agent's rail row. Clients draw only what the daemon pushes, and keep no
// clock of their own, so the token leaves the rail only when the daemon's
// prune drops it from the state: the new image must arm that prune again.
//
// How it could pass wrongly: the token could expire before the client
// attached, and never be drawn. The test waits for it on the rail first.
func TestSelfExecPrunesMetadataWithATimeToLive(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	startHandoffDaemon(t, base)
	const sess = "e2e-ttl"
	const token = "ttlmark7"
	newCommandWindow(t, base, sess, "agent", false, "sh", "-c", "exec sleep 600")
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", sess, "working", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	setAt := time.Now()
	_ = mustCall(t, base, "set-agent-meta", map[string]any{"session": sess, "window": "0", "tokens": map[string]any{"phase": token}, "ttl_ms": 7000})

	handOver(t, base, map[string]any{})
	term := attachIn(t, base, sess, startOpts{})
	waitForAll(t, term, 5*time.Second, "the token on the rail of a client of the new image", token)
	time.Sleep(time.Until(setAt.Add(9 * time.Second)))
	err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), token) }, 3*time.Second)

	writeHandoffArtifact(t, map[string]any{"still_drawn_at_12s": err != nil, "screen": term.Screen().Text()})
	if err != nil {
		t.Errorf("ASSERTION: the token outlived its 7 s on the rail by %s after the handoff:\n%s", time.Since(setAt.Add(7*time.Second)).Round(time.Second), term.Snapshot())
	}
}

// TestSelfExecFadesAFinishedSubagent runs a subagent through the real Claude
// Code hook in a pane, start and stop, so the pane's list holds it as done,
// fading for 4 s (TUIOS_SUBAGENT_FADE_SECONDS). The panes are handed over at
// once. An agent whose subagent finished reports nothing more, so only the
// daemon's own prune drops the finished subagent, and it needs the record of
// when the subagent ended: after the fade the list must be empty.
//
// How it could pass wrongly: the subagent could fade before the handoff. The
// test checks the list still holds it after the handoff.
func TestSelfExecFadesAFinishedSubagent(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base, "TUIOS_SUBAGENT_FADE_SECONDS=4")
	const sess = "e2e-fade"
	newCommandWindow(t, base, sess, "agent", false, "sh", "-c", "exec sleep 600")
	dir := workDirIn(t, base)
	for _, payload := range []string{
		`{"hook_event_name":"SessionStart","session_id":"fade-1","source":"startup"}`,
		`{"hook_event_name":"UserPromptSubmit","session_id":"fade-1","prompt":"look around"}`,
		`{"hook_event_name":"SubagentStart","session_id":"fade-1","agent_id":"afade0c2e71d4b5a6","agent_type":"Explore"}`,
		`{"hook_event_name":"SubagentStop","session_id":"fade-1","agent_id":"afade0c2e71d4b5a6","agent_type":"Explore","last_assistant_message":"Looked."}`,
	} {
		if explained, err := subagentHook(base, dir, sess, payload); err != nil {
			t.Fatalf("%s: %v", explained, err)
		}
	}
	stoppedAt := time.Now()
	before := readSubagents(t, base, sess)

	handOver(t, base, map[string]any{})
	carried := readSubagents(t, base, sess)
	time.Sleep(time.Until(stoppedAt.Add(6 * time.Second)))
	after := readSubagents(t, base, sess)

	writeHandoffArtifact(t, map[string]any{"before": before.raw, "after_handoff": carried.raw, "after_fade": after.raw})
	if len(before.List) != 1 || before.List[0].State != "done" {
		t.Fatalf("before the handoff the pane lists %+v, want one done subagent", before.List)
	}
	if len(carried.List) != 1 {
		t.Fatalf("the subagent faded before the handoff was checked, so the test exercised nothing: %+v", carried.List)
	}
	if len(after.List) != 0 {
		t.Errorf("ASSERTION: 6 s after it stopped, past its 4 s fade, the pane still lists %+v", after.List)
	}
}

// agentActivity is a pane's agent-activity answer: its entries' kinds and
// seqs, the ring's last seq, and its recap's finished turns.
type agentActivity struct {
	Entries []struct {
		Seq  uint64 `json:"seq"`
		Kind string `json:"kind"`
		Tool string `json:"tool"`
	} `json:"entries"`
	LastSeq uint64 `json:"last_seq"`
	Recap   struct {
		Turns int `json:"turns"`
	} `json:"recap"`
}

func readAgentActivity(t *testing.T, base, sess string) agentActivity {
	t.Helper()
	res := mustCall(t, base, "agent-activity", map[string]any{"session": sess, "window": "0", "recap": true})
	raw, _ := json.Marshal(res)
	var a agentActivity
	_ = json.Unmarshal(raw, &a)
	return a
}

// TestSelfExecKeepsAgentActivity reports a turn through the real Claude Code
// hook in a pane: a prompt, a tool call and the turn's end. agent-activity
// then lists the entries and the recap counts one turn. After the handoff it
// must list the same entries with the same numbers and count the same turn,
// and the next entry must be numbered after them: the recap of an agent that
// was mid-task is what the person reads after an upgrade.
func TestSelfExecKeepsAgentActivity(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-activity"
	newCommandWindow(t, base, sess, "agent", false, "sh", "-c", "exec sleep 600")
	dir := workDirIn(t, base)
	hook := func(payload string) {
		t.Helper()
		if explained, err := subagentHook(base, dir, sess, payload); err != nil {
			t.Fatalf("%s: %v", explained, err)
		}
	}
	hook(`{"hook_event_name":"SessionStart","session_id":"act-1","source":"startup"}`)
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"act-1","prompt":"run the tests"}`)
	hook(`{"hook_event_name":"PreToolUse","session_id":"act-1","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_use_id":"call-1"}`)
	hook(`{"hook_event_name":"PostToolUse","session_id":"act-1","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"stdout":"ok"},"tool_use_id":"call-1"}`)
	hook(`{"hook_event_name":"Stop","session_id":"act-1","last_assistant_message":"Tests pass."}`)
	before := readAgentActivity(t, base, sess)

	handOver(t, base, map[string]any{})
	after := readAgentActivity(t, base, sess)
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"act-1","prompt":"and lint"}`)
	next := readAgentActivity(t, base, sess)

	writeHandoffArtifact(t, map[string]any{"before": before, "after": after, "after_next_prompt": next})
	if len(before.Entries) < 3 || before.Recap.Turns != 1 {
		t.Fatalf("before the handoff agent-activity lists %d entries and %d turns, want a prompt, a tool call and a finished turn", len(before.Entries), before.Recap.Turns)
	}
	b, _ := json.Marshal(before.Entries)
	a, _ := json.Marshal(after.Entries)
	if string(a) != string(b) || after.LastSeq != before.LastSeq {
		t.Errorf("ASSERTION: after the handoff agent-activity lists %s (last %d), and before it %s (last %d)", a, after.LastSeq, b, before.LastSeq)
	}
	if after.Recap.Turns != before.Recap.Turns {
		t.Errorf("ASSERTION: after the handoff the recap counts %d turns, and before it %d", after.Recap.Turns, before.Recap.Turns)
	}
	if len(next.Entries) <= len(before.Entries) || next.Entries[len(before.Entries)].Seq != before.LastSeq+1 {
		t.Errorf("ASSERTION: the entries after the handoff do not go on from %d: %+v", before.LastSeq, next.Entries)
	}
}

// bufferListing is list-buffers' answer, by name: each buffer's version.
func bufferListing(t *testing.T, base string) map[string]float64 {
	t.Helper()
	res := mustCall(t, base, "list-buffers", map[string]any{})
	raw, _ := json.Marshal(res["buffers"])
	var list []struct {
		Name    string  `json:"name"`
		Version float64 `json:"version"`
	}
	_ = json.Unmarshal(raw, &list)
	out := map[string]float64{}
	for _, b := range list {
		out[b.Name] = b.Version
	}
	return out
}

// TestSelfExecKeepsPasteBuffers sets an automatic paste buffer and a named
// one, and hands the panes over. The new image must list the same buffers
// with the same versions, so a caller that read one can still act on it; show
// the named one's bytes; and name the next automatic buffer after the last,
// so no name is given twice.
func TestSelfExecKeepsPasteBuffers(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	newCommandWindow(t, base, "e2e-buffers", "a", false, "sh", "-c", "exec sleep 600")
	auto := mustCall(t, base, "set-buffer", map[string]any{"data": "first yank"})
	_ = mustCall(t, base, "set-buffer", map[string]any{"name": "keep", "data": "named\x00bytes"})
	before := bufferListing(t, base)

	handOver(t, base, map[string]any{})
	after := bufferListing(t, base)
	shown, err := callVerb(t, base, "show-buffer", map[string]any{"name": "keep"})
	next := mustCall(t, base, "set-buffer", map[string]any{"data": "second yank"})

	writeHandoffArtifact(t, map[string]any{"before": before, "after": after, "show_keep": shown, "show_error": errString(err), "first": auto, "next": next})
	if len(before) != 2 {
		t.Fatalf("before the handoff list-buffers names %v, want two buffers", before)
	}
	for name, v := range before {
		if after[name] != v {
			t.Errorf("ASSERTION: after the handoff buffer %s has version %v, and %v before it", name, after[name], v)
		}
	}
	if err != nil || shown["data"] != "named\x00bytes" {
		t.Errorf("ASSERTION: after the handoff show-buffer keep answered %v (%v)", shown, err)
	}
	if next["name"] == auto["name"] || next["name"] == nil {
		t.Errorf("ASSERTION: the first automatic buffer after the handoff is named %v, as the one before it, %v", next["name"], auto["name"])
	}
}

// queuedEntries is list-queued's entries for a session, as "id state".
func queuedEntries(t *testing.T, base, sess string) []string {
	t.Helper()
	res := mustCall(t, base, "list-queued", map[string]any{"session": sess})
	raw, _ := json.Marshal(res["entries"])
	var list []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	_ = json.Unmarshal(raw, &list)
	var out []string
	for _, e := range list {
		out = append(out, e.ID+" "+e.State)
	}
	return out
}

// queueProbeScript is an agent stand-in: each line typed into it is printed
// once, as got:<line>.
const queueProbeScript = `stty -echo; echo ready; while read l; do echo "got:$l"; done`

// queueTyped counts the times the probe printed the line marker.
func queueTyped(t *testing.T, base, sess, window, marker string) int {
	t.Helper()
	lines, err := daemonScrollback(base, sess, window, 2000)
	if err != nil {
		t.Fatalf("capture the pane: %v", err)
	}
	return strings.Count(strings.Join(lines, "\n"), "got:"+marker)
}

// TestSelfExecKeepsTheDeliveryQueue queues a message for a pane whose agent
// reports working, and hands the panes over. The new image must list the
// same entry, waiting, and type it into the pane once the agent reports
// idle, as the old image would have.
func TestSelfExecKeepsTheDeliveryQueue(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-queue"
	const marker = "queued-marker-9"
	win := newCommandWindow(t, base, sess, "agent", false, "sh", "-c", queueProbeScript)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^ready`), 10*time.Second)
	_ = mustCall(t, base, "set-agent-state", map[string]any{"session": sess, "window": win, "state": "working"})
	_ = mustCall(t, base, "queue-prompt", map[string]any{"session": sess, "window": win, "text": marker})
	before := queuedEntries(t, base, sess)

	handOver(t, base, map[string]any{})
	after := queuedEntries(t, base, sess)
	_ = mustCall(t, base, "set-agent-state", map[string]any{"session": sess, "window": win, "state": "idle"})
	deadline := time.Now().Add(10 * time.Second)
	for queueTyped(t, base, sess, win, marker) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	typed := queueTyped(t, base, sess, win, marker)

	writeHandoffArtifact(t, map[string]any{"before": before, "after": after, "typed": typed})
	if len(before) != 1 || !strings.HasSuffix(before[0], " waiting") {
		t.Fatalf("before the handoff list-queued says %v, want one waiting entry", before)
	}
	if len(after) != 1 || after[0] != before[0] {
		t.Errorf("ASSERTION: after the handoff list-queued says %v, and %v before it", after, before)
	}
	if typed != 1 {
		t.Errorf("ASSERTION: the queued message was typed %d times once the agent rested after the handoff, want once", typed)
	}
}

// TestHandoffHoldsTheDeliveryQueue queues a message for a working agent, and
// holds a handoff 3 s between the manifest and the exec. In the hold, on
// connections opened before the handoff, the agent reports idle and another
// message is queued. The old image must type nothing in the hold: the
// manifest has the message waiting, and the new image types it once the
// agent reports idle to it, so a message typed in the hold would be typed
// twice. The second queue-prompt must be answered daemon_upgrading, not
// queued into an image the exec ends.
func TestHandoffHoldsTheDeliveryQueue(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-queue-hold"
	const marker = "held-marker-4"
	win := newCommandWindow(t, base, sess, "agent", false, "sh", "-c", queueProbeScript)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^ready`), 10*time.Second)
	_ = mustCall(t, base, "set-agent-state", map[string]any{"session": sess, "window": win, "state": "working"})
	_ = mustCall(t, base, "queue-prompt", map[string]any{"session": sess, "window": win, "text": marker})

	early := dialEarly(t, base)
	img := helloImage(t, base)
	_ = mustCall(t, base, "test-handoff", map[string]any{"hold_ms": 3000})
	if _, err := early.Call("set-agent-state", map[string]any{"session": sess, "window": win, "state": "idle"}); err != nil {
		t.Fatalf("set-agent-state idle in the hold: %v", err)
	}
	_, queueErr := early.Call("queue-prompt", map[string]any{"session": sess, "window": win, "text": "in-the-hold"})
	inHold := answeredBy(t, early) == img.Instance
	awaitNewImage(t, base, img, 20*time.Second)
	// The idle in the hold came after the manifest, so the new image has the
	// agent working; it reports again, as a harness does.
	_ = mustCall(t, base, "set-agent-state", map[string]any{"session": sess, "window": win, "state": "idle"})
	deadline := time.Now().Add(10 * time.Second)
	for queueTyped(t, base, sess, win, marker) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	typed := queueTyped(t, base, sess, win, marker)

	writeHandoffArtifact(t, map[string]any{"typed": typed, "queue_in_hold": errString(queueErr), "calls_in_hold": inHold})
	if !inHold {
		t.Fatalf("the calls in the hold were answered by the new image, so the test exercised nothing")
	}
	if typed != 1 {
		t.Errorf("ASSERTION: the queued message was typed %d times across the handoff, want once", typed)
	}
	if queueErr == nil || !strings.Contains(queueErr.Error(), "daemon_upgrading") {
		t.Errorf("ASSERTION: a queue-prompt in the hold was answered %v, want daemon_upgrading", queueErr)
	}
}
