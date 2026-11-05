# Authentication status

Run `openai auth status` to inspect configuration without contacting OpenAI.
The command does not verify identity, credentials, permissions, membership, or billing.
It does not read stdin, refresh credentials, save settings, or configure your shell.

```sh
openai auth status
openai auth status --format json
```

The report shows API credentials and Admin credentials separately.
Each credential includes its configured state, type, and source. The credential fields contain only this metadata.
Organization, project, and base URL each include their own source.
If no CLI input selects an organization or project, text output shows `not set in CLI`.
The offline report cannot identify the organization or project associated with a key.
JSON reports an absent setting with `"value": ""` and `"source": "unset"`.
An explicitly empty setting shows `(empty)` in text and retains its flag or environment source in both formats.
If a displayed setting contains a configured credential, the report replaces that setting with `[redacted]`.
`offline: true` and `verified: false` describe the JSON report.
Missing credentials still produce a successful configuration report.

## Selection

The report follows existing CLI request selection:

- Literal `--header Authorization: ...` overrides API and Admin keys, including an explicitly empty header.
- A nonempty API key or Admin key overrides an Authorization header from `OPENAI_CUSTOM_HEADERS`.
- An empty key option suppresses its environment key. An environment Authorization header can still apply.
- Literal organization and project headers override their corresponding options.
- Organization and project options override environment custom headers, including explicitly empty values.
- Repeated options retain the parser's existing last-value behavior.
- A nonempty `--base-url` overrides `OPENAI_BASE_URL`. An empty flag falls back to the environment.
- An explicitly empty `OPENAI_BASE_URL` remains empty. An absent value selects the SDK production URL.

Explicit flag assignment remains distinguishable from environment input, even when their values match.
Sources use `flag:--NAME`, `environment:VARIABLE`, `default`, or `unset`.
Header-derived values identify `--header` or `OPENAI_CUSTOM_HEADERS` as their source.
The report does not infer credentials or permissions from arbitrary gateway headers.

URL user information can provide HTTP Basic authentication when the effective Authorization header is empty.
The report identifies this as `url_basic_auth`, without displaying the username or password.
Base URLs omit user information, queries, fragments, and custom paths.
Only the origin and known `/` or `/v1` paths remain visible. `redacted` identifies omitted components.
The CLI requires a hostname. Use HTTPS for remote servers. Localhost and loopback IP addresses also support HTTP.
If the address fails validation, the command reports an error without printing the address.

## Output

`auto` and `text` produce plain labeled text, including when stdout is redirected.
`json` produces one JSON object. Terminal control characters remain escaped in text output.
The command rejects positional arguments, other output formats, `--transform`, and `--raw-output`.
Existing error-format, quiet, and diagnostic policies remain in effect.
Output failures return an error. The report does not test network connectivity or account access.
