#!/usr/bin/env bash
#
# install-tuios-release.sh — install tuios and tuios-web from a prerelease that
# scripts/publish-tuios-build.sh made, on a machine with no checkout, no Go and
# no ssh from the build machine. It needs only this file.
#
#   TUIOS_RELEASE_REPO=owner/tuios install-tuios-release.sh         # newest build (needs gh)
#   TUIOS_RELEASE_REPO=owner/tuios install-tuios-release.sh TAG     # one build (gh, or curl for a public repo)
#   TUIOS_ARCHIVE=~/tuios_linux_amd64.tar.gz install-tuios-release.sh  # offline: a copied archive
#   TUIOS_INSTALL_TARGET=~/.local/bin/tuios ...                     # force the install path
#   TUIOS_UPGRADE_DAEMON=1 ...                                      # then upgrade the running daemon live
#
# An offline archive is checked against a SHA256SUMS file beside it when there
# is one. A download is always checked.
#
# The install is the one install-tuios-local.sh does: a backup, a fresh-inode
# swap (never an in-place overwrite of a binary a process may have mapped) and,
# on macOS, an adhoc signature, with the quarantine flag that AirDrop or a
# browser leaves removed. It never uses sudo.
#
set -euo pipefail

REPO="${TUIOS_RELEASE_REPO:-}"
TAG="${1:-}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "error: no build for $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "error: no build for $(uname -m)" >&2; exit 1 ;;
esac
archive="tuios_${os}_${arch}.tar.gz"

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# ── 1. fetch and check the archive ──────────────────────────────────────────
if [ -n "${TUIOS_ARCHIVE:-}" ]; then
  cp "$TUIOS_ARCHIVE" "$WORK/$archive"
  sums="$(dirname "$TUIOS_ARCHIVE")/SHA256SUMS"
  if [ -f "$sums" ]; then cp "$sums" "$WORK/SHA256SUMS"; else echo "    no SHA256SUMS beside $TUIOS_ARCHIVE; not checked"; fi
  LABEL="$TUIOS_ARCHIVE"
else
  [ -n "$REPO" ] || { echo "error: set TUIOS_RELEASE_REPO=owner/repo, or TUIOS_ARCHIVE for a copied archive." >&2; exit 1; }
  if command -v gh >/dev/null 2>&1; then
    if [ -z "$TAG" ]; then
      TAG="$(gh release list --repo "$REPO" --limit 50 --json tagName,name,isPrerelease,createdAt \
        --jq '[.[] | select(.isPrerelease and (.name | startswith("tuios build ")))] | sort_by(.createdAt) | last | .tagName // empty')"
      [ -n "$TAG" ] || { echo "error: $REPO has no tuios build prerelease." >&2; exit 1; }
    fi
    gh release download "$TAG" --repo "$REPO" --dir "$WORK" --pattern "$archive" --pattern SHA256SUMS
  else
    [ -n "$TAG" ] || { echo "error: without gh, name the build: $0 TAG" >&2; exit 1; }
    for f in "$archive" SHA256SUMS; do
      curl -fsSL -o "$WORK/$f" "https://github.com/$REPO/releases/download/$TAG/$f"
    done
  fi
  LABEL="$REPO $TAG"
fi
if [ -f "$WORK/SHA256SUMS" ]; then
  want="$(awk -v f="$archive" '$2 == f {print $1}' "$WORK/SHA256SUMS")"
  got="$(sha256 "$WORK/$archive")"
  [ -n "$want" ] && [ "$want" = "$got" ] || { echo "error: $archive does not match SHA256SUMS." >&2; exit 1; }
  echo "==> checksum ok"
fi
tar -C "$WORK" -xzf "$WORK/$archive" tuios tuios-web
NEW_VERSION="$("$WORK/tuios" --version | head -1)"
echo "==> $LABEL: $NEW_VERSION"

# ── 2. resolve the install target ───────────────────────────────────────────
resolve_path() {
  if command -v realpath >/dev/null 2>&1; then
    realpath "$1"
  else
    local p="$1"
    while [ -L "$p" ]; do p="$(readlink "$p")"; done
    printf '%s\n' "$p"
  fi
}
if [ -n "${TUIOS_INSTALL_TARGET:-}" ]; then
  TARGET="$TUIOS_INSTALL_TARGET"
elif command -v tuios >/dev/null 2>&1; then
  TARGET="$(resolve_path "$(command -v tuios)")"
else
  TARGET="$HOME/.local/bin/tuios"
fi
echo "==> install target: $TARGET"
STAMP="$(printf '%s\n' "$NEW_VERSION" | awk '{print $3}')"
STAMP="${STAMP//[^A-Za-z0-9.+-]/_}"

# ── 3. backup, fresh-inode swap, signature ──────────────────────────────────
install_one() {
  local src="$1" dst="$2"
  mkdir -p "$(dirname "$dst")"
  if [ -e "$dst" ]; then
    cp -p "$dst" "$dst.pre-$STAMP"
    echo "==> backed up existing -> $dst.pre-$STAMP"
  fi
  rm -f "$dst"
  cp "$src" "$dst"
  chmod 755 "$dst"
  if [ "$os" = darwin ]; then
    xattr -d com.apple.quarantine "$dst" 2>/dev/null || true
    codesign --force --sign - --timestamp=none "$dst"
  fi
  echo "==> installed $dst"
}
install_one "$WORK/tuios" "$TARGET"
# tuios-web goes beside tuios, so a Homebrew symlink to it reaches the new one.
install_one "$WORK/tuios-web" "$(dirname "$TARGET")/tuios-web"

onpath="$(command -v tuios 2>/dev/null || true)"
if [ -z "$onpath" ] || [ "$(resolve_path "$onpath")" != "$(resolve_path "$TARGET")" ]; then
  echo "    tuios on PATH is ${onpath:-missing}, not $TARGET; put $(dirname "$TARGET") first on PATH"
fi
if [ "$os" = darwin ] && command -v brew >/dev/null 2>&1 && brew list --versions tuios >/dev/null 2>&1; then
  brew list --pinned 2>/dev/null | grep -qx tuios || echo "    Homebrew does not pin tuios; brew upgrade would replace this build. Run: brew pin tuios"
fi

# ── 4. the running daemon ───────────────────────────────────────────────────
if [ -n "${XDG_RUNTIME_DIR:-}" ]; then
  socket="$XDG_RUNTIME_DIR/tuios/tuios.sock"
else
  socket="/tmp/tuios-$(id -u)/tuios.sock"
fi
if [ -f "$socket.pid" ] && pid="$(cat "$socket.pid" 2>/dev/null)" && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  # The path on PATH when it is this install, so the daemon execs what a shell
  # runs as tuios after the next install too.
  live="$TARGET"
  [ -n "$onpath" ] && [ "$(resolve_path "$onpath")" = "$(resolve_path "$TARGET")" ] && live="$onpath"
  if [ "${TUIOS_UPGRADE_DAEMON:-}" = 1 ]; then
    echo "==> upgrading the daemon (pid $pid) live"
    if ! TUIOS_SERVER='' "$TARGET" daemon upgrade --binary "$live"; then
      echo "The daemon still runs the previous build. To restart it, which ends running programs:"
      echo "    tuios kill-server"
    fi
  else
    echo
    echo "A daemon (pid $pid) still runs the previous build. Move it to the new one:"
    echo "    tuios daemon upgrade --binary $live   # live: every program keeps running"
    echo "TUIOS_UPGRADE_DAEMON=1 runs the live upgrade as part of the install."
  fi
fi
