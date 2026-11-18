# Installation and upgrades

## Installing a prebuilt binary

Open **Assets** on the [release you want to install](https://github.com/openai/openai-cli/releases).
Choose the archive for your operating system and processor. GitHub also offers source archives for building the CLI yourself.
Each release includes a checksums file. Compare your download’s SHA-256 against its entry before extracting it.

On macOS or Linux, extract the archive and put `openai` in a directory on your `PATH`.
Use `$HOME/.local/bin` for a manual installation. Add that directory to your shell’s `PATH` if needed.
On Windows, extract `openai.exe` and follow the [Windows instructions](#windows).
Use `openai --version` to confirm your installation. To upgrade manually, repeat these steps with the release you choose.

## Installing with Homebrew

```sh
brew install openai/tools/openai
```

To upgrade the Homebrew copy, run `brew upgrade --cask openai/tools/openai`.
Homebrew can publish after GitHub. Use `brew list --cask --versions openai` to check the installed cask.
If your terminal reports a different version, run the Homebrew executable directly.

```zsh
"$(brew --prefix)/bin/openai" --version
```

Run `type -a openai` to identify other executables, aliases, or functions. To select Homebrew, put its bin directory first on your PATH.

## Linux

Choose the release archive for your processor, or use a package for your distribution:

| Distribution | Package | Install the downloaded file |
| --- | --- | --- |
| Debian or Ubuntu | `.deb` | `sudo apt install ./PACKAGE.deb` |
| Fedora or RHEL | `.rpm` | `sudo dnf install ./PACKAGE.rpm` |
| Alpine | `.tar.gz` | Extract the verified tarball. Use the release `.apk` only if your system trusts its signature. |
| Arch Linux | `.pkg.tar.zst` | `sudo pacman -U ./PACKAGE.pkg.tar.zst` |

Replace the sample filename in each command with your downloaded filename.
If your distribution cannot verify the package, use the verified tarball.
Use the same package manager to upgrade. Debian and RPM packages install the CLI at `/usr/bin/openai`.

### Install over SSH

The same binary and package instructions work in an SSH session. You can download the release locally and transfer it.
Verify the checksum on the destination machine before installation.

## Windows

Choose the ZIP ending in `_windows_amd64.zip` for 64-bit Intel or AMD.
Choose `_windows_arm64.zip` for Arm64 or `_windows_386.zip` for 32-bit Windows.
Select **Extract All** in File Explorer. In the extracted folder, verify the CLI with PowerShell:

```powershell
.\openai.exe --version
```

Put the executable in `%LOCALAPPDATA%\Programs\OpenAI CLI`. Add that folder to your user `Path`.
Open a new PowerShell window.
Run `Get-Command openai -All | Select-Object CommandType, Name, Definition` to see the selected command and any alternatives.
Close running CLI processes before replacing the executable with a newer version.

## Installing with Go

Install [Go 1.26 or later](https://go.dev/doc/install) and run:

```sh
go install github.com/openai/openai-cli/cmd/openai@latest
```

Run `go env GOBIN GOPATH` to find the destination. Go uses `GOBIN` when set, otherwise the first `GOPATH` entry’s `bin` directory.
Add that directory to your PATH. Choose a Go directory separate from your other CLI installations.
Repeat the command to upgrade, or replace `latest` with a specific release tag.

<details>
<summary>Install for this computer with custom Go or PATH settings</summary>

In Bash or zsh, this block uses your host OS and architecture and checks the destination before installing.
It places the selected Go directory first on your PATH for this shell.

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

If another command still wins, run the executable by its full path. For future shells, add your selected directory to PATH.

</details>
