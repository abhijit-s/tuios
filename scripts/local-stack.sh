#!/usr/bin/env bash
# Rebuilds the fork's local integration stack: upstream/main plus our open pull
# requests, local-only work, and a few upstream pull requests not yet merged,
# applied in the order of LAYERS below. It exists so the stack can be rebuilt
# when a pull request moves, rather than redone by hand.
#
# This script is fork-only and never goes upstream.
#
# Usage:
#   scripts/local-stack.sh WORKTREE [BRANCH]       build BRANCH from upstream/main
#   scripts/local-stack.sh --from N WORKTREE [BRANCH]
#                                                  go on from layer N (1-based) on
#                                                  BRANCH as it stands, after a
#                                                  conflict was resolved and committed
#
# WORKTREE is created with `git worktree add` when it does not exist. BRANCH
# defaults to integ/<today>. Nothing is pushed.
#
# git rerere is turned on, so a conflict resolved once is resolved the same way
# on the next run. A conflict rerere has no resolution for stops the run: resolve
# it hunk by hunk, commit (or `git cherry-pick --continue`), then rerun with
# --from on the next layer. After each layer `go build ./...` must pass
# (NO_BUILD=1 skips it).
set -euo pipefail

# The run checks branches out, which can rewrite this very file when it is run
# from the worktree it rebuilds, so it runs from a copy.
if [[ -z ${LOCAL_STACK_SELF:-} ]]; then
	copy=$(mktemp -t local-stack.XXXXXX)
	cp "$0" "$copy"
	LOCAL_STACK_SELF=$0 exec bash "$copy" "$@"
fi
self=$LOCAL_STACK_SELF

# Upstream pull requests fetched as upstream/pr/N, for a layer that merges one.
PULLS=()

# The fork-only layers that are commits of ours rather than branches are kept
# alive by local branches: stack/spaces-760-merge-fix, stack/spaces-glue,
# stack/spaces-settings-row, stack/spaces-760-glue, stack/spaces-presets-glue, stack/space-rename-dialog,
# stack/space-session-menus, stack/space-remove-close, stack/space-numbers, stack/space-new, stack/ws-handoff-glue,
# stack/handoff-vt-glue and stack/local-stack (this script's own commit). Move one with
# `git branch -f` when its commit is redone.

# One layer per line: "merge REF" merges a whole branch, "pick REF..." cherry-
# picks the listed commits in order, and "gen" rebuilds the skill archive and
# commits it (see regenerate_skills). A kind written "~merge" or "~pick" is not
# built on its own, because the layer after it is the fix that makes it build.
# A trailing "# ..." is a note. Layers marked fork-only are not pull requests
# anywhere.
LAYERS=(
	"~merge pr/spaces                           # fork-only: full spaces, reconciled by rerere with #708 and on top of #760's space headings"
	"pick stack/spaces-760-merge-fix ea51350a stack/spaces-glue stack/spaces-settings-row stack/spaces-760-glue stack/spaces-presets-glue  # fork-only: the accessors #760 and pr/spaces both declare, once, rail helper dedupe, the space tests #708 supersedes, the Spaces settings row found past the per-edge rail rows, the fork's space tests fitted to #760's headings, and the keybind presets' which-key check read with a space"
	"pick 5fe9bc4c e12b4401 9b10884c d642be74  # fork-only, also on backup/main-2026-10-15: install scripts and docs, tiling scheme"
	"pick 3bd3c2ce 18441c89                    # fork-only, stack/space-rename-dialog: the space rename dialog and its negative controls; its skill archive commit is left to gen"
	"pick 49c016f7 8ed74e62                    # fork-only, stack/space-session-menus: Move to space, New session here and New session menu rows, their actions, the rail drag between spaces, and their negative controls; its skill archive commit is left to gen"
	"pick 22953ccc 85d40d52 503b136c           # fork-only, stack/space-remove-close: Remove space and Close space... on space headings, their actions, tuios remove-space, the fallback for an older daemon, and their negative controls; its fast-path fix 38adf89d is upstream's ceb88f6c, and its skill archive commit is left to gen"
	"pick 164d3a5f 024836e8                    # fork-only, stack/space-numbers: switch_space_1..9, numbers on space headings, the heading menu's Switch to space row, and their negative controls"
	"pick 96227f94 30052b03 ebbf8bb2           # fork-only, stack/space-new: the new_space action, New space... rows on the blank rail, session and space heading menus, the dialog's create hint, and their negative controls"
	"merge pr/agent-row-after-exit              # upstream #873: a hooked agent's pane clears once its harness has exited without a SessionEnd"
	"merge pr/live-handoff-10                   # live handoff of the daemon (tuios daemon upgrade): the tip of the upstream series #876 and its parts 2-10"
	"pick 9b1ffec4a                              # fork-only, stack/handoff-vt-glue: the handoff restores report colours through upstream's SetReportColors with the palette; goes into the live-handoff series when it is rebased past upstream 4d443aea"
	"pick abd2ebf3                              # fork-only: the local install offers tuios daemon upgrade, on top of the install script layer; kept alive by stack/install-upgrade-offer"
	"merge fix/workspace-move-renumber          # a moved workspace takes its new number, and herdr tab ids stay stable: a local branch until it is pushed, so origin/ is not used"
	"pick 178117af                              # fork-only, stack/ws-handoff-glue: a display order handed over by an older image becomes the numbering on adoption, needs both merges above"
	"merge fix/agent-report-own-pane            # upstream #874: a hook report lands on a pane only from a harness that runs in it"
	"gen                                     # fork-only: the skill archive, rebuilt from the merged skill files"
	"pick stack/local-stack                     # fork-only: this script, the last commit of the stack"
)

