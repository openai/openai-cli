# Authentication status

Run `openai auth status` to inspect configuration without contacting OpenAI.
The default command does not verify identity, credentials, permissions, membership, or billing.
It does not read stdin, refresh credentials, save settings, or configure your shell.

```sh
openai auth status
openai auth status --format json
```

The report shows API credentials and Admin credentials separately.
Each credential includes its configured state, type, and source. It never includes credential values or fragments.
Organization, project, and base URL each include their own source.
`offline: true` and `verified: false` describe the JSON report.
Missing credentials still produce a successful configuration report.
`api_identity` and `admin_identity` explicitly report `status: "unknown"`.
Their reason is `not_configured` for missing credentials, or `not_requested` otherwise.

## Identity lookup

Select one credential explicitly to request its authenticated principal:

```sh
openai auth status --verify --credential-kind api
openai auth status --verify --credential-kind admin --format json
```

The lookup sends one `GET /v1/me` request using the selected effective credential.
API and Admin credentials never substitute for each other.
The request includes the resolved organization and project headers.
It does not forward arbitrary custom headers, cookies, or gateway credentials.
The selected Authorization header must use the Bearer scheme.

The provider accepts `https://api.openai.com/v1` and `https://api.openai.com/v1/`, including explicit port 443.
Other endpoints, paths, ports, URL credentials, queries, and fragments produce `unsupported_provider` without a request.
The command never replaces an unsupported endpoint with production.
Certificate options require an explicit supported base URL, even when a profile selects that URL.

The provider returns an authenticated principal ID, plus nullable name and email fields.
It does not identify the active key resource, distinguish humans from service accounts, or establish membership or permissions.
The `/v1/me` route is absent from the public SDK and API reference.
Backend contract inspection establishes its response semantics; deployed availability remains unverified.
Unavailable or incompatible responses leave identity unknown.
The lookup performs no model request, but authentication can update key usage metadata.

Successful JSON sets `offline: false`, `verified: true`, and `verified_credential_kind` to the selected credential kind.
Here, `verified` describes only that credential's authenticated principal.
It does not verify the entire configuration, organization, project, permissions, or billing.
The selected identity contains `status: "resolved"`, `provider: "openai_me_v1"`, `id`, `name`, and `email`.
The other identity remains unknown.
Unknown identities have a `reason`, null `name` and `email`, and no `id` or `provider`.

Failures produce an unknown identity report and a nonzero exit status.
Reasons include `unauthorized`, `forbidden`, `rate_limited`, `unavailable`, `redirect`, `invalid_response`, `request_failed`, and `timed_out`.
Invalid local credentials or TLS configuration produce `invalid_configuration` without a request.
Missing selected credentials produce `not_configured` without fallback.
The command discards provider error bodies and never includes them in diagnostics, including debug output.

The lookup has a 15-second deadline, follows no redirects, and performs no automatic retries.
Interrupt and termination signals stop the lookup without printing a status report.
Their exit statuses are 130 and 143, respectively.
The command never caches identity, saves credentials, or stores credential hashes.
Each lookup uses the same configuration snapshot that supplies the report.

## Selection

The report follows existing CLI request selection:

- Literal `--header Authorization: ...` overrides API and Admin keys, including an explicitly empty header.
- A nonempty API key or Admin key overrides an Authorization header from `OPENAI_CUSTOM_HEADERS`.
- An empty key option suppresses its environment key. An environment Authorization header can still apply.
- Literal organization and project headers override their corresponding options.
- Root organization and project flags override present environment values, then selected-profile fields.
- A present profile project overrides folder links. Absent profile fields permit fallback; empty fields suppress it.
- Folder links precede custom-header fallback. Explicit headers retain their higher priority.
- Repeated options retain the parser's existing last-value behavior.
- A nonempty `--base-url` overrides `OPENAI_BASE_URL`. An empty flag falls back to the environment.
- An explicitly empty `OPENAI_BASE_URL` remains empty. An absent environment value permits the selected profile endpoint.
- An effective empty stored endpoint selects the SDK production URL. Status reports that URL with profile provenance.

Explicit flag assignment remains distinguishable from environment input, even when their values match.
Sources use `flag:--NAME`, `environment:VARIABLE`, `profile:NAME`, `folder`, `default`, or `unset`.
Root `--profile`, present `OPENAI_PROFILE`, then the saved default select a profile.
An empty selector disables the saved default; folder fallback remains eligible.
Unknown selected profiles fail without fallback. See [profiles](profiles.md) for local management.
Header-derived values identify `--header` or `OPENAI_CUSTOM_HEADERS` as their source.
The report does not infer credentials or permissions from arbitrary gateway headers.

URL user information can provide HTTP Basic authentication when the effective Authorization header is empty.
The report identifies this as `url_basic_auth`, without displaying the username or password.
Base URLs omit user information, queries, fragments, and custom paths.
Only the origin and known `/` or `/v1` paths remain visible. `redacted` identifies omitted components.
Malformed effective URLs produce a fixed error without repeating the URL.

## Output

`auto` and `text` produce plain labeled text, including when stdout is redirected.
`json` produces one JSON object. Terminal control characters remain escaped in text output.
The command rejects positional arguments, other output formats, `--transform`, and `--raw-output`.
Existing error-format, quiet, and diagnostic policies remain in effect.
Output failures return an error.
The offline report does not test network connectivity or account access.
