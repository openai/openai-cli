# Automatic updates

Supported self-managed installations prepare release updates automatically. Other installations retain the existing update notice.

## Install from a release archive

On macOS or Linux, the official release executable can update itself. GitHub CLI (`gh`) verifies each future download.

### Set up automatic updates with Homebrew on macOS

Choose this route if you want the CLI to manage future upgrades.
Homebrew downloads the matching archive and checks its cask checksum. This avoids installing another Homebrew cask.
The block installs `gh` and selects the self-managed executable in this terminal.
In zsh, it also adds that selection to your interactive startup file. Choose the extracted-archive steps to keep setup temporary.

```sh
if sh -eu <<'SETUP'
  brew update
  brew fetch --cask openai/tools/openai
  archive=$(brew --cache --cask openai/tools/openai)
  work=$(mktemp -d)
  trap 'rm -f "$work/install.sh" "$work/openai"; rmdir "$work"' EXIT
  if ! unzip -q "$archive" openai install.sh -d "$work"; then
    printf '%s\n' 'Setup needs a release archive containing openai and install.sh.' >&2
    exit 1
  fi
  brew install gh
  sh "$work/install.sh" --require-gh
SETUP
then
  export PATH="$HOME/.openai/bin:$PATH"
  if [ -n "${ZSH_VERSION:-}" ]; then
    profile="${ZDOTDIR:-$HOME}/.zshrc"
    path_line='export PATH="$HOME/.openai/bin:$PATH"'
    if [ -L "$profile" ] || { [ -e "$profile" ] && [ ! -f "$profile" ]; }; then
      printf '%s\n' 'CLI installed. Add the PATH export to your chosen zsh startup file.' >&2
      false
    else
      grep -Fqx "$path_line" "$profile" 2>/dev/null || printf '\n%s\n' "$path_line" >> "$profile"
    fi
  fi &&
  if [ "$(command -v openai)" = "$HOME/.openai/bin/openai" ]; then
    command -v openai
  else
    printf '%s\n' 'CLI installed. Your shell selects another command. Use "$HOME/.openai/bin/openai" directly.' >&2
    false
  fi
else
  false
fi
```

