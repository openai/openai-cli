# Repository Guidance

Read [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md) before
making changes.

## Generated CLI sources

Most files in `pkg/cmd/` are generated from the OpenAPI specification by
Castiron. Generated files identify themselves with a `File generated` header;
`.castiron.stats.yml` and `api_reference/openapi.transformed.yml` record the
generation inputs. Establish whether the generator owns a file before editing
it. Fix recurring generated defects in the authoritative schema or generator
when available instead of introducing broad manual changes.

The CLI entrypoint, `internal/` packages, and selected files such as
`pkg/cmd/flagoptions.go` and `pkg/cmd/mtls.go` contain handwritten behavior.
Keep changes focused and preserve command compatibility, generated ownership,
and the intentionally separate `api_reference/go.mod` module.

## Security requirements

- Never commit, print, or upload live API keys, admin keys, access tokens,
  webhook secrets, private keys, signing credentials, `.env` files, or
  customer data. Read `OPENAI_API_KEY`, `OPENAI_ADMIN_KEY`, and
  `OPENAI_WEBHOOK_SECRET` from the environment. Keep mutual-TLS private keys in
  permission-restricted files referenced by `OPENAI_MTLS_CLIENT_KEY_FILE`;
  never place key material in arguments, fixtures, logs, or environment values.
- Use clearly fake credentials, synthetic payloads, `t.Setenv`, `httptest`, or
  the reviewed local mock server in examples and tests. Avoid live API calls,
  production data, and credentials in shell history, process arguments, test
  recordings, snapshots, or CI artifacts.
- Redact `Authorization`, `Api-Key`, `X-Api-Key`, `Cookie`, `Set-Cookie`,
  signed URLs, credential-bearing query parameters, prompts, uploaded files,
  and sensitive request or response bodies from logs, errors, traces, and
  shared diagnostics. Preserve existing request-header filtering and review
  response-header logging independently; never assume `--debug` output is safe
  to share without sanitization.
- Treat base URLs, redirects, proxies, TLS and mutual-TLS configuration, CLI
  arguments, `@file` references, escaped `@` values, filenames, stdin, uploads,
  YAML/JSON input, pagination, and streaming as security boundaries. Preserve
  credential isolation, same-origin mutual-TLS redirects, HTTPS-proxy
  restrictions, streaming where present, cancellation, and owned-file cleanup.
  Apply appropriate resource limits when changing paths that buffer input.
- Review direct and transitive Go dependencies, `go.mod`, `go.sum`,
  `api_reference/go.mod`, module sources, `replace` directives, and checksum
  verification. Review `scripts/bootstrap`, `scripts/mock`, the pinned
  `@stdy/cli` npm mock dependency, and any install hooks before execution.
  Preserve the existing hourly `github.com/openai/openai-go/v3` updater and do
  not introduce competing dependency or release automation.
- Pin third-party GitHub Actions to reviewed full commit SHAs. Minimize
  per-job permissions, default to `persist-credentials: false`, and preserve
  the narrowly scoped authenticated checkout required by the existing hourly
  SDK updater. Never expose GitHub App private keys, release tokens, Homebrew
  credentials, macOS signing/notarization material, or OIDC credentials to
  untrusted code, pull requests, logs, or artifacts.
- Preserve protected `release` and `publish` environments, validation that a
  release tag belongs to `main`, GoReleaser artifact integrity, Homebrew
  publication boundaries, and release provenance attestations. Keep
  `id-token: write` and `attestations: write` limited to the attestation job;
  do not publish, dispatch releases, or alter repository settings without
  explicit authorization.
- Require SDK CODEOWNER review for changes to authentication, admin endpoints,
  URL validation, redirects, proxy/TLS/mTLS behavior, file paths or uploads,
  request parsing, debug logging, dependency installation, generated code,
  workflows, signing, or release artifacts. Add focused regression or security
  tests when behavior changes; otherwise use the smallest appropriate
  structural, workflow, dependency, generated-output, or artifact validation.
  Exercise malformed or attacker-controlled inputs where appropriate.
- Report suspected vulnerabilities privately through [SECURITY.md](SECURITY.md)
  and `disclosure@openai.com`. Never disclose vulnerability details in public
  GitHub issues, pull requests, or discussions.

## Focused validation

Choose the smallest checks that cover the change. Safe local checks include:

```sh
go test ./internal/...
go test ./... -run '^$'
go mod verify
./scripts/lint
```

The complete `./scripts/test` suite starts the npm-backed local mock server;
review that dependency and use synthetic data before running it. Avoid
unrelated `go.mod`/`go.sum`, generated-source, or formatting changes, and
report exactly which checks ran.
