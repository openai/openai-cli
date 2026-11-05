<div align="center">
  <h1>OpenAI CLI</h1>

  <p>The official command-line tool for the OpenAI REST API.</p>

  <p>
    <a href="#installation"><strong>Install</strong></a>
    ·
    <a href="docs/cli-reference.md">CLI reference</a>
    ·
    <a href="#guides">Guides</a>
  </p>
</div>

<p align="center">
  <img src="./assets/openai-cli.png" alt="OpenAI CLI terminal showing commands for images, files, batches, and JSON output" width="80%">
</p>

> Looking for the coding agent? Use [Codex CLI](https://developers.openai.com/codex/cli).

<!-- x-release-please-start-version -->

## Installation

Choose your operating system. Each setup installs all CLI commands and turns on automatic updates for normal interactive use.

[macOS](#macos) · [Linux](#linux) · [Windows](#windows) · [When updates run](docs/automatic-updates.md#eligible-invocations)

<a id="installing-with-homebrew"></a>

### macOS

Using Homebrew and zsh, paste both lines together in Terminal. Use the same command for an older Homebrew installation.

```zsh
brew update && brew install --cask openai/tools/openai &&
source "$(brew --prefix)/bin/openai-enable-updates.zsh"
```

[Other shells and Homebrew troubleshooting](docs/installation.md#installing-with-homebrew) · [Install without Homebrew](docs/installation.md#installing-a-prebuilt-binary)

<a id="installing-a-prebuilt-binary"></a>

### Linux

On Debian, Ubuntu, or Fedora, paste the block below into Bash or zsh. It picks the package for your computer.
Enter your password if sudo asks.

<details>
<summary>Show the Linux install command</summary>

```sh
(
  installer=$(mktemp /tmp/openai-install.XXXXXXXX) || exit
  trap 'rm -f -- "$installer"' EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
  printf 'Downloading the OpenAI CLI installer...\n' >&2
  curl -qfsSL --retry 3 --retry-max-time 60 --max-time 60 --proto '=https' --proto-redir '=https' \
    -o "$installer" https://raw.githubusercontent.com/openai/openai-cli/334f332fc6a5f8298b20548d4fbbbb30c940cdc5/scripts/install-linux-release.sh || exit
  printf '%s  %s\n' b0c05af08bd5e168366063bde36147d880f90fba337ce57fff928aa5f3633ce5 "$installer" | sha256sum --check --quiet || exit
  /bin/bash -p "$installer"
) && source /usr/share/openai-cli/enable-updates.sh
```

</details>

[Other Linux distributions](docs/installation.md#installing-a-prebuilt-binary)

### Windows

1. Open the [Windows installer](scripts/install-windows-release.ps1).
2. Select **Copy raw file**.
3. Paste it into PowerShell without administrator privileges.

The installer downloads the matching ZIP and makes `openai` available in this terminal. [Install with File Explorer](docs/installation.md#windows)

<!-- x-release-please-end -->

## Authentication

To check your installation, run the CLI in your terminal. It lists commands without an API key.

```sh
openai
```

To make API requests, follow the instructions this command prints for your shell.

```sh
openai help setup
```

For scripts, supply `OPENAI_API_KEY` through your environment or secret manager. [Create an API key](https://platform.openai.com/settings/organization/api-keys)

## Things to try

### Send a request

```sh
openai responses create --model gpt-5.5 --input "Say this is a test"
```

### Generate an image and save it locally

```sh
openai images generate --prompt "A tiny orange robot watering a houseplant"
```

Images are saved to `~/Downloads/gpt-images/`, and the command prints their paths.
Use `--output-dir` for an existing destination directory or `--name` for a filename.
Supported terminals can preview the result. See the [image guide](docs/image-generation-saving.md)
for editing, model selection, and preview settings.

### Upload a file and retrieve it later

```sh
openai files upload report.txt --purpose user_data
openai files get file-example
openai files download file-example --output downloaded-report.txt
```

Use your own local file and replace `file-example` with the ID returned by the upload.
See [working with files](docs/files.md) for more examples.

### Run requests in a batch

Submit a JSONL request file, then wait for the batch and download its results:

```sh
openai files upload requests.jsonl --purpose batch
openai batches create --input-file-id file-example --endpoint /v1/responses --completion-window 24h
openai batches wait batch_example --output-dir ./results
```

Replace the file and batch IDs with the returned IDs. The [batch guide](docs/batches.md)
includes a sample `requests.jsonl` and explains result files and failed requests.

### Export API data for a script

```sh
openai models list --format jsonl | jq '.id'
openai files list --format json > files.json
```

Choose `--format json` for a JSON document or `--format jsonl` for records.
The default output is readable text, **even in pipes**. The first example uses
[jq](https://jqlang.org/).

## Usage

Commands follow the API's resources:

```sh
openai <resource> [subresource...] <command> [flags...]
```

Browse commands and options with help. Help needs no API key or network connection.

```sh
openai --help
openai images generate --help
openai admin organization --help
```

`openai help COMMAND` and `openai COMMAND --help` show the same page.
Commands with many options list every local flag in a compact index.
For example, `openai help --all images generate` shows flag descriptions, defaults, and accepted values.
Existing colon-separated commands, such as `openai admin:organization:usage completions`,
also work.

### Admin requests

Admin endpoints use `OPENAI_ADMIN_KEY`, a separate
[admin API key](https://platform.openai.com/settings/organization/admin-keys).
For example, retrieve one day of organization usage:

```sh
openai admin organization usage completions --start-time 1735689600 --end-time 1735776000 --bucket-width 1d
```

### Output and errors

Successful JSON responses display readable text by default. Select a data format
explicitly when parsing results. Use `--transform` to extract fields with
[GJSON syntax](https://github.com/tidwall/gjson/blob/master/SYNTAX.md):

```sh
openai files get file-example --transform filename --raw-output
```

Errors go to stderr as readable summaries. Use `--format-error json` for API error
details, or `--transform-error` to extract error fields. Explicit `--format json`,
`jsonl`, `raw`, and `yaml` also apply to errors unless `--format-error` overrides
them; `--format-error text` restores summaries. API error details and `--debug`
logs can contain sensitive data: sanitize them before sharing.

`--debug` adds per-attempt HTTP timing. Add `--debug-body` for redacted JSON
previews or `--debug-sse --debug-event-timing` for bounded stream diagnostics.
See [HTTP debugging](docs/debugging.md) for capture limits and timing boundaries.

Check the command's exit status before using redirected results; a failed request
can leave an incomplete output file. See [reading command results](docs/readable-output.md)
for list formats, streaming, binary downloads, and error behavior.

### File input

Binary uploads take a plain path. JSON input flags can read a value from a local
file with `@path`:

```sh
openai files upload report.txt --purpose user_data
openai responses create --model gpt-5.5 --input @prompt.txt
```

For a complete request, `responses create` and `chat completions create` accept `--request-file request.json`.
Use `--request-file -` to wait for a pipeline, including delayed producers.
Run `openai help piping` for tokenizer, upload, and script examples.

Use `OPENAI_UNTRUSTED_STDIN=1` when piping JSON or YAML from an untrusted source
so input cannot cause local file reads. In that mode, provide upload paths
explicitly as flags. See [file arguments and encoding](docs/cli-reference.md#passing-files-as-arguments)
for escaping literal `@` values, JSON/YAML input, and text or base64 encoding.

### Shell shortcuts

Set up [shell completion](docs/shell-completion.md) to complete commands and file
paths. [Command insertion](docs/command-insertion.md) lets you search examples
with Ctrl+X, then Tab and insert them without running them.

The first eligible interactive run saves image-picker Tab shortcuts for future
Bash, zsh, and fish sessions. Open a new terminal to use them. See
[setup and removal](docs/image-picker-shell-setup.md) for requirements and opt-out.
In PowerShell, press Enter after `openai images generate` to open the picker.

## Guides

| Task | Guide |
| --- | --- |
| Generate, edit, save, and preview images | [Images](docs/image-generation-saving.md) |
| Upload and download files | [Files](docs/files.md) |
| Submit batches and collect results | [Batches](docs/batches.md) |
| Create vector stores, upload files, and search | [Vector stores](docs/vector-store-workspace.md) |
| Generate speech and work with audio | [Audio](docs/audio-workbench.md) |
| Inspect and count tokens locally | [Tokenizer](docs/local-utilities.md) |
| Test webhook handling | [Webhooks](docs/webhook-testing.md) |
| Parse results in scripts | [Output formats](docs/readable-output.md) |
| Configure headers, mTLS, and file input | [CLI reference](docs/cli-reference.md) |

> [!TIP]
> **Managing OpenAI resources with Terraform?** Use the official
> [OpenAI Terraform provider](https://github.com/openai/terraform-provider-openai)
> to manage projects, service accounts, rate limits, and other supported resources
> as infrastructure as code.

## Use the CLI from a coding agent

The optional [OpenAI CLI skill](skills/openai-cli/SKILL.md) helps a shell agent choose API commands and interpret their output.
Install the CLI in the environment where the agent runs.

From your project, use GitHub CLI to install the skill from a tagged release that includes it:

```sh
gh skill install openai/openai-cli openai-cli --agent codex --scope project
```

Choose your agent with `--agent`. Options include `claude-code`, `cursor`, `gemini-cli`, and `github-copilot`.
Run `gh skill install --help` for the full list. Use `--scope user` to share the skill across your projects.

For an unreleased checkout, use its local path instead:

```sh
gh skill install /path/to/openai-cli openai-cli --from-local --agent codex --scope project
```

For manual installation, copy the entire `skills/openai-cli` folder into the appropriate skills directory.

| Agent | Current project | All projects for this user |
| --- | --- | --- |
| Codex | `.agents/skills/` | `~/.agents/skills/` |
| Claude Code | `.claude/skills/` | `~/.claude/skills/` |

Start a new agent session in the target project. For example, ask “Upload requests.jsonl to OpenAI for a future batch.”

## Development

<a id="installing-with-go"></a>

To install from source, follow the [Go installation instructions](docs/installation.md#installing-with-go).

`scripts/run` is a development helper for running the CLI from a cloned checkout:

```sh
./scripts/run --help
```

See the [development instructions](CONTRIBUTING.md#running-and-building-locally)
for its dependency, credential, path, and exit-status behavior.

To test against another OpenAI Go SDK version, use `./scripts/link`:

```sh
./scripts/link github.com/org/repo@version
./scripts/link ../path/to/openai-go
```

With no arguments, it uses `../openai-go`. See [CONTRIBUTING.md](CONTRIBUTING.md)
for setup, checks, and repository structure.

## Contributing

Please share bug reports and feature requests through [GitHub issues](https://github.com/openai/openai-cli/issues).
Pull requests are limited to repository collaborators; we do not accept pull requests from non-collaborators.
See [CONTRIBUTING.md](./CONTRIBUTING.md) for the contribution policy and development guide.
See the [security model](docs/architecture/security-model.md) for trust boundaries.
For security vulnerabilities, follow [SECURITY.md](SECURITY.md).

## License

Copyright 2026 OpenAI

This project is licensed under the [Apache License 2.0](LICENSE).
