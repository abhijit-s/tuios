//go:build !windows

package sockpath

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
)

// Dir is the folder the daemon's socket lives in: BaseDir for the default
// server, and BaseDir/servers/<name> for a named one. It only names the
// folder; the daemon creates and checks it.
func Dir() string {
	return serverscope.SocketDir(BaseDir())
}

// BaseDir is the default server's socket folder: $XDG_RUNTIME_DIR/tuios, or
// /tmp/tuios-<uid> without a runtime directory.
func BaseDir() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "tuios")
	}
	return filepath.Join("/tmp", fmt.Sprintf("tuios-%d", os.Getuid()))
}
