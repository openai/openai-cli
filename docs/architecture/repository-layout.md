# Repository layout

The CLI separates handwritten application code from generated output by directory.
This repository ships the `openai` executable; its Go packages are implementation
details, not a supported library API.

## Ownership

| Path | Ownership and responsibility |
| --- | --- |
| `cmd/openai/` | Handwritten startup, invocation, process exit, and executable tests. |
| `generated/commands/` | Castiron API commands, runtime bindings, and generated tests. |
| `generated/castiron/` | Castiron checker tooling, documentation, tests, and ownership inventory. |
| `generated/tokenizer/` | Unicode tables produced by `scripts/generate-tokenizer-unicode.go`. |
| `internal/app/` | Handwritten assembly, global request/output policy, invocation lifecycle, and ordered feature registration. |
| `internal/commands/` | Command-tree routing, aliases, shortcuts, help groups, and shared feature contracts. |
| `internal/commands/<feature>/` | Handwritten audio, images, files, batches, models, admin, tokenizer, Codex, and manpage workflows. |
| `internal/clihelp/` | Help rendering, command suggestions, and safe recovery guidance. |
| `internal/request/` | Request configuration, authentication, transport, headers, input decoding, and encoding. |
| `internal/input/` | Stdin classification, readiness, and borrowed-file input. |
| `internal/output/` | Rendering, transformations, streaming, paging, and output lifecycle. |
| `internal/terminal/` | Terminal presentation, escaping, process helpers, and platform adapters. |
| `internal/tokenizer/` | Handwritten tokenization engine. |
| `internal/testutil/` | Shared synthetic test fixtures. |
| `tests/cli/`, `tests/terminal/` | Handwritten contracts spanning commands and terminal behavior. |
| `scripts/` | Handwritten developer, build, and CI entrypoints. |
| `docs/`, `api_reference/` | Documentation and the separate API-reference module. |

GitHub requires workflows under `.github/workflows/`. The two Castiron custom-code
workflows remain there with generated headers and ownership-manifest coverage.
Generation provenance remains in `.castiron.stats.yml`; the transformed API input
remains in `api_reference/openapi.transformed.yml`. These fixed-location artifacts are exceptions to using `generated/` for
generated output.

Fix Castiron-owned files through the authoritative schema or compiler and
regenerate. Do not hand-edit generated files or their manifest. The compiler-owned
verifier checks the checkpoint and retired paths; trusted-main CI enforces the
zero custom-code budget. See [the budget guidance](../../generated/castiron/CUSTOM_CODE.md).

## Dependency direction

- The executable supplies the generated command tree to `internal/app.Run`.
- Application assembly connects features and shared policies in the existing order.
- Generated runtime bindings call application policy directly; there is no
  `pkg/custom` forwarding package.
- Feature packages use shared request, output, input, and terminal packages.
- Shared packages do not import application assembly or command features.
- Handwritten implementation packages do not import generated command definitions.
  Generated tokenizer data is available to the handwritten tokenizer engine.

`TestRepositoryLayout` checks retired paths and production imports across all
platform source files. Unit tests stay with their implementation. Handwritten
cross-package tests may exercise application assembly; generated tests stay with
their generated implementation. Use explicit functions and small contracts rather
than a generic plugin framework.

## Compatibility and validation

Preserve command paths and aliases, flags, defaults, environment variables,
configuration and saved-file locations, stdout/stderr, exit status, completion,
cancellation, and platform support. Preserve large JSON and streaming payloads.
Keep the `go install` path, developer script entrypoints, and release artifacts
working. The separate `api_reference/go.mod` module remains separate.

For moves, compile all packages and run affected unit/entrypoint tests and platform
checks. For generator changes, verify ownership and repeat generation without
unexplained drift. Keep signing, CODEOWNER, merge-queue, and trusted-main controls
intact; budget changes require their own human-approved PR.
