#!/usr/bin/env bash
#
# install-tuios-remote.sh — cross-compile tuios from this checkout for Linux
# and swap it onto a remote machine over SSH, using the same backup +
# fresh-inode swap steps as install-tuios-local.sh (see that script's header
# for the macOS Cellar-swap routine this mirrors).
#
# Pure Go cross-compiles for Linux with zero extra tooling (CGO_ENABLED=0,
# same as every linux/* build in .goreleaser.yml), so unlike the ghostty
# backend this needs nothing installed on the build machine beyond go itself.
#
# Usage:
#   TUIOS_REMOTE_HOST=mybox scripts/install-tuios-remote.sh
#   TUIOS_REMOTE_HOST=user@host TUIOS_REMOTE_ARCH=arm64 \
#       scripts/install-tuios-remote.sh
#
#   TUIOS_REMOTE_HOST     required: an ssh(1) target (a ~/.ssh/config alias,
#                         or user@hostname). Nothing runs without it.
#   TUIOS_REMOTE_ARCH     amd64 or arm64 (default: amd64; auto-detected via
#                         `ssh $TUIOS_REMOTE_HOST uname -m` when reachable)
#   TUIOS_REMOTE_PATH     install path on the remote (default: ~/.local/bin/tuios)
#   TUIOS_SKIP_BUILD=1    reuse an already cross-compiled binary at
#                         $ROOT/.install-tuios-remote.build
#
# Notable choices (the "why"):
#   * No codesign step: that is a macOS-only AMFI (Apple Mobile File
#     Integrity) requirement. Linux has nothing equivalent to satisfy.
#   * Fresh-inode swap, done on the remote: the same ETXTBSY / vnode-cache
#     reasoning as the local script applies to a remote daemon just as much
#     as a local one, so the remote side gets rm + cp too, never an in-place
#     overwrite or a naive scp onto the live path.
#   * Backend is always pure Go: the ghostty backend's libghostty-vt would
#     need its own Linux cross-build via scripts/ghostty-lib.sh <target>
#     first. Not wired up here because nothing requires it yet — add it the
#     same way install-tuios-local.sh picked up TUIOS_BACKEND if that changes.
#
set -euo pipefail

repo_root() {
  cd "$(dirname "$0")/.." && pwd -P
}
ROOT="$(repo_root)"
cd "$ROOT"
[ -f "$ROOT/go.mod" ] || { echo "error: no tuios checkout above $0" >&2; exit 1; }

[ -n "${TUIOS_REMOTE_HOST:-}" ] || {
  echo "error: TUIOS_REMOTE_HOST is not set. Example:" >&2
  echo "    TUIOS_REMOTE_HOST=myhost $0" >&2
  exit 1
}
HOST="$TUIOS_REMOTE_HOST"
REMOTE_PATH="${TUIOS_REMOTE_PATH:-\$HOME/.local/bin/tuios}"

SHA="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

# ── 0. arch ──────────────────────────────────────────────────────────────
ARCH="${TUIOS_REMOTE_ARCH:-}"
if [ -z "$ARCH" ]; then
  if detected=$(ssh -o BatchMode=yes -o ConnectTimeout=8 "$HOST" uname -m 2>/dev/null); then
    case "$detected" in
      x86_64) ARCH=amd64 ;;
      aarch64 | arm64) ARCH=arm64 ;;
      *) echo "error: remote reports unrecognized arch '$detected'. Set TUIOS_REMOTE_ARCH." >&2; exit 1 ;;
    esac
    echo "==> detected remote arch: $detected -> $ARCH"
  else
    ARCH=amd64
    echo "==> could not reach $HOST to detect arch; defaulting to amd64 (set TUIOS_REMOTE_ARCH to override)"
  fi
fi

# ── 1. cross-compile ─────────────────────────────────────────────────────
NEW="$ROOT/.install-tuios-remote.build"
if [ "${TUIOS_SKIP_BUILD:-0}" != "1" ]; then
  buildversion="dev+${SHA}"
  commit="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    buildversion="$buildversion-dirty"
    commit="$commit-dirty"
  fi
  ldflags="-s -w -X main.builtBy=install-tuios-remote.sh -X main.version=$buildversion -X main.commit=$commit -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  echo "==> cross-compiling linux/$ARCH (pure Go backend)"
  GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
    go build -trimpath -ldflags "$ldflags" -o "$NEW" ./cmd/tuios
fi
[ -x "$NEW" ] || { echo "error: $NEW not found — build first (or unset TUIOS_SKIP_BUILD)." >&2; exit 1; }
echo "==> built: $(file -b "$NEW")"

# ── 2. ship it over ──────────────────────────────────────────────────────
STAGED="/tmp/.tuios.install.$$"
scp -q "$NEW" "$HOST:$STAGED"
echo "==> staged on $HOST:$STAGED"

# ── 3. remote backup + fresh-inode swap ──────────────────────────────────
# shellcheck disable=SC2087  # REMOTE_PATH and STAGED are meant to expand on the remote shell
ssh "$HOST" bash -s -- "$REMOTE_PATH" "$STAGED" "$SHA" <<'REMOTE'
set -euo pipefail
target="$1"
staged="$2"
sha="$3"
target="$(eval echo "$target")"

mkdir -p "$(dirname "$target")"
if [ -e "$target" ]; then
  backup="$target.pre-$sha"
  cp -p "$target" "$backup"
  echo "==> (remote) backed up existing -> $backup"
fi

old_inode="$( [ -e "$target" ] && stat -c %i "$target" 2>/dev/null || echo none )"
rm -f "$target"
cp "$staged" "$target"
rm -f "$staged"
chmod 755 "$target"
new_inode="$(stat -c %i "$target")"
echo "==> (remote) swapped (inode $old_inode -> $new_inode)"
echo "==> (remote) installed: $("$target" --version | head -1)"

socket="${XDG_RUNTIME_DIR:-/tmp/tuios-$(id -u)}/tuios.sock"
if [ -f "$socket.pid" ] && pid="$(cat "$socket.pid" 2>/dev/null)" && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  echo
  echo "A daemon (pid $pid) is still running the previous build on $target."
  echo "Restart it to run the new binary:"
  echo "    tuios kill-server"
fi
REMOTE
