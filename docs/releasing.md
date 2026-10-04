# Release tooling maintenance

CI and release publishing install GoReleaser with `scripts/install-goreleaser`.
The installer downloads only the version in `.goreleaser-version`, requires a
matching, repository-reviewed SHA-256 digest in `.goreleaser-checksums`, and
verifies the archive before extracting or executing anything from it. Release
write tokens and signing/notarization secrets are not requested until this
verification succeeds.

The reviewed checksums cover Linux and macOS hosts on x86_64 and arm64. The
host architecture does not limit the Linux, macOS, and Windows release targets
configured in `.goreleaser.yml`.

The Homebrew cask's post-install hook runs the staged binary's existing
`@completion --install-picker --automatic` action during install and upgrade.
It restores Homebrew's saved shell configuration locations and sudo marker only
for that child, uses no sudo, and tolerates setup failure or its ten-second
timeout. User interruption still propagates. There is no uninstall
hook, so upgrades do not remove setup or record an opt-out. The shared setup
routine owns profile changes and consent; see [shell setup](image-picker-shell-setup.md)
for eligibility, activation and removal. This hook runs on the user's machine,
not while GoReleaser generates the cask or publishes release artifacts.

Linux packages install one fish startup file at
`/usr/share/fish/vendor_conf.d/openai-picker.fish`. It activates at the first
prompt after user configuration, respects opt-out and custom bindings, and only
runs when `/usr/bin/openai` is the selected executable, including after PATH
changes. Ordinary completion remains under the user's existing shell setup.
The package manager owns and removes the file; installation does not edit user
profiles. Bash and zsh retain their existing first-run setup. See
[shell shortcuts](image-picker-shell-setup.md).

`go run ./scripts/render-linux-picker` generates the fish asset at build time.
Release inputs include it only when the checked-out tag's package configuration
references it, preserving older tags' original inputs. The configured
`termux.deb` format still requires an Android build.

Shell completions and man pages are generated in a separate, read-only job before
the protected publishing job receives repository-write tokens or signing and
notarization credentials. GoReleaser packages those prebuilt files without
running the application. For local release or snapshot builds, first run
`./scripts/generate-release-artifacts` without publishing or signing credentials.
Publishing always skips GoReleaser's `before` hooks, including when an older
release tag still contains the previous application-executing hooks.
Rooted, exact-file ignore rules keep only the expected local completions and man
page out of Git status; the publishing checkout applies the same exact-file
excludes in its local Git metadata so non-snapshot releases also pass
GoReleaser's clean-tree validation when historical tags predate those rules.

The publish workflow installs GoReleaser from its trusted workflow checkout
before switching to the requested release tag. This allows existing release tags
that predate the installer to be republished; the workflow checkout, not the
historical tag, determines the reviewed GoReleaser version and artifact generator.

## Update GoReleaser

1. Choose and review the upstream release, then set its version without the `v`
   prefix in `.goreleaser-version`.
2. Download its checksum manifest and Sigstore bundle:

   ```sh
   version="$(cat .goreleaser-version)"
   gh release download "v$version" \
     --repo goreleaser/goreleaser \
     --pattern checksums.txt \
     --pattern checksums.txt.sigstore.json
   ```

3. Verify that the manifest was signed by GoReleaser's upstream release workflow
   for the exact tag, using an independently trusted `cosign` installation:

   ```sh
   cosign verify-blob \
     --certificate-identity "https://github.com/goreleaser/goreleaser/.github/workflows/release.yml@refs/tags/v$version" \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     --bundle checksums.txt.sigstore.json \
     checksums.txt
   ```

4. Replace the reviewed manifest with the four supported host-archive digests:

   ```sh
   awk -v version="$version" \
     '$2 ~ /^goreleaser_(Darwin|Linux)_(arm64|x86_64)\.tar\.gz$/ {
       print $1 "  v" version "/" $2
     }' checksums.txt > .goreleaser-checksums
   test "$(wc -l < .goreleaser-checksums | tr -d ' ')" -eq 4
   ```

5. Run `./scripts/test-goreleaser-installer` and review the version and all four
   digest changes together. The installer intentionally fails before downloading
   if a version or host platform does not have exactly one reviewed digest.