die() {
	printf 'local-stack: %s\n' "$*" >&2
	exit 1
}

from=1
if [[ ${1:-} == --from ]]; then
	from=${2:?--from needs a layer number}
	shift 2
fi
worktree=${1:?usage: local-stack.sh [--from N] WORKTREE [BRANCH]}
branch=${2:-integ/$(date +%F)}

git fetch upstream
git fetch origin
for n in ${PULLS[@]+"${PULLS[@]}"}; do
	git fetch upstream "+pull/$n/head:refs/remotes/upstream/pr/$n"
done
git config rerere.enabled true
git config rerere.autoupdate true

if ((from == 1)); then
	if [[ -d $worktree ]]; then
		[[ -z $(git -C "$worktree" status --porcelain --untracked-files=no) ]] || die "$worktree has uncommitted changes"
		git -C "$worktree" checkout -B "$branch" upstream/main
	else
		git worktree add -B "$branch" "$worktree" upstream/main
	fi
else
	[[ $(git -C "$worktree" branch --show-current) == "$branch" ]] || die "$worktree is not on $branch"
fi
cd "$worktree"

archive=skills/tuios.tar.gz

# take_archive settles a conflict on the skill archive by keeping this side.
# The archive is a binary rerere cannot merge, and the gen layer rebuilds it
# from the merged skill files, so either side is as good as the other.
take_archive() {
	if [[ -n $(git ls-files --unmerged -- "$archive") ]]; then
		git checkout --ours -- "$archive"
		git add -- "$archive"
	fi
}

# finish commits a layer rerere resolved whole, going on through a list of
# picks while rerere keeps resolving them. It fails when a path is still
# unmerged, or when the layer failed with no merge or pick under way.
finish() {
	while take_archive && [[ -z $(git diff --name-only --diff-filter=U) ]]; do
		if [[ -f $(git rev-parse --git-path MERGE_HEAD) ]]; then
			git commit --no-edit
			return
		elif [[ -f $(git rev-parse --git-path CHERRY_PICK_HEAD) ]]; then
			GIT_EDITOR=true git cherry-pick --continue && return
		else
			return 1
		fi
	done
	return 1
}

# regenerate_skills rebuilds the skill archive from the skill files and commits
# it when it changed. The archive is a binary that rerere cannot merge, so a
# conflict on it keeps either side (see take_archive): the merged skill files
# are the source, and this commits what they build.
regenerate_skills() {
	go generate ./skills
	if [[ -n $(git status --porcelain -- skills) ]]; then
		git commit -m "chore(skills): regenerate the skill archive" -m "The layers below change the skill files, and the archive is built from them." -- skills
	fi
	# docs/look-keys.json is generated from the config options, which the space
	# layers extend, and TestLookKeysJSONCurrent fails when it is stale.
	go run ./cmd/tuios look keys --json >docs/look-keys.json
	if [[ -n $(git status --porcelain -- docs/look-keys.json) ]]; then
		git commit -m "docs: regenerate look-keys.json" -m "The layers below add config options, and the file is generated from them." -- docs/look-keys.json
	fi
}

for ((i = from; i <= ${#LAYERS[@]}; i++)); do
	read -ra words <<<"${LAYERS[i - 1]%%#*}"
	kind=${words[0]}
	build=1
	if [[ $kind == "~"* ]]; then
		kind=${kind#"~"}
		build=
	fi
	refs=("${words[@]:1}")
	printf '\n== layer %d: %s\n' "$i" "${LAYERS[i - 1]}"
	if [[ $kind == gen ]]; then
		regenerate_skills
		continue
	fi
	for ref in "${refs[@]}"; do
		git rev-parse --verify --quiet "$ref^{commit}" >/dev/null || die "layer $i: $ref does not exist"
	done
	case $kind in
	merge) ok() { git merge --no-edit "${refs[@]}"; } ;;
	pick) ok() { git cherry-pick "${refs[@]}"; } ;;
	*) die "layer $i: unknown kind $kind" ;;
	esac
	if ! ok && ! finish; then
		git diff --name-only --diff-filter=U >&2
		die "layer $i stopped on a conflict rerere could not resolve (files above). Resolve it hunk by hunk, never a whole file with --ours/--theirs, commit, then run: $self --from $((i + 1)) $worktree $branch"
	fi
	if [[ -n $build && -z ${NO_BUILD:-} ]]; then
		go build ./... || die "layer $i: go build failed. Fix it in a commit, then run: $self --from $((i + 1)) $worktree $branch"
	fi
done

# The skill is read from an archive built from skills/tuios, so a layer that
# touches the skill files after the gen layer leaves it stale.
go generate ./skills
if [[ -n $(git status --porcelain -- skills) ]]; then
	die "skills/tuios.tar.gz is stale after the layers. Move the gen layer after the last layer that touches skills/tuios"
fi

printf '\nlocal-stack: %s is built at %s\n' "$branch" "$(git rev-parse --short HEAD)"
