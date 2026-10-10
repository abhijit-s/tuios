package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// daemonWithUserConfig is startTestDaemon with a config file behind it, which
// is what every real starter has and what the shared helper deliberately does
// not: these tests are about the file tier itself.
func daemonWithUserConfig(t *testing.T, uc *config.UserConfig) (*Daemon, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))

	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true, UserConfig: uc})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)

	sp, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	return d, sp
}

// configuredTo returns a config with one option set away from its default.
func configuredTo(t *testing.T, path, value string) *config.UserConfig {
	t.Helper()
	uc := config.DefaultConfig()
	if err := config.SetOptionValue(uc, path, value); err != nil {
		t.Fatalf("SetOptionValue(%s, %s): %v", path, value, err)
	}
	return uc
}

// TestGetOptionReportsTheConfiguredValue: an option the person set in
// config.toml reads back as that value, tagged "config".
//
// It used to read back as the built-in default tagged "default", which was not
// merely unattributed but wrong: the daemon was acting on the file the whole
// time. appearance.tiling_scheme is the case that surfaced it -- the file said
// smart_split, every workspace was tiling by smart_split, and get-option said
// spiral.
func TestGetOptionReportsTheConfiguredValue(t *testing.T) {
	uc := configuredTo(t, "appearance.tiling_scheme", "smart_split")
	d, sp := daemonWithUserConfig(t, uc)
	makeSessionWithWindow(t, d, "configured")
	c := dialVerb(t, sp)

	got := result(t, c.call(t, `{"verb":"get-option","params":{"session":"configured","key":"appearance.tiling_scheme"}}`))
	if got["value"] != "smart_split" {
		t.Errorf("value = %v, want smart_split", got["value"])
	}
	if got["source"] != "config" {
		t.Errorf("source = %v, want config", got["source"])
	}
	// The built-in stays visible beside it: a caller comparing the two is how
	// "this is not stock" is answered.
	if got["default"] != "spiral" {
		t.Errorf("default = %v, want spiral", got["default"])
	}
}

// TestGetOptionPrefersTheSessionOverTheFile keeps the tiers in order. The file
// tier was inserted beneath the session override, not in front of it.
func TestGetOptionPrefersTheSessionOverTheFile(t *testing.T) {
	uc := configuredTo(t, "appearance.border_style", "double")
	d, sp := daemonWithUserConfig(t, uc)
	makeSessionWithWindow(t, d, "layered")
	c := dialVerb(t, sp)

	_ = result(t, c.call(t, `{"verb":"set-option","params":{"session":"layered","key":"appearance.border_style","value":"thick"}}`))

	got := result(t, c.call(t, `{"verb":"get-option","params":{"session":"layered","key":"appearance.border_style"}}`))
	if got["value"] != "thick" || got["source"] != "session" {
		t.Errorf("value/source = %v/%v, want thick/session", got["value"], got["source"])
	}
}

// TestGetOptionStillReportsTheDefault: a path the file leaves alone is
// unchanged by the new tier, and so is a daemon started without a file at all.
func TestGetOptionStillReportsTheDefault(t *testing.T) {
	uc := configuredTo(t, "appearance.tiling_scheme", "smart_split")
	d, sp := daemonWithUserConfig(t, uc)
	makeSessionWithWindow(t, d, "untouched")
	c := dialVerb(t, sp)

	got := result(t, c.call(t, `{"verb":"get-option","params":{"session":"untouched","key":"appearance.border_style"}}`))
	if got["source"] != "default" {
		t.Errorf("source = %v, want default for an option the file does not set", got["source"])
	}
	if got["value"] != got["default"] {
		t.Errorf("value = %v, want the default %v", got["value"], got["default"])
	}
}

// TestGetOptionFollowsAReload: the file can change under a running daemon, and
// the readout has to move with it rather than report what was loaded at start.
func TestGetOptionFollowsAReload(t *testing.T) {
	d, sp := daemonWithUserConfig(t, configuredTo(t, "appearance.tiling_scheme", "smart_split"))
	makeSessionWithWindow(t, d, "reloaded")
	c := dialVerb(t, sp)

	d.applyUserConfig(configuredTo(t, "appearance.tiling_scheme", "alternate"), true)

	got := result(t, c.call(t, `{"verb":"get-option","params":{"session":"reloaded","key":"appearance.tiling_scheme"}}`))
	if got["value"] != "alternate" {
		t.Errorf("value = %v, want alternate after the reload", got["value"])
	}
}
