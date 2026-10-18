package session

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// The binary a handoff falls back to.
//
// After the exec there is no old process to return to. If the new image
// cannot adopt the panes, it re-execs the binary this image runs, with the
// same manifest, and that binary adopts them as it wrote them. By the time
// someone upgrades, an installer has usually put the new binary where the old
// one was (install.sh renames over it, Homebrew repoints its link and may
// delete the old copy), and a file with no name left cannot be run. So the
// running binary is kept at daemon start, as a hard link to its own inode
// where the file system allows, and as a copy where it does not.

// prevBinaryName is the file the running image is kept as, in handoffDir.
const prevBinaryName = "tuios.prev"

// prevBinary is the running image's binary, kept for a fallback.
type prevBinary struct {
	path string
	file fileID
	// size and modified are the file's when it was kept. A hard link shares
	// its inode with the path the binary was installed at, so a write in
	// place there (cp onto it, an editor, a truncate) changes what the link
	// names with the inode the same; the write moves the modification time.
	// The change time is no use: it moves when the inode gains or loses a
	// link, which an installer renaming a new binary over the path does.
	size     int64
	modified int64
	// sum is the copy's SHA-256, when the binary was copied rather than
	// linked: a copy has an inode of its own, so only its content says it
	// is still the binary.
	sum []byte
}

// keptBinary is the binary kept at path, as it is now.
func keptBinary(path string) (*prevBinary, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, errNotAnExecutable
	}
	return &prevBinary{path: path, file: fileIDOf(info), size: info.Size(), modified: info.ModTime().UnixNano()}, nil
}

// same says now is the file p was kept as.
func (p *prevBinary) same(now *prevBinary) bool {
	return now.file == p.file && now.size == p.size && now.modified == p.modified
}

// keepPrevBinary keeps this image's binary for a later handoff to fall back
// to. Failure is logged: it only means a handoff from this image is refused.
func (d *Daemon) keepPrevBinary() {
	d.prevMu.Lock()
	defer d.prevMu.Unlock()
	d.keepPrevBinaryLocked()
}

// keepPrevBinaryLocked is keepPrevBinary for a caller holding prevMu, which
// checkPrevBinary waits on.
func (d *Daemon) keepPrevBinaryLocked() {
	prev, err := keepRunningBinary(handoffDir())
	if err != nil {
		LogError("Live upgrade: the running binary could not be kept for a fallback, so this daemon cannot be upgraded live: %v", err)
		return
	}
	d.prev = prev
}

// keepRunningBinary links or copies the running executable into dir.
func keepRunningBinary(dir string) (*prevBinary, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, prevBinaryName)
	tmp := dst + ".new"
	_ = os.Remove(tmp)
	if err := os.Link(exe, tmp); err == nil {
		if err := os.Rename(tmp, dst); err != nil {
			return nil, err
		}
		return keptBinary(dst)
	}

	// Another file system, or a binary whose name is gone already (Linux
	// names it "(deleted)"): copy what is running.
	src := exe
	if runtime.GOOS == "linux" {
		src = "/proc/self/exe"
	}
	sum, err := copyExecutable(src, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, err
	}
	kept, err := keptBinary(dst)
	if err != nil {
		return nil, err
	}
	kept.sum = sum
	// The first run of a new file is slow on macOS. It is paid now, not in a
	// fallback.
	go func() {
		//tuios:allow-exec runs the kept copy once so a fallback does not pay its first exec
		_ = exec.Command(dst, "daemon", "handoff-accepts").Run()
	}()
	return kept, nil
}

// copyExecutable copies src to dst, owner only, and returns its SHA-256.
func copyExecutable(src, dst string) ([]byte, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700) //nolint:gosec // an executable, owner only
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		_ = out.Close()
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// checkPrevBinary says whether the kept binary is still the one kept.
func (d *Daemon) checkPrevBinary() (*prevBinary, error) {
	d.prevMu.Lock()
	prev := d.prev
	d.prevMu.Unlock()
	if prev == nil {
		return nil, errors.New("this daemon kept no copy of its binary to fall back to (see the daemon log); restart it with 'tuios kill-server' to upgrade")
	}
	now, err := keptBinary(prev.path)
	if err != nil || !prev.same(now) {
		return nil, fmt.Errorf("the copy of this daemon's binary kept for a fallback, %s, has changed; restart it with 'tuios kill-server' to upgrade", prev.path)
	}
	if prev.sum != nil {
		f, err := os.Open(prev.path)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil || !bytes.Equal(h.Sum(nil), prev.sum) {
			return nil, fmt.Errorf("the copy of this daemon's binary kept for a fallback, %s, has changed; restart it with 'tuios kill-server' to upgrade", prev.path)
		}
	}
	return prev, nil
}
