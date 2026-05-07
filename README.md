# OpenAI CLI

The official CLI for the [OpenAI REST API](https://platform.openai.com/docs).

<!-- x-release-please-start-version -->

## Installation

### Installing with Homebrew

```sh
brew install openai/tools/openai
```

### Installing with Go

To test or install the CLI locally, you need [Go](https://go.dev/doc/install) version 1.25 or later installed.

```sh
go install 'github.com/openai/openai-cli/cmd/openai@latest'
```

Once you have run `go install`, the binary is placed in your Go bin directory:

- **Default location**: `$HOME/go/bin` (or `$GOPATH/bin` if GOPATH is set)
- **Check your path**: Run `go env GOPATH` to see the base directory

If commands aren't found after installation, add the Go bin directory to your PATH:

```sh
# Add to your shell profile (.zshrc, .bashrc, etc.)
export PATH="$PATH:$(go env GOPATH)/bin"
```

<!-- x-release-please-end -->

### Running Locally

After cloning the git repository for this project, you can use the
`scripts/run` script to run the tool locally:

```sh
./scripts/run args...
```

## Usage

The CLI follows a resource-based command structure:

```sh
openai [resource] <command> [flags...]
```

Standard API endpoints require an [API key](https://platform.openai.com/settings/organization/api-keys):

```sh
export OPENAI_API_KEY="sk-..."

openai responses create \
  --input "Say this is a test" \
  --model gpt-5.5
```

Admin endpoints require an [admin API key](https://platform.openai.com/settings/organization/admin-keys):

```sh
export OPENAI_ADMIN_KEY="sk-admin-..."

openai admin:organization:usage completions \
  --start-time 1735689600 \
  --end-time 1735776000 \
  --bucket-width 1d
```

For details about specific commands, use the `--help` flag.

### Environment variables

| Environment variable    | Required | Default value |
| ----------------------- | -------- | ------------- |
| `OPENAI_API_KEY`        | no       | `null`        |
| `OPENAI_ADMIN_KEY`      | no       | `null`        |
| `OPENAI_ORG_ID`         | no       | `null`        |
| `OPENAI_PROJECT_ID`     | no       | `null`        |
| `OPENAI_WEBHOOK_SECRET` | no       | `null`        |

### Global flags

- `--api-key` (can also be set with `OPENAI_API_KEY` env var)
- `--admin-api-key` (can also be set with `OPENAI_ADMIN_KEY` env var)
- `--organization` (can also be set with `OPENAI_ORG_ID` env var)
- `--project` (can also be set with `OPENAI_PROJECT_ID` env var)
- `--webhook-secret` (can also be set with `OPENAI_WEBHOOK_SECRET` env var)
- `--help` - Show command line usage
- `--debug` - Enable debug logging. This includes HTTP request/response details and bodies; do not share debug logs if they may contain sensitive payloads.
- `--version`, `-v` - Show the CLI version
- `--base-url` - Use a custom API backend URL
- `--format` - Change the output format (`auto`, `explore`, `json`, `jsonl`, `pretty`, `raw`, `yaml`)
- `--format-error` - Change the output format for errors (`auto`, `explore`, `json`, `jsonl`, `pretty`, `raw`, `yaml`)
- `--transform` - Transform the data output using [GJSON syntax](https://github.com/tidwall/gjson/blob/master/SYNTAX.md)
- `--transform-error` - Transform the error output using [GJSON syntax](https://github.com/tidwall/gjson/blob/master/SYNTAX.md)

### Passing files as arguments

To pass files to your API, you can use the `@myfile.ext` syntax:

```bash
openai <command> --arg @abe.jpg
```

Files can also be passed inside JSON or YAML blobs:

```bash
openai <command> --arg '{image: "@abe.jpg"}'
# Equivalent:
openai <command> <<YAML
arg:
  image: "@abe.jpg"
YAML
```

If you need to pass a string literal that begins with an `@` sign, you can
escape the `@` sign to avoid accidentally passing a file.

```bash
openai <command> --username '\@abe'
```

#### Explicit encoding

For JSON endpoints, the CLI tool does filetype sniffing to determine whether the
file contents should be sent as a string literal (for plain text files) or as a
base64-encoded string literal (for binary files). If you need to explicitly send
the file as either plain text or base64-encoded data, you can use
`@file://myfile.txt` (for string encoding) or `@data://myfile.dat` (for
base64-encoding). Note that absolute paths will begin with `@file://` or
`@data://`, followed by a third `/` (for example, `@file:///tmp/file.txt`).

```bash
openai <command> --arg @data://file.txt
```

## Linking different Go SDK versions

You can link the CLI against a different version of the OpenAI Go SDK
for development purposes using the `./scripts/link` script.

To link to a specific version from a repository (version can be a branch,
git tag, or commit hash):

```bash
./scripts/link github.com/org/repo@version
```

To link to a local copy of the SDK:

```bash
./scripts/link ../path/to/openai-go
```

If you run the link script without any arguments, it will default to `../openai-go`.

## License

Copyright 2026 OpenAI

This project is licensed under the [Apache License 2.0](LICENSE).
