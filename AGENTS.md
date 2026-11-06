# Repository guidance

Before changing code, read and follow [CONTRIBUTING.md](CONTRIBUTING.md) and
[SECURITY.md](SECURITY.md). In particular:

- [Project structure](CONTRIBUTING.md#project-structure),
  [generated and handwritten code](CONTRIBUTING.md#generated-and-handwritten-code),
  and [repository layout](docs/architecture/repository-layout.md): package ownership,
  dependency direction, command compatibility, assembly order, safe help recovery,
  and the separate `api_reference/go.mod` module. Reuse existing packages; explain
  any new package's distinct responsibility.
- [Security requirements](CONTRIBUTING.md#security-requirements): credential handling,
  synthetic tests, diagnostic redaction, dependency/install review, the existing
  hourly SDK updater, workflow permissions, signing/publication boundaries, and
  required SDK CODEOWNER review. Review response-header logging independently;
  sanitize `--debug` output before sharing. Never print or upload live secrets or
  customer data. Keep `id-token: write` and `attestations: write` limited to the
  attestation job.
- [Formatting and releases](CONTRIBUTING.md#formatting-and-releases): authorization
  requirements for publishing, release dispatch, and repository settings.
- [Security model](docs/architecture/security-model.md): trust boundaries. Treat
  base URLs, redirects, proxies, TLS/mTLS, CLI arguments, `@file` and escaped `@`,
  filenames, stdin, uploads, YAML/JSON, pagination, and streaming as boundaries.
  Preserve credential isolation, same-origin mTLS redirects, HTTPS-proxy
  restrictions, streaming, cancellation, and owned-file cleanup.

## Generated-file ownership

Establish ownership before editing: `File generated` headers, `.castiron.stats.yml`, and
`api_reference/openapi.transformed.yml` identify generated commands and inputs.
Never hand-edit generated files, including tests, documentation, workflows, or
inventory hashes. Fix the authoritative schema, configuration, or compiler and
regenerate; put SDK-specific behavior in separately owned handwritten helpers.

Regeneration replaces owned output and deletes retired paths; handwritten patches
are never replayed or merged. Preserve separately owned handwritten files and
resolve ownership collisions explicitly. Review output and inventory changes
together. Enforcement relies on contributor guidance and code review, not a
dedicated generated-file edit gate. See [ownership details](CONTRIBUTING.md#generated-file-ownership).

## Focused validation

Follow [Running tests](CONTRIBUTING.md#running-tests); choose the smallest checks
covering the change. Available local checks:

```sh
go test ./internal/...
go test ./... -run '^$'
go mod verify
./scripts/lint
```

`./scripts/test` starts the source-based local mock server: review its dependencies
first and use synthetic data. Avoid unrelated module, generated-source, or
formatting changes; report exactly which checks ran.

## Large-payload compatibility

Preserve large payloads supported by the last few releases, including Responses,
Chat Completions, other JSON bodies, and streaming events. Size alone does not
make input malformed or hostile. Do not add arbitrary body/event/line limits or
tighten existing limits for security or efficiency. Prefer incremental processing,
amortized-linear buffering, timely cleanup, and caller cancellation. New rejection
limits require an explicit owner-approved API contract and review of previously
supported payloads and transports; longstanding transport limits still apply.

Use focused, deterministic public-entrypoint tests with large synthetic payloads
generated in memory, not committed captures or live image generation. High memory
use is intentional: do not shrink payloads or raise client limits to accommodate
new caps. Cover the main JSON and streaming categories; run large cases
sequentially to bound peak memory. Respect longstanding transport limits when
choosing fixtures; fixture size is a regression probe, not a new API maximum.
