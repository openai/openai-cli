# CLI configuration and input reference

For installation and common tasks, see the [README](../README.md).
Run `openai --help` for the full set of global options.

## Environment variables

| Environment variable | Required | Default value |
| --- | --- | --- |
| `OPENAI_API_KEY` | no | `null` |
| `OPENAI_ADMIN_KEY` | no | `null` |
| `OPENAI_ORG_ID` | no | `null` |
| `OPENAI_PROJECT_ID` | no | `null` |
| `OPENAI_WEBHOOK_SECRET` | no | `null` |
| `OPENAI_BASE_URL` | no | `https://api.openai.com/v1` |
| `OPENAI_CLI_NO_UPDATE_CHECK` | no | unset |
| `OPENAI_CUSTOM_HEADERS` | no | `null` |
| `OPENAI_MTLS_CLIENT_CERT_FILE` | no | `null` |
| `OPENAI_MTLS_CLIENT_KEY_FILE` | no | `null` |
| `OPENAI_UNTRUSTED_STDIN` | no | `false` |

## Global flags

- `--api-key` (can also be set with `OPENAI_API_KEY` env var)
- `--admin-api-key` (can also be set with `OPENAI_ADMIN_KEY` env var)
- `--organization` (can also be set with `OPENAI_ORG_ID` env var)
- `--project` (can also be set with `OPENAI_PROJECT_ID` env var)
- `--webhook-secret` (can also be set with `OPENAI_WEBHOOK_SECRET` env var)
- `--mtls-client-cert-file` (can also be set with `OPENAI_MTLS_CLIENT_CERT_FILE` env var)
- `--mtls-client-key-file` (can also be set with `OPENAI_MTLS_CLIENT_KEY_FILE` env var)
- `--help` - Show command line usage
- `--debug` - Enable debug logging. This includes HTTP request/response details and bodies; do not share debug logs if they may contain sensitive payloads.
- `--version`, `-v` - Show the CLI version
- `--base-url` - Use a custom API backend URL. HTTPS is required except for
  `localhost` or literal loopback IP addresses such as `127.0.0.1` and `::1`.
  This also applies to `OPENAI_BASE_URL`; remote HTTP backends must enable HTTPS.
- `--header`, `-H` - Add a literal request header as `Name: Value`; repeat for multiple headers
- `--format` - Change the output format (`auto`, `text`, `explore`, `json`, `jsonl`, `pretty`, `raw`, `yaml`); `auto` selects readable text unless extracting data
- `--format-error` - Change the stderr error format (`auto`, `text`, `explore`, `json`, `jsonl`, `pretty`, `raw`, `yaml`); `auto` selects readable summaries unless extracting error data
- `--transform` - Transform the data output using [GJSON syntax](https://github.com/tidwall/gjson/blob/master/SYNTAX.md)
- `--transform-error` - Transform the error output using [GJSON syntax](https://github.com/tidwall/gjson/blob/master/SYNTAX.md)

## Request headers

Use `--header` or `-H` before or after a resource command:

```sh
openai --header 'X-Trace-ID: example-123' models list \
  -H 'X-Client-Label: local,manual'
```

Values are literal: commas and colons are preserved, and `@` prefixes do not
read files. Surrounding spaces and tabs are trimmed from values. `-H 'X-Example:'`
sends an empty value. If a name is repeated, the last value wins regardless of
case, overriding default and endpoint-specific headers. Headers managed by the
HTTP transport, such as `Host` and `Content-Length`, retain the transport's
behavior. Supplied header values are hidden in help and redacted in debug logs.

For credential-bearing headers, have a secret manager or trusted launcher set
`OPENAI_CUSTOM_HEADERS` directly in the CLI's environment. This keeps credentials
out of command arguments and avoids typing literal secrets into shell history.
The variable accepts one `Name: Value` entry per line. For example, with
non-sensitive values:

```sh
export OPENAI_CUSTOM_HEADERS='X-Trace-ID: example-123
X-Client-Label: local,manual'
openai models list
```

`--header` and endpoint-specific header flags override matching environment
entries. Environment header values are also redacted in debug logs. Use
`OPENAI_API_KEY` and `OPENAI_ADMIN_KEY` for standard API and admin authentication.

## Mutual TLS

Mutual TLS is currently in beta. To opt in and activate a CA certificate for
your organization or project, follow the
[OpenAI Mutual TLS Beta Program](https://help.openai.com/en/articles/10876024-openai-mutual-tls-beta-program)
instructions.

To authenticate API-key requests with a mutual TLS client certificate, provide
the client certificate and private key as separate PEM files. If certificate
chain support is enabled for your organization, the certificate file must
contain the leaf certificate first, followed by any intermediate certificates.
Otherwise, use a client certificate signed directly by an activated CA
certificate.

```sh
export OPENAI_MTLS_CLIENT_CERT_FILE=/run/secrets/openai/client-chain.pem
export OPENAI_MTLS_CLIENT_KEY_FILE=/run/secrets/openai/client.key
export OPENAI_BASE_URL=https://mtls.api.openai.com/v1

openai files list
```

Both mTLS files must be configured together. Keep the private key in a
permission-restricted file; do not put private-key contents directly in command
arguments or environment variables. The CLI does not automatically select an
mTLS endpoint, so configure `OPENAI_BASE_URL` or `--base-url` explicitly. HTTP
and SOCKS proxies remain supported, but HTTPS proxies are rejected to prevent
presenting the client certificate during the proxy's own TLS handshake.

## Passing files as arguments

For JSON input values, `@myfile.ext` reads local file contents:

Binary upload flags use plain paths. For example, use `files upload --file "upload space.txt" --purpose user_data`.
Here, `--file "@upload space.txt"` names an actual file starting with `@`; it does not expand another file reference.
See [working with files](files.md).

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

When piping JSON or YAML from an untrusted source, enable untrusted-stdin mode:

```bash
untrusted-producer | OPENAI_UNTRUSTED_STDIN=1 openai <command> --file ./upload.txt
```

In this mode, values supplied through stdin are treated as literal data: `@`,
`@file://`, and `@data://` references in request bodies, headers, and query
parameters do not read local files. File-upload parameters must be provided
explicitly as command-line flags. Explicit flags retain their normal file
behavior and take precedence over values supplied through stdin. Leave
`OPENAI_UNTRUSTED_STDIN` unset when using trusted heredocs or other trusted
piped input that intentionally references local files.

If you need to pass a string literal that begins with an `@` sign, you can
escape the `@` sign to avoid accidentally passing a file.

```bash
openai <command> --username '\@abe'
```

### Explicit encoding

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

## Update notices

Bare `openai` checks for newer stable releases when stdin, stdout, and stderr are terminals.
It adds a plain-text notice below help, at most once every seven days.
Explicit help, commands, completion, CI, and redirected streams skip the check entirely.

The notice includes an update command when the CLI can verify its installation method.
Verified Homebrew installations use `brew upgrade --cask openai/tools/openai`.
Go commands require a verified binary, an available Go command, and an explicit matching `GOBIN`.
Custom `GOOS` or `GOARCH` settings select the installation guide instead.
Go upgrades to major version 2 or later also select the installation guide.
Unknown installations receive the installation-guide link instead.
The CLI never installs updates automatically.

Checks use public GitHub release metadata without API credentials, at most once daily.
Checks respect `HTTPS_PROXY` and `NO_PROXY`, including their lowercase forms.
Update discovery waits at most 200 milliseconds. Failed checks remain silent.
Package-manager availability can lag a new GitHub release.
Set `OPENAI_CLI_NO_UPDATE_CHECK=1` to disable checks and notices.