The installer needs a compatible `gh` before enrolling through this route. Fix the reported prerequisite and retry.
If the tap still supplies an older archive, use a [completed release](https://github.com/openai/openai-cli/releases) that contains the helper.

### From an extracted archive

1. Download and extract your platform archive from [GitHub Releases](https://github.com/openai/openai-cli/releases/latest).
2. In the extracted directory, paste this command into Bash or zsh.

```sh
sh ./install.sh && export PATH="$HOME/.openai/bin:$PATH"
```

The installer reports the installed version and where it put the executable.
If GitHub CLI is missing, install it using the instruction shown. Bare interactive `openai` launches handle future updates.
An alias or function can take precedence. Use `"$HOME/.openai/bin/openai"` directly in that case.

### Keep this selection in a new terminal

If your new terminal already selects `$HOME/.openai/bin/openai`, setup is complete.
If installation succeeded but shell selection failed, keep the installed executable.
Run `export PATH="$HOME/.openai/bin:$PATH"` to select it in this shell. Use its full path if an alias still wins.
For zsh, put this line in your interactive startup file (`${ZDOTDIR:-$HOME}/.zshrc`).

```sh
export PATH="$HOME/.openai/bin:$PATH"
```

For Bash, add the same export to your interactive startup file. The installer itself never edits shell profiles.

### Older installations and interrupted setup

An older archive without the helper can run directly as `./openai`. Homebrew can still manage its own installed cask.
Released versions 1.46.0 and 1.47.0 can fail to verify future updates with GitHub CLI.
To enroll, download a release containing the corrected updater and `install.sh`.

The helper stops if `$HOME/.openai/bin/openai` already exists.
If you previously installed your own executable there, verify it and move it to a new backup path before running setup.
Keep that backup until the new executable works. For a package-owned file, use that package's upgrade method.
A forced stop can leave a private `.openai-install.*` staging directory. Remove that directory only after all installers exit.

## Supported installations

Automatic installation requires all these conditions:

- macOS or Linux, running as an ordinary user.
- An official release built with the automatic-update release metadata.
- The executable is a regular file at `$HOME/.openai/bin/openai`.
- The executable and dedicated directories belong to the current user and reject writes from other users.
- The executable and dedicated directories have no symlinks. The executable has no hard links.
- A compatible GitHub CLI, `gh`, is available through `PATH`.

GitHub CLI verifies existing GitHub artifact provenance. The updater downloads public attestation bundles without GitHub credentials.
It supplies those bundles to `gh attestation verify`. It does not install GitHub CLI or request login.
Verification requires `--bundle`, `--cert-identity`, `--source-ref`, and `--deny-self-hosted-runners` support.
The detached helper checks required options before downloading an update.
If GitHub CLI lacks support, later eligible invocations retain manual update notices.
Capability checks retry at most every four hours, so upgrading GitHub CLI can restore automatic updates.

Homebrew, Linux packages, Go installations, unknown locations, and development builds remain outside automatic installation.
A symlink from `$HOME/.openai/bin/openai` to a Go installation remains owned by Go.
Windows retains update notices. This implementation does not replace running Windows executables.

Existing v1.40.4 binaries lack the new release metadata. They require a manual upgrade before using this feature.
Do not copy a development build into your normal installation to enable automatic updates.

## Eligible invocations

Only successful, bare `openai` invocations qualify. Stdin, stdout, and stderr must all be terminals.
CI environments and `TERM=dumb` do not qualify. Color overrides cannot enable updates in scripts.

Explicit help, flags, commands, completion, redirected streams, pipes, structured output, and quiet invocations perform no updater work.
Interactive command screens also perform no updater work. The updater never changes an ongoing session.

The first eligible invocation starts a silent, detached helper after help finishes.
The helper discovers the latest stable release, at most once every four hours, including failed attempts.
Published, non-prerelease releases qualify immediately after their publishing workflow succeeds and artifact provenance becomes available.
Four hours limits discovery frequency. It does not impose a release-age wait or a staged rollout.

The helper downloads and authenticates the exact selected archive. It then prepares the replacement without changing the installed executable.
A later eligible invocation starts activation after its help finishes. That invocation still executes its original binary.
Activation checks the selected release again for withdrawal or replacement. This exact-release validation does not discover another release.
Failed activation checks retry at most every four hours. The helper exits within approximately two minutes.

The next eligible invocation executing the activated binary shows this plain-text line below successful help:

```text
Updated to vX.Y.Z.
```

The CLI records the confirmation once before writing it to stderr. A failed terminal write does not change command success.
The existing artwork, colors, menus, spacing, and help layout stay unchanged.
There are no prompts, progress indicators, terminal queries, or API requests to OpenAI.

## Controls

Disable automatic installation through your shell environment:

```sh
export OPENAI_CLI_NO_AUTO_UPDATE=1
```

Manual update notices remain enabled.

Disable all update discovery and notices with the existing control:

```sh
export OPENAI_CLI_NO_UPDATE_CHECK=1
```

Pin an exact stable version:

```sh
export OPENAI_CLI_VERSION_PIN=v1.40.4
```

Pins accept three numeric components, with an optional `v`. Invalid pins disable automatic installation.
Pinning the executing version prevents updates. Another pin selects only that exact release, including an explicit downgrade.
Installation still requires that release's updater metadata, matching platform archive, and successful authentication.
Unavailable or unsupported pins leave the working installation unchanged. The updater never substitutes `latest` for a pin.

These controls also block activation of conflicting prepared updates. Set them before invoking `openai`.
Environment changes do not modify an already running helper's inherited environment.
To stop such work immediately, terminate that helper before changing or removing its files.

## Integrity, recovery, and withdrawal

The helper uses a permanent, nonblocking process lock under `$HOME/.openai/bin/.update`.
It binds selection to the release ID, asset ID, SHA-256 digest, size, timestamp, version, and tag commit.
GitHub CLI authenticates the archive against this repository's publishing workflow on `main` using GitHub-hosted runners.
The helper also requires the authenticated workflow run attempt to have completed successfully.
The extracted executable must match the selected version, commit, operating system, and architecture.
An adjacent unsigned checksum file cannot authorize installation.

The helper rechecks persisted state, installation identity, and prepared bytes before replacement.
An atomic rename activates the prepared executable. Existing processes continue using their original executable.
Failures leave the installed executable unchanged. Interrupted preparation leaves bounded files for the next successful attempt to remove.
Malformed state, unsafe permissions, changed files, unsupported verification, and rate limits fail silently.
No daemon, service, scheduler, or telemetry system runs between invocations.

Maintainers can withdraw a release through the existing GitHub release mechanism, such as deleting it or marking it as draft.
Activation rejects withdrawn releases, moved tags, and replaced assets. A later discovery can select a corrected release.
An offline activation cannot validate withdrawal and therefore leaves the installed executable unchanged.
Withdrawal does not automatically downgrade installations that already activated the release. Use an exact supported pin or a manual reinstall.

The updater replaces only the executable. Existing completion files and manual pages remain user-managed.
Keep release publishing, protected environments, signing, CODEOWNER review, and generation provenance under their existing controls. Generated-file ownership relies on `AGENTS.md` and ordinary code review.
