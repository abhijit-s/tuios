#!/usr/bin/env bash
#
# publish-tuios-build.sh — build tuios and tuios-web from this checkout for
# each target system and publish them as a GitHub prerelease, so a machine with
# no ssh from here installs them with scripts/install-tuios-release.sh.
#
#   scripts/publish-tuios-build.sh                          # build, package, publish
#   TUIOS_DRY_RUN=1 scripts/publish-tuios-build.sh          # build and package only
#   TUIOS_TARGETS="darwin/arm64 linux/amd64" \
#       scripts/publish-tuios-build.sh                      # choose the targets
#   TUIOS_RELEASE_REPO=owner/tuios scripts/publish-tuios-build.sh
#   TUIOS_RELEASE_TAG=integ-x scripts/publish-tuios-build.sh
#   TUIOS_KEEP=5 scripts/publish-tuios-build.sh             # prereleases to keep
#
# gh publishes with whatever account GH_TOKEN or gh's active account names:
#   GH_TOKEN=$(gh auth token --user OWNER) scripts/publish-tuios-build.sh
#
# Notable choices (the "why"):
#   * The tag is the branch name with "/" as "-", never v*: tuios update reads
#     only upstream's releases, and a v* tag on the fork would read as a release
#     to anyone browsing it. The release name starts with "tuios build ", which
#     is how install-tuios-release.sh finds the newest and how pruning finds
#     what this script made.
#   * The version is the one install-tuios-local.sh stamps, dev+<release>.<sha>,
#     so tuios treats it as a source build and offers no update over it.
#   * A dirty tree is refused: a published build has to be a commit someone can
#     check out. The commit has to be on the fork too, since the tag points at it.
#   * The pure-Go backend only: the ghostty backend does not cross-compile.
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$ROOT"
[ -f "$ROOT/go.mod" ] || { echo "error: no tuios checkout above $0" >&2; exit 1; }

TARGETS="${TUIOS_TARGETS:-darwin/arm64 linux/amd64 linux/arm64}"
KEEP="${TUIOS_KEEP:-5}"
DRY_RUN="${TUIOS_DRY_RUN:-0}"

if [ -n "$(git status --porcelain)" ]; then
  echo "error: the checkout has uncommitted changes; commit them first." >&2
  exit 1
fi
SHA="$(git rev-parse --short HEAD)"
COMMIT="$(git rev-parse HEAD)"

REPO="${TUIOS_RELEASE_REPO:-}"
if [ -z "$REPO" ]; then
  REPO="$(git remote get-url origin | sed -E 's#^(git@[^:]+:|https://[^/]+/)##; s#\.git$##')"
fi
TAG="${TUIOS_RELEASE_TAG:-$(git rev-parse --abbrev-ref HEAD | tr '/' '-')}"
case "$TAG" in
  HEAD) echo "error: detached HEAD; name the tag with TUIOS_RELEASE_TAG." >&2; exit 1 ;;
  v[0-9]*) echo "error: $TAG looks like a release tag; pick another with TUIOS_RELEASE_TAG." >&2; exit 1 ;;
esac

# The same version install-tuios-local.sh stamps; see the reasoning there.
base="$(git tag --merged HEAD --list 'v[0-9]*' --sort=-v:refname | grep -v -- - | head -n 1 || true)"
VERSION="dev+${base:+${base#v}.}${SHA}"
ldflags="-s -w -X main.builtBy=publish-tuios-build.sh -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

OUT="${TMPDIR:-/tmp}/tuios-publish-$SHA"
rm -rf "$OUT"
mkdir -p "$OUT"
echo "==> $VERSION -> $REPO $TAG"

# ── 1. build and package each target ───────────────────────────────────────
assets=()
for target in $TARGETS; do
  goos="${target%/*}" goarch="${target#*/}"
  stage="$OUT/stage-$goos-$goarch"
  mkdir -p "$stage"
  echo "==> building $goos/$goarch"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$ldflags" -o "$stage/tuios" ./cmd/tuios
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$ldflags" -o "$stage/tuios-web" ./cmd/tuios-web
  archive="tuios_${goos}_${goarch}.tar.gz"
  tar -C "$stage" -czf "$OUT/$archive" tuios tuios-web
  rm -rf "$stage"
  assets+=("$OUT/$archive")
done
(cd "$OUT" && shasum -a 256 tuios_*.tar.gz > SHA256SUMS)
assets+=("$OUT/SHA256SUMS")
echo "==> packaged in $OUT:"
(cd "$OUT" && ls -l tuios_*.tar.gz SHA256SUMS)

if [ "$DRY_RUN" = 1 ]; then
  echo "==> dry run: nothing published"
  exit 0
fi

# ── 2. publish ───────────────────────────────────────────────────────────────
if ! gh api "repos/$REPO/commits/$COMMIT" --silent 2>/dev/null; then
  echo "error: $SHA is not on $REPO; push its branch to the fork first:" >&2
  echo "    git push origin $(git rev-parse --abbrev-ref HEAD)" >&2
  exit 1
fi
if gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
  echo "error: $REPO already has a release $TAG; delete it or set TUIOS_RELEASE_TAG." >&2
  exit 1
fi
gh release create "$TAG" "${assets[@]}" --repo "$REPO" --target "$COMMIT" --prerelease \
  --title "tuios build $TAG" \
  --notes "Local build $VERSION of $COMMIT, pure-Go backend. Install with scripts/install-tuios-release.sh."
echo "==> published $REPO $TAG"

# ── 3. prune ─────────────────────────────────────────────────────────────────
# Only prereleases this script made, newest first; the rest are never touched.
old="$(gh release list --repo "$REPO" --limit 200 --json tagName,name,isPrerelease,createdAt \
  --jq "[.[] | select(.isPrerelease and (.name | startswith(\"tuios build \")))] | sort_by(.createdAt) | reverse | .[$KEEP:] | .[].tagName")"
for tag in $old; do
  echo "==> pruning $tag"
  gh release delete "$tag" --repo "$REPO" --cleanup-tag --yes
done
