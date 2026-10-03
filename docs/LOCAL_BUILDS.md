# Local builds: getting a build from this checkout running somewhere

This fork carries three scripts for turning a checkout into a running
`tuios` binary. They overlap on purpose — each covers a different starting
point (a shell on the target machine, a Homebrew install to overwrite, a
remote box with no Go toolchain) — so pick the one that matches where you're
installing to, not the one you used last time.

| Starting point | Script |
| --- | --- |
| A shell + Go toolchain on the target machine itself | `scripts/install.sh` |
| An existing `brew install tuios` on this Mac you want to overwrite | `scripts/install-tuios-local.sh` |
| A remote Linux box you only have SSH (Secure Shell) access to, no Go toolchain | `scripts/install-tuios-remote.sh` |

These three are personal-fork tooling for the maintainer's own workflow, not
part of the upstream project's documented install paths (see the
[Installation](../README.md#installation) section of the README for those).

## Native build on the target machine (`scripts/install.sh`)

The simplest path: you have a shell and a Go toolchain (and, for the
ghostty backend, Zig) directly on the machine you want `tuios` running on —
for example a fresh `git clone` on a Linux box. It builds `tuios` and
`tuios-web` together and installs both onto PATH.

```bash
./scripts/install.sh            # pure Go emulator (default), needs only go
./scripts/install.sh ghostty    # libghostty-vt emulator, needs zig
./scripts/install.sh --prefix ~/bin
./scripts/install.sh --kill-server   # stop a running daemon after installing
./scripts/install.sh --keep-server   # leave a running daemon alone
```

`TUIOS_PREFIX` sets the default install directory in place of `--prefix`
(default: `~/.local/bin`). Reach for one of the other two scripts instead
when there's no toolchain on the target, or when the existing binary is
owned by a package manager (Homebrew) that a plain install would shadow
rather than replace.

## macOS Homebrew Cellar swap (`scripts/install-tuios-local.sh`)

For overwriting an existing `brew install tuios` (or `tuios-ghostty`) with a
build from this checkout, without fighting PATH order between a Homebrew
copy and a second `~/.local/bin` copy. Builds from the checkout, auto-detects
the backend (pure vs ghostty) from whichever binary is currently on PATH, and
swaps it in place of the resolved Homebrew Cellar target.

```bash
scripts/install-tuios-local.sh                        # build + swap over the tuios on PATH
TUIOS_INSTALL_TARGET=~/.local/bin/tuios \
    scripts/install-tuios-local.sh                     # force a specific install path
TUIOS_BACKEND=ghostty scripts/install-tuios-local.sh   # build the libghostty-vt backend
TUIOS_SKIP_BUILD=1 scripts/install-tuios-local.sh      # swap an already-built target
```

- `TUIOS_INSTALL_TARGET` — install path to swap, in place of resolving the
  `tuios` already on PATH.
- `TUIOS_BACKEND` — `pure` or `ghostty`; overrides auto-detection from the
  existing binary.
- `TUIOS_SKIP_BUILD=1` — reuse an already-built binary instead of building.

## Cross-compile and push over SSH (`scripts/install-tuios-remote.sh`)

For a remote Linux box you want a fresh build on without installing a Go
toolchain there. Cross-compiles the pure Go backend for Linux from this
(macOS) checkout — `CGO_ENABLED=0`, the same as every `linux/*` build in
`.goreleaser.yml`, so nothing beyond `go` itself is needed on the build
machine — then ships the binary over SSH and swaps it in on the remote host.

```bash
TUIOS_REMOTE_HOST=mybox scripts/install-tuios-remote.sh
TUIOS_REMOTE_HOST=user@host TUIOS_REMOTE_ARCH=arm64 \
    scripts/install-tuios-remote.sh
```

- `TUIOS_REMOTE_HOST` — required: an `ssh(1)` target (a `~/.ssh/config`
  alias, or `user@hostname`). Nothing runs without it.
- `TUIOS_REMOTE_ARCH` — `amd64` or `arm64` (default: `amd64`; auto-detected
  via `ssh $TUIOS_REMOTE_HOST uname -m` when the host is reachable).
- `TUIOS_REMOTE_PATH` — install path on the remote (default:
  `~/.local/bin/tuios`).
- `TUIOS_SKIP_BUILD=1` — reuse an already cross-compiled binary at
  `.install-tuios-remote.build` instead of building.

Only the pure Go backend is wired up here: the ghostty backend's
libghostty-vt would need its own Linux cross-build via
`scripts/ghostty-lib.sh <target>` first, and nothing requires that yet.

## The two safety choices behind both swap scripts

`install-tuios-local.sh` and `install-tuios-remote.sh` share the same two
choices in their backup-and-swap step. Quoting directly from their header
comments, so this doesn't drift from what the scripts actually do:

**Fresh-inode swap (`rm` + `cp`, never an in-place overwrite):**

> Overwriting the running binary in place poisons the kernel's vnode cache
> and can crash a live process mapping it. Removing then copying gives the
> new file a new inode.

The remote script applies the identical reasoning on the far end of the SSH
connection:

> The same ETXTBSY (Executable Text Busy) / vnode-cache reasoning as the
> local script applies to a remote daemon just as much as a local one, so
> the remote side gets rm + cp too, never an in-place overwrite or a naive
> scp onto the live path.

**Adhoc codesign, macOS only (`install-tuios-local.sh` only):**

> macOS AMFI (Apple Mobile File Integrity) refuses to exec an
> unsigned/altered Mach-O; `codesign --sign -` applies a valid adhoc
> signature. Needed here because the Homebrew bottle ships signed, and a raw
> local rebuild is not.

`install-tuios-remote.sh` skips this step entirely, for the same reason in
reverse:

> That is a macOS-only AMFI requirement. Linux has nothing equivalent to
> satisfy.

Both scripts also write a timestamped backup of the file they're replacing
(`$TARGET.pre-$SHA`) before the swap, so a bad build can be rolled back by
hand.
