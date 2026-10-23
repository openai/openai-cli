# OpenAI CLI security model

This document is the single canonical detailed threat model for this
repository. It is reusable across changes and is the authority for
trust-boundary decisions during Codex Security scans. [SECURITY.md](../../SECURITY.md)
remains the authority for private vulnerability reporting and coordinated
disclosure.

## 1. Overview

`openai-cli` is a local Go command-line client for OpenAI APIs. It is
not a background daemon. Ordinary API commands do not open inbound listeners.
The explicit `audio translate --live` mode opens the private loopback browser
companion described below. A user invokes resource commands; the CLI parses flags
or piped YAML/JSON, may read local files, constructs requests through `openai-go`, and renders API responses to a
terminal, pager, pipe, or file. The entry point runs the generated command
tree, validates an environment-selected base URL, and formats API errors
([internal/app/run.go](../../internal/app/run.go),
[generated/commands/cmd.go:26-45](../../generated/commands/cmd.go#L26-L45)).

Endpoint commands and request tests under `generated/commands/` are generated
from the checked-in OpenAPI snapshot. Shared request plumbing remains
handwritten in `internal/app/` and `internal/request/`, including request
policy wiring and mTLS transport construction.
That split matters for
maintenance, but both generated and handwritten tracked source are repository
code once reviewed and executed
([CONTRIBUTING.md:51-64](../../CONTRIBUTING.md#L51-L64),
[generated/commands/cmd.go:1](../../generated/commands/cmd.go#L1)).

| Component | Responsibility | Evidence |
| --- | --- | --- |
| `cmd/openai`, `internal/app`, and generated `generated/commands` commands | Parse CLI arguments, construct SDK clients, and invoke API resources, including ordinary and organization-administration operations. | [internal/app/run.go](../../internal/app/run.go), [generated/commands/cmd.go:82-101](../../generated/commands/cmd.go#L82-L101), [generated/commands/cmd.go:103-150](../../generated/commands/cmd.go#L103-L150) |
| `internal/requestflag`, `internal/request`, and `internal/app/flagoptions.go` | Map flags and piped YAML/JSON into path, query, header, and body values; expand file references; serialize requests. | [internal/requestflag/requestflag.go:61-80](../../internal/requestflag/requestflag.go#L61-L80), [internal/request/flag_options.go](../../internal/request/flag_options.go), [internal/app/flagoptions.go](../../internal/app/flagoptions.go), [internal/request/piped_input.go](../../internal/request/piped_input.go), [internal/request/file_expand.go](../../internal/request/file_expand.go), [internal/request/encoding.go](../../internal/request/encoding.go) |
| `internal/request/mtls.go` | Read a caller-selected client certificate/key pair and construct the custom mTLS transport. | [internal/request/mtls.go](../../internal/request/mtls.go), [internal/request/mtls.go](../../internal/request/mtls.go) |
| `internal/debugmiddleware` and output code | Log redacted HTTP metadata when requested and display or write API responses. | [internal/debugmiddleware/debug_middleware.go:43-63](../../internal/debugmiddleware/debug_middleware.go#L43-L63), [internal/output/format.go](../../internal/output/format.go), [internal/output/iterator.go](../../internal/output/iterator.go), [generated/commands/response.go:429-481](../../generated/commands/response.go#L429-L481) |
| Development and release tooling | Run local tests/mock tooling and build, validate, sign, attest, and publish official release artifacts. | [scripts/test:28-61](../../scripts/test#L28-L61), [docs/releasing.md:3-29](../releasing.md#L3-L29) |

```mermaid
flowchart LR
  caller[Local caller or wrapper] -->|flags, env, stdin, files| cli[OpenAI CLI process]
  cli -->|HTTPS request and credentials| api[OpenAI API or caller-selected backend]
  api -->|response data| cli
  cli -->|terminal, pager, pipe, file| sink[Local output sink]
  pr[Candidate checkout code] -->|build and test| ci[Ordinary PR execution: no repository permissions]
  tag[Validated main tag] -->|generate fixed artifacts| inputs[Release-input job: read token, no protected credentials]
  inputs -->|verified fixed artifacts| publish[Protected publish job]
```

| Deployment or workflow | Resource or capability | Configuration and precedence | Safe effective value or location | Readers, writers, or recipients | Enforcing control | Evidence or unknowns |
| --- | --- | --- | --- | --- | --- | --- |
| Normal CLI invocation | API/admin credentials and tenant selectors | Global flags may source `OPENAI_API_KEY`, `OPENAI_ADMIN_KEY`, `OPENAI_ORG_ID`, `OPENAI_PROJECT_ID`, and `OPENAI_WEBHOOK_SECRET`. | Process memory and outbound SDK request options; never repository files. | Invoking process and selected API backend. | Caller chooses credentials; server remains authoritative for authentication, authorization, and tenancy. | [generated/commands/cmd.go:82-101](../../generated/commands/cmd.go#L82-L101), [internal/request/default_options.go](../../internal/request/default_options.go) |
| Normal CLI invocation | Request destination | `--base-url` overrides the default; `OPENAI_BASE_URL` is validated at startup. | Default SDK HTTPS endpoint or explicit HTTPS backend; HTTP is allowed only for exact `localhost` or literal loopback IP addresses. | Selected network origin and any configured proxy. | CLI parses the effective URL and requires HTTPS outside the loopback exception before request setup; `openai-go` owns normal transport, redirects, and auth-header behavior. | [generated/commands/cmd.go:39-45](../../generated/commands/cmd.go#L39-L45), [internal/app/run.go](../../internal/app/run.go), [internal/request/default_options.go](../../internal/request/default_options.go); default SDK behavior is an external dependency. |
| mTLS invocation | Client certificate and private key | Dedicated flags or `OPENAI_MTLS_CLIENT_CERT_FILE` / `OPENAI_MTLS_CLIENT_KEY_FILE`; both are required together. | Caller-selected PEM files read locally; key bytes are cleared after use. | CLI process and same-origin HTTPS endpoint only. | Explicit HTTPS base URL, matching pair, HTTPS-proxy rejection, and same-origin redirect limit. | [internal/request/mtls.go](../../internal/request/mtls.go), [internal/request/mtls.go](../../internal/request/mtls.go) |
| Piped request input | YAML/JSON and referenced local files | Explicit flags take precedence; unset request fields can be populated from stdin. `OPENAI_UNTRUSTED_STDIN` preserves provenance. | Parsed in process; trusted `@` references may read files, while untrusted-stdin values remain literal. | CLI process, local files explicitly referenced under trusted mode, selected API backend. | Single-consumer stdin, provenance-marked literal values, and rejection of stdin-derived binary file paths in untrusted mode. | [internal/request/flag_options.go](../../internal/request/flag_options.go), [internal/app/flagoptions.go](../../internal/app/flagoptions.go), [internal/request/piped_input.go](../../internal/request/piped_input.go), [internal/request/stdin_policy.go](../../internal/request/stdin_policy.go), [internal/request/stdin_consumers.go](../../internal/request/stdin_consumers.go), [internal/request/input_values.go](../../internal/request/input_values.go), [internal/request/file_expand.go](../../internal/request/file_expand.go), [internal/request/stdin_reader.go](../../internal/request/stdin_reader.go) |
| Integration tests | Local mock executable and OpenAPI snapshot | Checked-in spec is hash-checked by default; an explicit local path or URL is caller-selected and its content is untrusted. | Pinned fork source and checksum-verified Deno runtime; mock binds `127.0.0.1:4010`. | Local test process and any caller-selected specification origin or referenced resource. | Full Git commit, pristine source checks, HTTPS-only runtime download and redirects, runtime SHA-256 verification, and frozen Deno dependencies authenticate tooling; they do not authenticate caller-selected specification content. | [scripts/mock](../../scripts/mock), [scripts/steady/install](../../scripts/steady/install), [scripts/steady/settings](../../scripts/steady/settings) |
| Ordinary test/build pull-request CI | Candidate checkout execution | A separate preparation job checks out the candidate as data; CI execution jobs restore its same-run artifact and run tracked scripts/tests. | Preparation has read-only repository credentials and executes only reviewed workflow instructions and trusted-main tooling. Candidate execution has `permissions: {}` and no repository-read or protected credentials. | Source preparation, then candidate runners and configured test dependencies. | Separate runners and immutable same-run source artifacts; no candidate helper or local action runs in preparation. | [.github/workflows/ci.yml:1-27](../../.github/workflows/ci.yml#L1-L27), [.github/workflows/ci.yml:110-127](../../.github/workflows/ci.yml#L110-L127) |
| CodeQL analysis and publication | Candidate analysis; trusted result publication | `.github/workflows/codeql.yml` analyzes the candidate with autobuild and stages SARIF. `codeql-upload.yml` runs separately on `workflow_run` using trusted main tooling. | Analysis has `permissions: {}` and consumes prepared same-run source artifacts. Only the trusted publisher has `security-events: write` and `statuses: write`; neither job receives release/signing credentials. | Candidate runner writes untrusted report artifacts; trusted publisher submits them to GitHub code scanning. | Publisher executes no candidate code, reads fixed ZIP members as data, verifies the source run/attempt and current GitHub SHA/ref, and fixes the tool/language categories. This confines publication authority; it does not attest candidate report correctness. | [.github/workflows/codeql.yml](../../.github/workflows/codeql.yml), [.github/workflows/codeql-upload.yml](../../.github/workflows/codeql-upload.yml), [.github/scripts/codeql_upload.py](../../.github/scripts/codeql_upload.py) |
| Release | Official artifacts and publishing credentials | A job without protected write/signing credentials generates fixed release inputs from a validated tag; a protected publisher verifies them before obtaining publishing credentials. | Fixed artifact paths, protected `publish` environment, scoped app tokens, signing/notarization secrets. | Release jobs, GitHub releases, Homebrew, artifact attestation. | Tag-on-main and SHA binding, fixed artifact verification, verified GoReleaser, protected environment, and isolated attestation permissions. | [docs/releasing.md:3-29](../releasing.md#L3-L29), [.github/workflows/publish-release.yml:17-90](../../.github/workflows/publish-release.yml#L17-L90), [.github/workflows/publish-release.yml:92-152](../../.github/workflows/publish-release.yml#L92-L152), [.github/workflows/publish-release.yml:247-273](../../.github/workflows/publish-release.yml#L247-L273) |

### Optional live translation browser companion

Only `audio translate --live` starts the browser companion. It binds
`127.0.0.1` on an operating-system-selected port, not a public interface.
Each launch uses a separate `.localhost` hostname containing 128 random bits.
The browser origin changes independently of the port and authorization capability.
The private launch page opens the companion only when desktop Chromium
Client Hints confirm a supported browser. Otherwise, it shows instructions
for current Chrome or Edge without navigating to the local address.
The CLI prints the private launch file path for opening it in another browser.
Loopback binding alone does not authorize private operations.
The server validates the exact Host on every request. Private routes require
an unpredictable capability header. They reject a supplied Origin that differs
from the local Origin. Write requests also require that Origin header.
If supplied on private routes, `Sec-Fetch-Site` must be `same-origin`.
The server grants no CORS permission
([companion server](../../internal/audiolive/server.go)).

A private launch file carries the local address and capability fragment.
The browser removes that fragment from its current history entry after reading it.
The opener receives only the file URL, without the capability in its arguments.
The launch directory and file use private creation permissions, including the
existing protected Windows file helpers
([launch ownership](../../internal/audiolive/server.go),
[Non-Windows private files](../../internal/audiolive/private_other.go),
[Windows private files](../../internal/audiolive/private_windows.go)).
Static page assets contain no credential or retained recording. Their delivery
does not authorize private routes.

On explicit Start, the page requests microphone or display-capture permission
through browser APIs. Tab capture forwards audio samples, not its video track.
The API key stays in the CLI; the browser uses the local capability.
The Go adapter retains the application's configured SDK credential and transport
policy. Browser capture permission does not establish remote API authorization
([capture controls](../../internal/audiolive/translation.js),
[API adapter](../../internal/commands/audio/audio_translation.go),
[application policy](../../internal/app/audio_policy.go)).

The CLI owns the listener, private launch file, API sessions, and receive workers.
Shutdown closes the listener, cancels active work, aborts owned sessions, waits
for owned workers, and attempts to remove the private launch file and directory.
A disconnected event reader aborts its active translation without stopping the
companion listener. New sessions still require capability and origin checks
([session lifecycle](../../internal/audiolive/server.go)).

## 2. Threat model, trust boundaries, and assumptions

[Internal Go SDK cross-linking](internal-sdk-cross-linking.md) uses a trusted
producer to share private SDK source with ordinary PR checks. Only the producer
can access the main-restricted credential environment; PR jobs receive source
artifacts without App credentials. CI, help, and CodeQL execution jobs also have no repository-read token: an isolated preparation job stages their source through same-run artifacts. Reviewed workflow definitions remain trusted configuration; candidate application code is untrusted. The environment restricts key access, not
the key's App-wide maximum authority, so protecting workflows on main remains
essential.

### Protected assets and objectives

- API and admin credentials, webhook secrets, mTLS private keys, prompts,
  uploaded files, and returned customer data must not be disclosed to an
  unintended recipient or diagnostic sink
  ([generated/commands/cmd.go:82-101](../../generated/commands/cmd.go#L82-L101),
  [internal/debugmiddleware/debug_middleware.go:18-27](../../internal/debugmiddleware/debug_middleware.go#L18-L27)).
- The CLI must preserve the caller's chosen API operation, path/query/header/body
  mapping, destination, and credential type; the remote API is authoritative for
  account authentication, RBAC, tenant isolation, quotas, and rate limits
  ([internal/requestflag/requestflag.go:217-245](../../internal/requestflag/requestflag.go#L217-L245),
  [internal/request/default_options.go](../../internal/request/default_options.go)).
- Local filesystem reads and writes must happen only through intentional CLI
  features, with untrusted stdin unable to turn data strings into file reads
  when the caller enables the documented protection
  ([README.md:156-168](../../README.md#L156-L168),
  [internal/request/file_expand.go](../../internal/request/file_expand.go), [internal/request/file_source.go](../../internal/request/file_source.go)).
- mTLS credentials must be presented only to the explicit same-origin HTTPS
  endpoint, not an HTTPS proxy or cross-origin redirect
  ([internal/request/mtls.go](../../internal/request/mtls.go)).
- Official release integrity, signing/notarization credentials, publication
  tokens, and provenance attestations must remain isolated from untrusted
  pull-request execution and from unverified release inputs
  ([docs/releasing.md:14-29](../releasing.md#L14-L29),
  [.github/workflows/publish-release.yml:17-152](../../.github/workflows/publish-release.yml#L17-L152)).

### Actors and starting capabilities

- A local operator controls the invocation, arguments, environment, working
  directory, local files, stdin, proxy/CA settings, `PATH`, and `PAGER`. If the
  operator already controls these, choosing a malicious pager, backend, or
  executable is not a new privilege granted by the CLI.
- A lower-trust runtime producer may control piped YAML/JSON, filenames,
  response data, API/network data, or wrapper-forwarded arguments without
  controlling the operator's environment or local files. Those inputs remain
  real security boundaries wherever they reach parsing, file access, terminal
  rendering, output paths, request destinations, or credentials.
- An API or custom backend controls response bytes and response metadata but
  does not control the caller's local process, filesystem, terminal, or
  credentials beyond what the CLI intentionally sends.
- A pull-request author controls candidate repository contents. They do not
  control protected release credentials, trusted workflow code from `main`, or
  repository settings unless a workflow crosses that boundary.

### Suggested command insertion

Command insertion accepts only snippets explicitly registered by CLI presenters.
It does not discover commands in API responses, model text, or arbitrary terminal output.
Readable presenters escape external text before adding trusted command links.
Structured and redirected output retain their existing formats.
Navigation suggestions come from displayed command definitions, using conservative shell-safe command names.
Typed help examples render and register together; new command-tree entries need no separate shell catalog.

Persistent keyboard setup requires an explicit install action.
It reuses the profile transaction engine with separate command-insertion markers and cleanup ownership.
Shared profile locks preserve concurrent image-picker and insertion changes.
Managed loaders retain the logical executable path selected during installation, including package-manager symlinks.
Startup requires that PATH still resolves to that installation's current file.
The loader evaluates only successful, complete integration output carrying the supported completion marker.
It never evaluates suggested commands or falls back to another executable.
Insertion-only generation preserves ordinary completion functions and settings.
Binary updates supply new suggestions immediately; a new shell refreshes integration code without reinstalling setup.
Setup errors preserve their causes while presenting fixed messages without private paths.

The optional Kitty adapter sends identifiers through a private local FIFO.
Links contain no command text, arguments, or executable paths.
The adapter validates the opened directory, readiness record, FIFO, and source Kitty window.
The zsh editor independently validates the session owner, generation, editor epoch, context, directory, and command index.
It rejects busy shells and expired links.
Only the active editor changes its input buffer; the adapter never writes terminal input or submits a command.
Ordinary Enter input still follows the shell's execution rules.

The supported click path is an explicitly configured, local Kitty session with zsh.
SSH and terminal multiplexers retain keyboard insertion only.
Click-script generation does not modify shell profiles or terminal configuration.
Persistent keyboard setup does not install the experimental mouse adapter.
Private session files contain identifiers, while the shell holds the command text in memory.
Normal disable and shell exit remove owned session files.
An abrupt process kill can leave a private directory, but its readiness record cannot activate a missing editor.
See [command insertion](../command-insertion.md),
[command registration](../../internal/commandsuggest/suggestions.go),
[Kitty adapter](../../internal/autocomplete/shellscripts/kitty_command_insertion.py), and
[zsh editor integration](../../internal/autocomplete/shellscripts/zsh_command_insertion_click.zsh).

### Repository-code authority

Reviewed, checked-in source, generated code, examples, tests, executable
fixtures, build scripts, mock scripts, and other tracked checkout files have
repository-code authority when an operator or workflow executes or imports them
as code.
A contributor who can change such tracked executable code does not gain a new
privilege merely because tests or CI run it: execution is the expected authority
of the candidate revision. Local wrappers run checkout source
([scripts/run:1-7](../../scripts/run#L1-L7),
[scripts/build:1-8](../../scripts/build#L1-L8)); integration tests run checkout
code ([internal/mocktest/mocktest.go:64-93](../../internal/mocktest/mocktest.go#L64-L93));
ordinary test/build CI intentionally runs candidate scripts/tests with no
repository permissions. A separate read-token job stages source as data and
never executes candidate helpers or local actions
([source preparation](../../.github/workflows/prepare-ci-source.yml),
[CI execution](../../.github/workflows/ci.yml)).

This is not a blanket exclusion. A tracked fixture consumed as bytes by a
parser is still data at that parser boundary. Independently mutable artifacts,
cache entries, downloaded dependencies, runtime/API/network data, or
post-checkout files controlled by a lower-trust actor remain lower-trust input.
Candidate repository code reaching a protected token, signing key,
notarization credential, release environment, or publication action is a real
CI/release boundary. The custom-code workflow illustrates the distinction: the
candidate workflow has read-only permissions, while its write-capable
`workflow_run` handler checks out trusted `main` code and recomputes from Git
objects rather than trusting candidate artifacts
([.github/workflows/castiron-custom-code.yml:30-80](../../.github/workflows/castiron-custom-code.yml#L30-L80),
[.github/workflows/castiron-custom-code-comment.yml:16-98](../../.github/workflows/castiron-custom-code-comment.yml#L16-L98),
[.github/workflows/castiron-custom-code-comment.yml:116-215](../../.github/workflows/castiron-custom-code-comment.yml#L116-L215)).

### Assumptions and known unknowns

- The invoking workstation, shell, environment, and credential files are not
  already compromised. Wrappers that forward lower-trust data into flags,
  stdin, paths, or `--base-url` create a boundary that the wrapper must model.
- `openai-go` owns the normal default endpoint, HTTP transport, retries,
  redirects, and auth-header behavior; this repository owns option wiring and
  the custom mTLS client. The dependency is pinned in
  [go.mod:13](../../go.mod#L13), but this model does not assert uninspected SDK
  behavior.
- GitHub environment protection rules, branch protection, CODEOWNERS
  enforcement, and secret availability are configured outside the repository
  and cannot be proven from this checkout.
- Pinned Actions and downloaded Go/npm dependencies remain supply-chain
  boundaries. This repository's visible controls are commit-SHA pinning,
  checksum/integrity verification, and credential staging
  ([.github/workflows/ci.yml:20-27](../../.github/workflows/ci.yml#L20-L27),
  [scripts/mock:41-44](../../scripts/mock#L41-L44),
  [docs/releasing.md:3-8](../releasing.md#L3-L8)).
- Debug middleware redacts listed sensitive headers and response `Location`,
  and currently dumps metadata without bodies
  ([internal/debugmiddleware/debug_middleware.go:43-63](../../internal/debugmiddleware/debug_middleware.go#L43-L63),
  [internal/debugmiddleware/debug_middleware.go:91-132](../../internal/debugmiddleware/debug_middleware.go#L91-L132)).
  `README.md` conservatively warns that debug includes bodies
  ([README.md:100](../../README.md#L100)); scanners should treat this as a
  documentation/implementation discrepancy, not proof of a vulnerability.

## 3. Attack surface, mitigations, and attacker stories

The scenarios below are hypotheses for review, not validated findings.

| Priority | Scenario and capability gain | Prerequisites | Impact | Existing controls | Mitigation | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| High | A lower-trust wrapper input influences the request destination while credentials are attached, gaining credential exposure to an unintended backend. | A wrapper forwards attacker-controlled `--base-url` or `OPENAI_BASE_URL`; ordinary operator selection alone is not a bypass. | API/admin credential disclosure or misdirected sensitive requests. | Base URLs require HTTPS except for explicit localhost/loopback HTTP; mTLS additionally requires explicit HTTPS and same-origin redirects. | Wrappers should allowlist expected HTTPS origins; preserve SDK redirect/header protections and mTLS origin controls. | [generated/commands/cmd.go:39-45](../../generated/commands/cmd.go#L39-L45), [internal/app/run.go](../../internal/app/run.go), [internal/request/mtls.go](../../internal/request/mtls.go) |
| High | A lower-trust piped string crosses file-expansion logic and gains a local file read/upload. | Automation forwards untrusted YAML/JSON under trusted-stdin semantics and the process can read the target file. | Disclosure of local files to the selected API backend. | Escaped `\@` is literal; stdin is single-use; `OPENAI_UNTRUSTED_STDIN` preserves provenance and rejects stdin-derived binary paths. | Enable untrusted-stdin mode for lower-trust producers and keep explicit file flags operator-controlled. | [internal/request/file_expand.go](../../internal/request/file_expand.go), [internal/request/file_source.go](../../internal/request/file_source.go), [internal/request/stdin_policy.go](../../internal/request/stdin_policy.go), [internal/request/input_values.go](../../internal/request/input_values.go), [internal/request/stdin_reader.go](../../internal/request/stdin_reader.go) |
| High | Candidate or dependency code crosses from a job without protected write/signing credentials into protected publish credentials or official artifacts. | A workflow change, mutable artifact, unverified dependency, or credential staging error defeats current separation. | Compromise of official binaries, signing/notarization material, publication tokens, or attestations. | Release-input generation without protected credentials, tag/SHA binding, fixed artifact verification, protected publish environment, verified GoReleaser, and isolated attestation job. | Preserve the separation and review workflow/dependency/release changes as security-sensitive. | [docs/releasing.md:3-29](../releasing.md#L3-L29), [.github/workflows/publish-release.yml:17-152](../../.github/workflows/publish-release.yml#L17-L152), [.github/workflows/publish-release.yml:247-273](../../.github/workflows/publish-release.yml#L247-L273) |
| Medium | Hostile API/network output reaches terminal, pager, raw pipeline, debug/error logs, or an output path and gains unintended local effect or disclosure. | Response data or metadata reaches a sensitive sink; exploitability depends on selected output format and downstream consumer. | Terminal spoofing, downstream injection, overwrite, or sensitive diagnostic leakage. | Debug redacts known headers and response `Location`; the CLI uses structured rendering and explicit output modes. | Preserve sanitization, avoid shell evaluation downstream, and treat debug/raw output as sensitive. | [internal/debugmiddleware/debug_middleware.go:43-63](../../internal/debugmiddleware/debug_middleware.go#L43-L63), [internal/debugmiddleware/debug_middleware.go:91-132](../../internal/debugmiddleware/debug_middleware.go#L91-L132), [generated/commands/response.go:429-481](../../generated/commands/response.go#L429-L481) |
| Medium | Malformed or very large lower-trust YAML/JSON, file, or response data exhausts local resources or reaches parser edge cases. | A lower-trust producer controls input size/shape and a wrapper invokes the CLI with meaningful availability requirements. | Per-invocation denial of service or parser confusion. | Multipart bodies stream and close owned files; input provenance and single-use stdin avoid some ambiguous reads. | Preserve streaming/cancellation and large-payload compatibility; do not add arbitrary rejection limits. | [internal/request/flag_options.go](../../internal/request/flag_options.go), [internal/app/flagoptions.go](../../internal/app/flagoptions.go), [internal/request/multipart_body.go](../../internal/request/multipart_body.go), [AGENTS.md:100-119](../../AGENTS.md#L100-L119) |
| Low | A user already controlling `PATH`, `PAGER`, proxy/CA settings, shell, or local checkout chooses a malicious executable or endpoint. | Prior local operator/environment control. | Effects within authority the actor already possessed. | Pager execution avoids a shell; mTLS rejects HTTPS proxies. | Treat as deployment hardening unless a separate lower-trust input crosses into that configuration. | [internal/output/pager.go](../../internal/output/pager.go), [internal/output/pager_unix.go](../../internal/output/pager_unix.go), [internal/request/mtls.go](../../internal/request/mtls.go) |
| Not a new capability by itself | A PR author changes and ordinary test/build CI executes a checked-in test, executable fixture, build script, generated source, or mock script. | The changed tracked file is intentionally executed or imported as repository code in a candidate job without protected credentials. | No privilege gain beyond candidate repository-code execution. | Ordinary CI execution has no repository permissions; isolated source preparation uses read-only credentials without executing candidate code. This does not cover fixture bytes crossing a parser, the trusted CodeQL publisher's `security-events: write` boundary, or code reaching protected credentials. | Do not report solely on “PR can change executable checkout content”; instead trace an independently mutable lower-trust input or protected boundary crossing. | [.github/workflows/ci.yml:1-27](../../.github/workflows/ci.yml#L1-L27), [scripts/test:28-61](../../scripts/test#L28-L61), [.github/workflows/codeql.yml:7-47](../../.github/workflows/codeql.yml#L7-L47) |

## 4. Severity calibration

### Critical

Critical findings require broad ecosystem compromise or remote compromise of
ordinary users without unsafe operator configuration. Examples include a
release-pipeline flaw that lets an untrusted contributor publish or attest
arbitrary official binaries, or API-response-driven code execution that steals
admin or mTLS credentials during normal official-API use. A checked-in test or
script executing in its expected candidate job without protected credentials is not critical
merely because a PR author can edit it.

### High

High findings include a realistic redirect, proxy, origin, or request-option
failure that sends credentials to an unintended recipient during normal use; a
lower-trust runtime input that reads or overwrites a sensitive local file; or a
CI/release isolation bypass exposing protected publication/signing authority.
Severity falls when exploitation requires the operator to intentionally select
an attacker endpoint or pass an attacker-selected path.

### Medium

Medium findings include sensitive values leaking through common debug/error
paths, terminal or downstream-pipeline injection from realistic API-controlled
content, or resource exhaustion where lower-trust input reaches a common
automation path. A wrapper that deliberately forwards lower-trust values into
CLI flags can raise severity when it creates a concrete boundary, but the
wrapper's behavior must be evidenced rather than assumed.

### Low

Low findings include malformed local inputs that crash only one invocation,
limited resource leaks, or issues requiring prior control of the operator's
shell, `PATH`, `PAGER`, proxy/CA configuration, custom backend, or local
checkout. Findings whose only premise is that reviewed tracked executable
repository code can be changed by its contributor are unsupported: they show
repository-code authority, not a new security boundary.
