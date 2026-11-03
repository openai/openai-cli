# Automatic updates

Supported self-managed installations prepare release updates automatically. Other installations retain the existing update notice.

## Install from a release archive

On macOS or Linux, the official release executable can update itself. GitHub CLI (`gh`) verifies each future download.

### Set up automatic updates with Homebrew on macOS

The [macOS installation block](../README.md#installation) installs the CLI and enables automatic updates in one paste.
If you already have a Homebrew cask containing the helper, you can run the helper directly in zsh.

```zsh
source "$(brew --prefix)/bin/openai-enable-updates.zsh"
```

The helper checks for shell conflicts and selects the verified copy in this terminal.
It uses the GitHub CLI on your PATH when it supports update verification. Otherwise, it asks Homebrew to install or upgrade GitHub CLI.
It also adds the selection to your zsh startup file. Homebrew continues to own its separate copy.
For other shells or a linked zsh startup file, use the [archive instructions](#from-an-extracted-archive).

If the helper is missing, refresh the tap and upgrade the cask explicitly.

```sh
brew update && brew upgrade --cask openai/tools/openai
```

Retry the setup command. If the tap still provides an older cask, choose a release containing the helper below.
You can keep using the Homebrew CLI while waiting for the tap.

### From an extracted archive

1. Install a compatible [GitHub CLI](https://cli.github.com/).
2. Download your platform archive from the newest [stable release with downloads](https://github.com/openai/openai-cli/releases).
3. Extract the archive.

In zsh, paste this block from the extracted directory. It selects the CLI in current and future terminals.

```zsh
sh ./install.sh --require-gh --zsh "${ZDOTDIR:-$HOME}" &&
export PATH="$HOME/.openai/bin:$PATH"
```

For Bash or a linked zsh startup file, paste this command instead. It selects the CLI in this terminal.

```sh
sh ./install.sh --require-gh && export PATH="$HOME/.openai/bin:$PATH"
```

The installer reports the installed version and where it put the executable.
See which [interactive commands check for updates](#eligible-invocations).
An alias or function can take precedence. Use `"$HOME/.openai/bin/openai"` directly in that case.

### Keep this selection in a new terminal

If your new terminal already selects `$HOME/.openai/bin/openai`, setup is complete.
If installation succeeded but shell selection failed, keep the installed executable.
Run `export PATH="$HOME/.openai/bin:$PATH"` to select it in this shell. Use its full path if an alias still wins.
For zsh, put this line in your interactive startup file (`${ZDOTDIR:-$HOME}/.zshrc`).

```sh
export PATH="$HOME/.openai/bin:$PATH"
```

For Bash, add the same export to your interactive startup file. Only the explicit `--zsh` option edits a profile.

### Older installations and interrupted setup

An older archive without the helper can run directly as `./openai`. Homebrew can still manage its own installed cask.
Released versions 1.46.0 and 1.47.0 can fail to verify future updates with GitHub CLI.
To enroll, download a release containing the corrected updater and `install.sh`.

The helper preserves any existing `$HOME/.openai/bin/openai`.
Repeated Homebrew setup can select an identical, safely owned copy. A different copy needs your decision.
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
If GitHub CLI lacks support, bare interactive `openai` can show a manual update notice.
Capability checks retry at most every four hours, so upgrading GitHub CLI can restore automatic updates.

Homebrew, Linux packages, Go installations, unknown locations, and development builds remain outside automatic installation.
A symlink from `$HOME/.openai/bin/openai` to a Go installation remains owned by Go.
Windows retains update notices. This implementation does not replace running Windows executables.

Existing v1.40.4 binaries lack the new release metadata. They require a manual upgrade before using this feature.
Do not copy a development build into your normal installation to enable automatic updates.

## Eligible invocations

An enrolled installation checks after bare `openai` or a successful API command that prints one default text response.
For example, an ordinary `openai responses create` can start a check. The command prints only its normal output.

Stdin, stdout, and stderr must all be terminals. CI environments and `TERM=dumb` disable checks.
Explicit help, completion, quiet mode, selected output formats, extracted fields, and file saves skip checks.
Streams, lists, local commands, and interactive screens also skip checks. Color overrides cannot enable updates in scripts.

The first eligible invocation starts a silent, detached helper after the command finishes.
The helper discovers the latest stable release, at most once every four hours, including failed attempts.
Published, non-prerelease releases qualify immediately after their publishing workflow succeeds and artifact provenance becomes available.
Four hours limits discovery frequency. It does not impose a release-age wait or a staged rollout.

The helper downloads and authenticates the exact selected archive. It then prepares the replacement without changing the installed executable.
A later eligible invocation starts activation after the command finishes. That invocation still executes its original binary.
Activation checks the selected release again for withdrawal or replacement. This exact-release validation does not discover another release.
Failed activation checks retry at most every four hours. The helper exits within approximately two minutes.

The next bare `openai` invocation that runs the activated binary prints a confirmation below help.

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
