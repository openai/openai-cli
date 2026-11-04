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

On Debian, Ubuntu, or Fedora, paste this block into Bash or zsh.
It downloads the latest package for your system and verifies its SHA-256 before requesting your administrator password.

```sh
( installer=$(curl -qfsSL --proto '=https' --proto-redir '=https' https://github.com/openai/openai-cli/releases/latest/download/install-linux-release.sh) &&
  /bin/bash -p -c "$installer" ) &&
source /usr/share/openai-cli/enable-updates.sh
```

The package manager owns `/usr/bin/openai`. The helper selects your automatically updated copy in this shell and future shells.
If the latest release has no installer asset yet, choose your package from [Release downloads](https://github.com/openai/openai-cli/releases).

For an offline installation, verify your chosen package against its official release checksum first.
Give apt or dnf that exact path, such as `sudo apt install ./chosen-file.deb` or `sudo dnf install ./chosen-file.rpm`.
Older packages, including v1.48.1, support `/usr/bin/openai --help` and `/usr/bin/openai --version`.
To enable automatic updates with a supported package, run `source /usr/share/openai-cli/enable-updates.sh`.
Other systems can use the [Linux tarball](#installing-a-prebuilt-binary).

## Windows

Download the Windows ZIP from **Assets** in the latest stable release with downloads. [Release downloads](https://github.com/openai/openai-cli/releases)
Choose `windows_amd64.zip` for a 64-bit Intel or AMD PC, `windows_arm64.zip` for ARM64, or `windows_386.zip` for 32-bit Windows.

In File Explorer, select **Extract All**. Open the extracted folder.
If it contains **Install.cmd**, double-click **Install.cmd**.
Setup enables automatic updates for your account. If the system PATH selects another copy, the installer keeps its recovery instruction visible.

For an older ZIP without **Install.cmd**, open PowerShell in the extracted folder and run `.\openai.exe --help`.

<details>
<summary>Use an existing PowerShell window</summary>

For a ZIP with **Install.cmd**, this block also selects the managed CLI in your existing PowerShell.

```powershell
& {
    Write-Host 'Installing OpenAI CLI for your account...'
    $installed = & .\openai.exe install --user
    if ($LASTEXITCODE -ne 0 -or !$installed) { throw 'OpenAI CLI setup could not finish. See the installer message above.' }
    $bin = Join-Path $env:LOCALAPPDATA 'OpenAI\CLI\bin'
    if (($env:Path -split ';', 2)[0] -ine $bin) { $env:Path = "$bin;$env:Path" }
    $selected = Microsoft.PowerShell.Core\Get-Command openai
    if ($selected.CommandType -ne 'Application' -or $selected.Path -ine (Join-Path $bin 'openai.exe')) {
        throw 'A shell command overrides openai. Use & (Join-Path $env:LOCALAPPDATA ''OpenAI\CLI\bin\openai.exe'').'
    }
    openai --version
}
```

Setup installs every CLI command for your Windows account. It configures the update verifier and registers the CLI for new terminals.
Eligible interactive commands install future authenticated releases. [Automatic updates](automatic-updates.md#eligible-invocations)

If you prefer a portable copy, use `.\openai.exe` directly from the extracted directory.

</details>

<details>
<summary>Download the matching ZIP automatically</summary>

The setup recipe requires a published Windows ZIP that contains **Install.cmd**.
Open the [PowerShell setup recipe](../scripts/install-windows-release.ps1). Select **Copy raw file**.
Paste it into an ordinary PowerShell window. Setup downloads the correct Windows ZIP and checks its publisher before installation.
It selects the managed CLI in this PowerShell and adds the CLI to your user PATH.
If a machine-wide installation takes precedence, setup selects your copy now and shows how to select it in new terminals.
If your organization restricts scripts, use its approved installation method.

</details>

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
On Windows, use `.\openai.exe --version` for a portable copy. For automatic installation and updates, use the [Windows setup](#windows).

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
