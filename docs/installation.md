# Installation and upgrades

## Installing with Homebrew

For automatic updates in zsh, paste the [macOS installation block](../README.md#installation).
If you prefer to manage updates through Homebrew, install the cask directly.

```sh
brew install --cask openai/tools/openai && brew list --cask --versions openai
```

To upgrade, paste this command. It refreshes the tap and prints the installed cask version.

```sh
brew update && brew upgrade --cask openai/tools/openai && brew list --cask --versions openai
```

Homebrew shows download progress. One cask contains all CLI commands.

**Homebrew says the cask is current, but GitHub has a newer release.**
Homebrew can publish after GitHub. Reinstalling the same cask gives you the same version.
Wait for Homebrew or choose a [release archive](#installing-a-prebuilt-binary) that already has downloads.

**`openai --version` shows an older version than Homebrew printed.**
Your shell can select another executable, alias, or function. Use the repair below.

<details>
<summary>Check the installed cask and select it in Bash or zsh</summary>

Paste this block into your current shell. It checks Homebrew’s receipt and runs that exact executable.

```sh
cask_dir=$(brew --caskroom openai/tools/openai) &&
receipt="$cask_dir/.metadata/INSTALL_RECEIPT.json" &&
[ "$(/usr/bin/plutil -extract source.tap raw "$receipt")" = openai/tools ] &&
version=$(/usr/bin/plutil -extract source.version raw "$receipt") &&
cask_cli="$cask_dir/$version/openai" &&
"$cask_cli" --version && {
  brew_prefix=$(brew --prefix) &&
  if [ "$brew_prefix/bin/openai" -ef "$cask_cli" ]; then export PATH="$brew_prefix/bin:$PATH"; fi &&
  if ! [ "$(command -v openai)" -ef "$cask_cli" ]; then
    printf 'The cask is installed. Your shell selects another command. Run %q directly.\n' "$cask_cli" >&2
    false
  fi
}
```

Run `type -a openai` to see competing commands. If a new shell selects another copy, put Homebrew’s binary directory first in your profile.

</details>

## Linux

Download one package for your distribution into an empty directory. [Release downloads](https://github.com/openai/openai-cli/releases)

Use the newest stable release with package files under **Assets**.
Choose `amd64` for x86-64 or `arm64` for 64-bit ARM.
From the download directory, paste your distribution’s commands into an interactive Bash or zsh shell.

**Debian or Ubuntu**

```sh
sudo apt install ./openai_*_linux_*.deb &&
source /usr/share/openai-cli/enable-updates.sh
```

**Fedora**

```sh
sudo dnf install ./openai_*_linux_*.rpm &&
source /usr/share/openai-cli/enable-updates.sh
```

The first command installs the system copy at `/usr/bin/openai`.
The second enables automatic updates for your account and selects `$HOME/.openai/bin/openai` in current and future shells.
Setup uses a compatible GitHub CLI or downloads a verified private copy.
To keep package-manager-only updates, run just the first command.
For other distributions or an installation without administrator access, use the [Linux tarball](#installing-a-prebuilt-binary).

## Windows

Open **Assets** for the newest stable release with Windows ZIP files. [Release downloads](https://github.com/openai/openai-cli/releases)

Choose `windows_amd64.zip` for most 64-bit PCs or `windows_arm64.zip` for an ARM64 PC.
Extract the ZIP. In the extracted directory, run this PowerShell command.

```powershell
.\openai.exe --help
```

Use `.\openai.exe` in place of `openai` in the examples.
To use `openai` from any directory, add the extracted directory to your user `Path`.
Open a new terminal and run `where.exe openai` to check which copy it selects.

## Installing a prebuilt binary

Open **Assets** for the newest stable release with downloads for your computer. [GitHub Releases](https://github.com/openai/openai-cli/releases)

Choose the macOS ZIP, Linux tarball, or Windows ZIP. GitHub’s source code archives require a separate build.
On a Mac, the filename ends in `macos_arm64.zip` for Apple silicon or `macos_amd64.zip` for Intel.
Extract the archive into a dedicated directory.
On macOS and Linux, you can keep that directory as your manual installation.
From inside it, paste this block into Bash or zsh.

```sh
archive_bin=$(pwd -P) &&
"$archive_bin/openai" --version &&
export PATH="$archive_bin:$PATH" &&
command -v openai
```

The last command shows which executable, alias, or function your shell selects. If an alias or function wins, use the verified executable directly.
On Windows, run `.\openai.exe --version` in the extracted directory. Add that directory to your user `Path` and check `where.exe openai`.

**Want automatic updates?** Follow the [archive setup for your shell](automatic-updates.md#from-an-extracted-archive).
On a Mac with Homebrew and zsh, the [recommended installation](../README.md#installation) handles setup in one paste.

## Installing with Go

With [Go 1.26 or later](https://go.dev/doc/install), paste this block into Bash or zsh.
It installs the latest CLI and puts Go’s binary directory ahead of older copies.

```sh
go_bin=$(go env GOBIN) && go_path=$(go env GOPATH) &&
go_bin=${go_bin:-"${go_path%%:*}/bin"} &&
if [ -L "$go_bin/openai" ] || [ "${go_bin%/}" = "$HOME/.openai/bin" ] || [ "$go_bin" -ef "$HOME/.openai/bin" ]; then
  printf '%s\n' 'Go would write into another installation. Choose a separate Go directory, then paste this block again:' \
    '  export GOBIN="$HOME/.local/openai-go/bin"' >&2
  false
else
  printf '%s\n' 'Installing the latest OpenAI CLI with Go...'
  go_os=$(go env GOHOSTOS) && go_arch=$(go env GOHOSTARCH) &&
  GOOS="$go_os" GOARCH="$go_arch" GOBIN="$go_bin" go install github.com/openai/openai-cli/cmd/openai@latest &&
  export PATH="$go_bin:$PATH" &&
  "$go_bin/openai" --version && command -v openai
fi
```

This command targets the computer running Go, including when you configured Go for cross-compilation.
Go can finish silently. Add `-v` after `install` to show each package as it compiles.
If the last command shows an alias or function, use the verified Go executable directly.
For new shells, add the same PATH setup to your shell profile.

On Windows, run `go install github.com/openai/openai-cli/cmd/openai@latest`.
Add the Go binary directory to your user `Path`. Use `where.exe openai` to find installed copies.
