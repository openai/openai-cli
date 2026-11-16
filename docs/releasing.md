# Release tooling maintenance

GoReleaser embeds the exact release version and commit for [automatic updates](automatic-updates.md).
Ordinary builds and Go installations do not receive those metadata values.
Automatic activation requires the published archive's existing provenance and a successful publishing workflow run attempt.
Missing or incomplete attestations keep the installed binary unchanged. No additional signing key or release channel is introduced.

CI and release publishing install GoReleaser with `scripts/install-goreleaser`.
The installer downloads only the version in `.goreleaser-version`, requires a
matching, repository-reviewed SHA-256 digest in `.goreleaser-checksums`, and
verifies the archive before extracting or executing anything from it. Release
write tokens and signing/notarization secrets are not requested until this
verification succeeds.

The reviewed checksums cover Linux and macOS hosts on x86_64 and arm64. The
host architecture does not limit the Linux, macOS, and Windows release targets
configured in `.goreleaser.yml`.

The Homebrew cask installs the binary, ordinary completions, and manual without
running picker setup hooks. Homebrew users receive the existing first-run setup;
see [shell setup](image-picker-shell-setup.md) for eligibility and explicit setup.

Linux packages install separate image picker and Explore scripts in `/usr/share/fish/vendor_conf.d/`.
Fish loads them at the first interactive prompt after user configuration.
Separate filenames let personal image picker settings coexist with Explore.
Both check the selected executable and respect per-user opt-outs.
The package manager owns these files and the Explore selection function in `/usr/share/openai-cli/`.
See [image picker shortcuts](image-picker-shell-setup.md) and [command insertion](command-insertion.md).

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

## Historical artifact repairs

New releases must pass model verification before publication. Existing releases through v1.51.5 can repair their original artifacts.
The workflow verifies the release tag, version, commit, and publication date against GitHub's release record.
It also verifies that the commit belongs to the history of v1.51.5.
Version 1.0.0 recorded `main` as its release target. The check uses its original release ID and commit.
These repairs retain the existing signing, provenance, and trusted toolchain checks.

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
