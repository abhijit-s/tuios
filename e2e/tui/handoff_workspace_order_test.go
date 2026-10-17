package tuie2e

import (
	"os"
	"testing"
	"time"
)

// TestDaemonUpgradeAppliesADisplayOrder needs a tuios that keeps a dragged
// workspace order as a display order beside the numbers, named by
// TUIOS_E2E_DISPLAY_ORDER_BIN: a build from before workspaces were renumbered
// on a move. A daemon of that build hands its panes to this one with an order
// set, and the workspaces have to come up numbered the way their pills were
// arranged, as a restart from a saved session does, with no order left over.
func TestDaemonUpgradeAppliesADisplayOrder(t *testing.T) {
	prev := os.Getenv("TUIOS_E2E_DISPLAY_ORDER_BIN")
	if prev == "" {
		t.Skip("TUIOS_E2E_DISPLAY_ORDER_BIN is not set: name a build from before workspaces were renumbered on a move")
	}
	base := t.TempDir()
	killDaemon(t, base)
	prevCLI := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLIBinEnv(t, prev, base, []string{"TUIOS_TEST_HANDOFF=1"}, args...); err != nil {
			t.Fatalf("%v with %s: %v\n%s", args, prev, err, out)
		}
	}
	prevCLI("start-server")
	prevCLI("new", renumberSession, "--detach")
	for _, ws := range []struct{ num, name string }{{"1", "EDIT"}, {"2", "REVW"}, {"3", "DPLY"}} {
		have := 0
		if ws.num == "1" {
			have = 1
		}
		for i := have; i < renumberPanes[ws.name]; i++ {
			prevCLI("new-window", "-s", renumberSession, "--workspace", ws.num, "--no-focus")
		}
		prevCLI("set-workspace-name", ws.num, ws.name, "--session", renumberSession)
	}
	// The pills dragged into DPLY, EDIT, REVW.
	mustCall(t, base, "set-workspace-order", map[string]any{"session": renumberSession, "order": []int{3, 1, 2}})

	older := helloImage(t, base)
	mustCall(t, base, "test-handoff", map[string]any{"binary": tuiosBin})
	newer := awaitNewImage(t, base, older, 20*time.Second)
	if newer.PID != older.PID {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", older.PID, newer.PID)
	}
	wantNumbering(t, base, map[string]string{"1": "DPLY", "2": "EDIT", "3": "REVW"})
	wantPanesOn(t, base, map[int]string{1: "DPLY", 2: "EDIT", 3: "REVW"})
}
