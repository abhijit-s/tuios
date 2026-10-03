#!/usr/bin/env bash
#
# install-tuios-local.sh — build tuios from this checkout and install it over
# an existing local binary (including a Homebrew Cellar install) using the
# same build + backup + fresh-inode swap + (macOS) adhoc-codesign steps used
# for herdr (see herdr/scripts/install-herdr-local.sh).
#
# scripts/install.sh already covers the common case (installing fresh to
# ~/.local/bin). Reach for this one instead when the binary on PATH is a
# Homebrew install (brew install tuios) and a plain `scripts/install.sh` run
# would land a second copy that PATH order has to be untangled from.
#
#   scripts/install-tuios-local.sh                    # build + swap over the tuios on PATH
#   TUIOS_INSTALL_TARGET=~/.local/bin/tuios \
#       scripts/install-tuios-local.sh                # force a specific install path
#   TUIOS_BACKEND=ghostty scripts/install-tuios-local.sh  # build the libghostty-vt backend
#   TUIOS_SKIP_BUILD=1 scripts/install-tuios-local.sh     # swap an already-built target
#
# Notable choices (the "why"):
#   * Backend auto-detect: defaults to whatever backend the existing binary
#     reports via `--version` (pure vs ghostty), so a routine sync-and-swap
#     does not silently change backends underneath a running daemon.
#   * Fresh-inode swap (rm + cp, never in-place cp): overwriting the running
#     binary in place poisons the kernel's vnode cache and can crash a live
#     process mapping it. Removing then copying gives the new file a new
#     inode — the same reasoning as herdr's local-install script.
#   * Adhoc codesign (macOS): macOS AMFI refuses to exec an unsigned/altered
#     Mach-O; `codesign --sign -` applies a valid adhoc signature. Needed here
#     because the Homebrew bottle ships signed, and a raw local rebuild is not.
#
set -euo pipefail

repo_root() {
  cd "$(dirname "$0")/.." && pwd -P
}
ROOT="$(repo_root)"
cd "$ROOT"
[ -f "$ROOT/go.mod" ] || { echo "error: no tuios checkout above $0" >&2; exit 1; }

OS="$(uname -s)"
SHA="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

resolve_path() {
  # Resolve symlinks so we swap the real file (the Homebrew Cellar target
  # behind /opt/homebrew/bin/tuios), not the symlink.
  if command -v realpath >/dev/null 2>&1; then
    realpath "$1"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$1"
  else
    local p="$1"
    while [ -L "$p" ]; do p="$(readlink "$p")"; done
    printf '%s\n' "$p"
  fi
}

# ── 0. resolve install target + backend ────────────────────────────────────
if [ -n "${TUIOS_INSTALL_TARGET:-}" ]; then
  TARGET="$TUIOS_INSTALL_TARGET"
elif command -v tuios >/dev/null 2>&1; then
  TARGET="$(resolve_path "$(command -v tuios)")"
else
  TARGET="$HOME/.local/bin/tuios"
  echo "    no tuios on PATH; defaulting install target to $TARGET"
fi
echo "==> install target: $TARGET"

BACKEND="${TUIOS_BACKEND:-}"
if [ -z "$BACKEND" ]; then
  if [ -x "$TARGET" ] && "$TARGET" --version 2>/dev/null | grep -q 'ghostty backend'; then
    BACKEND=ghostty
  else
    BACKEND=pure
  fi
  echo "==> backend: $BACKEND (detected from existing binary)"
else
  echo "==> backend: $BACKEND (TUIOS_BACKEND override)"
fi

# ── 1. build ────────────────────────────────────────────────────────────────
NEW="$ROOT/.install-tuios-local.build"
if [ "${TUIOS_SKIP_BUILD:-0}" != "1" ]; then
  buildtags=()
  if [ "$BACKEND" = ghostty ]; then
    command -v zig >/dev/null || { echo "error: zig not found, and the ghostty backend needs it. Install zig >= 0.16, or build pure instead (TUIOS_BACKEND=pure)." >&2; exit 1; }
    cache="${GHOSTTY_VT_CACHE:-$ROOT/.ghostty-vt}"
    "$ROOT/scripts/ghostty-lib.sh" native
    export PKG_CONFIG_PATH="$cache/native/pkgconfig"
    [ -d "$PKG_CONFIG_PATH" ] || { echo "error: no pkgconfig directory at $PKG_CONFIG_PATH" >&2; exit 1; }
    export CGO_ENABLED=1
    buildtags=(-tags ghostty)
  fi

  buildversion="dev+${SHA}"
  commit="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    buildversion="$buildversion-dirty"
    commit="$commit-dirty"
  fi
  ldflags="-s -w -X main.builtBy=install-tuios-local.sh -X main.version=$buildversion -X main.commit=$commit -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  echo "==> building ($BACKEND backend)"
  go build "${buildtags[@]}" -trimpath -ldflags "$ldflags" -o "$NEW" ./cmd/tuios
fi
[ -x "$NEW" ] || { echo "error: $NEW not found — build first (or unset TUIOS_SKIP_BUILD)." >&2; exit 1; }
echo "==> built: $("$NEW" --version | head -1) ($(file -b "$NEW"))"

# ── 2. backup existing ──────────────────────────────────────────────────────
mkdir -p "$(dirname "$TARGET")"
if [ -e "$TARGET" ]; then
  BACKUP="$TARGET.pre-$SHA"
  cp -p "$TARGET" "$BACKUP"
  echo "==> backed up existing -> $BACKUP"
fi

# ── 3. fresh-inode swap ──────────────────────────────────────────────────────
OLD_INODE="$( [ -e "$TARGET" ] && { stat -f %i "$TARGET" 2>/dev/null || stat -c %i "$TARGET" 2>/dev/null; } || echo none )"
rm -f "$TARGET"
cp "$NEW" "$TARGET"
rm -f "$NEW"
chmod 755 "$TARGET"
NEW_INODE="$(stat -f %i "$TARGET" 2>/dev/null || stat -c %i "$TARGET")"
echo "==> swapped (inode $OLD_INODE -> $NEW_INODE)"

# ── 4. codesign (macOS only) ─────────────────────────────────────────────────
if [ "$OS" = "Darwin" ]; then
  codesign --force --sign - --timestamp=none "$TARGET"
  signed=0
  for _ in 1 2 3 4 5; do
    if codesign -dv "$TARGET" 2>&1 | grep -q 'flags=0x2(adhoc)'; then signed=1; break; fi
    sleep 0.3
  done
  if [ "$signed" = 1 ]; then
    echo "==> adhoc-signed (flags=0x2)"
  else
    echo "error: adhoc signature not applied." >&2; exit 1
  fi
fi

# ── 5. verify + remind ───────────────────────────────────────────────────────
echo "==> installed: $("$TARGET" --version | head -1) ($(file -b "$TARGET"))"

if [ -n "${XDG_RUNTIME_DIR:-}" ]; then
  socket="$XDG_RUNTIME_DIR/tuios/tuios.sock"
else
  socket="/tmp/tuios-$(id -u)/tuios.sock"
fi
if [ -f "$socket.pid" ] && pid="$(cat "$socket.pid" 2>/dev/null)" && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  echo
  echo "A daemon (pid $pid) is still running the previous build, and every"
  echo "attached session goes through it. Restart it to run the new binary:"
  echo "    tuios kill-server        # layouts and working directories come back with new shells"
fi
