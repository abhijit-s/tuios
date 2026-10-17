package tuie2e

import (
	"strings"
	"testing"
	"time"
)

// The live handoff's test verbs and hooks are switched on at run time by
// TUIOS_TEST_HANDOFF=1 in the daemon's environment, in every build: the E2E
// suite drives the binary that ships, and the version-span tests drive older
// builds through the same hooks (docs/design/live-handoff.md, section 7,
// "Test hooks").
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// TestHandoffTestHooksNeedTheirVariable checks the hooks stay off without
// the variable, and stay out of reach of a restricted connection with it.
//
//   - "off": a daemon started with the hooks' own variables
//     (TUIOS_TEST_HANDOFF_HOLD_MS, TUIOS_TEST_HANDOFF_FAIL=error) but not
//     TUIOS_TEST_HANDOFF answers test-handoff with unknown_verb, and `tuios
//     daemon upgrade` goes through without a hold and without the failure the
//     variables ask for.
//   - "restricted": with the variable, a connection restricted with
//     restrict-connection is refused test-handoff, and nothing happens.
func TestHandoffTestHooksNeedTheirVariable(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		base := t.TempDir()
		killDaemon(t, base)
		if out, err := tuiosCLIEnv(t, base, []string{"TUIOS_TEST_HANDOFF=", "TUIOS_TEST_HANDOFF_HOLD_MS=5000", "TUIOS_TEST_HANDOFF_FAIL=error"}, "start-server"); err != nil {
			t.Fatalf("start-server: %v\n%s", err, out)
		}
		newCommandWindow(t, base, "e2e-hooks-off", "a", false, "sh", "-c", "exec sleep 600")
		_, verbErr := callVerb(t, base, "test-handoff", map[string]any{})
		started := time.Now()
		res, out, upErr := daemonUpgrade(t, base, copyTuios(t))
		took := time.Since(started)

		writeHandoffArtifact(t, map[string]any{"test_handoff": errString(verbErr), "upgrade": res, "output": out, "upgrade_error": errString(upErr), "seconds": took.Seconds()})
		if verbErr == nil || !strings.Contains(verbErr.Error(), "unknown_verb") {
			t.Errorf("ASSERTION: a daemon without TUIOS_TEST_HANDOFF answered test-handoff with %v, want unknown_verb", verbErr)
		}
		if upErr != nil || res["action"] != "upgraded" || res["fallback"] != false {
			t.Errorf("ASSERTION: an upgrade of a daemon without TUIOS_TEST_HANDOFF took a test hook: %v %v\n%s", res, upErr, out)
		}
		if took > 4*time.Second {
			t.Errorf("ASSERTION: the upgrade took %s, so the hold of TUIOS_TEST_HANDOFF_HOLD_MS ran without TUIOS_TEST_HANDOFF", took)
		}
	})
	t.Run("restricted", func(t *testing.T) {
		base := t.TempDir()
		startHandoffDaemon(t, base)
		newCommandWindow(t, base, "e2e-hooks-restricted", "a", false, "sh", "-c", "exec sleep 600")
		img := helloImage(t, base)
		c := dialEarly(t, base)
		if _, err := c.Call("restrict-connection", map[string]any{"scope": "all", "read_only": true}); err != nil {
			t.Fatalf("restrict-connection: %v", err)
		}
		_, verbErr := c.Call("test-handoff", map[string]any{})
		time.Sleep(500 * time.Millisecond)
		after := helloImage(t, base)

		writeHandoffArtifact(t, map[string]any{"test_handoff": errString(verbErr), "before": img, "after": after})
		if verbErr == nil || !strings.Contains(verbErr.Error(), "forbidden") {
			t.Errorf("ASSERTION: a restricted connection was not refused test-handoff: %v", verbErr)
		}
		if after.Instance != img.Instance {
			t.Errorf("ASSERTION: a restricted connection's test-handoff replaced the daemon's image")
		}
	})
}
