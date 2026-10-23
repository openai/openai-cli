# Automatic updates

Supported self-managed installations prepare release updates automatically. Other installations retain the existing update notice.

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
Keep release publishing, protected environments, signing, CODEOWNER review, and custom-code gates under their existing controls.
