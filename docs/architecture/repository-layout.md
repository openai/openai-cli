# Repository layout migration

Status: accepted direction; migrate through small, independently buildable PRs.
Paths in the target table are destinations, not a claim that every move is complete.
API command definitions and generated tests now live in `generated/commands/`;
application assembly owns handwritten registration, and handwritten command
contracts live in `tests/cli/`.
The tokenizer Unicode tables live in `generated/tokenizer/`; their producer is
`scripts/generate-tokenizer-unicode.go`, while runtime code remains in
`internal/tokenizer/`.
The browser launcher and its platform/environment isolation tests now live in
`internal/terminal/`; the Codex command retains destination selection, output
ordering, and the launch deadline.

Cancellation-aware output writing and sink/diagnostic error classification live
in `internal/output/`, shared by handwritten commands and presentation code.

Shared color profiles, automatic terminal presentation eligibility, and background
theme detection live in `internal/terminal/`. Feature callers retain terminal
detection for their output destinations, including wrapped batch output.

The shared terminal output worker, frame painter, bounded cleanup, and diagnostic
session isolation live in `internal/terminal/`. Tokenizer, audio workbench, and
image previews use this runtime; its private `tokenizer __output` invocation and
wire protocol remain unchanged. Feature implementations use this shared runtime
without depending on one another.

The complete tokenizer command, live editor, preview worker, and native input
tests live in `internal/commands/tokenize/`. The encoding engine and generated
Unicode tables remain in `internal/tokenizer/` and `generated/tokenizer/`.
Shared borrowed-file reads live in `internal/input/`; frame layout and Windows
console records live in `internal/terminal/`. Native admin and tokenizer tests
share synthetic observer fixtures in `internal/testutil/console/`, without feature
packages depending on one another.

The complete Codex guide and its tests live in `internal/commands/codex/`.
`internal/commands/` owns command-tree shaping (nested API routes, compatibility
aliases, task shortcuts, and help-group metadata), alongside shared local-command
and safe-error contracts. Its tests exercise both tree configuration and actual
application assembly. The existing assembly sequence registers Codex before presentation
and error policies are installed. Tokenizer retains its preceding registration
position, including both private helper commands.

Manpage rendering, flattened documentation trees, text/gzip file cleanup, and
their tests live in `internal/commands/manpages/`. Application assembly supplies
the shared save-receipt policy and configures the feature in its original position.
The hidden `@manpages` entrypoint, generated flag declarations, manual paths,
and existing developer/release artifact-generation commands remain unchanged.

## Product and compatibility contract

This repository ships the `openai` executable. Its Go packages are implementation
details, not a supported library API. Internal imports and exported Go symbols may
change during this migration; do not retain public-package facades for consumers.

Preserve the CLI contract: command paths and compatibility aliases, flags, defaults,
environment variables, configuration and saved-file locations, stdout/stderr,
exit status, completion protocols, cancellation, and supported platforms. Preserve
large JSON bodies and streaming events without introducing new payload limits.
Keep `go install github.com/openai/openai-cli/cmd/openai@latest`, file-based build
entrypoints, developer script entrypoints, and release artifacts working.

## Target responsibilities

| Path | Ownership and responsibility |
| --- | --- |
| `cmd/openai/` | Handwritten executable entrypoint: startup, invocation, process exit. |
| `generated/commands/` | Castiron-owned API command definitions, bindings, and generated tests. |
| `generated/castiron/` | Castiron-owned checker tooling, documentation, and inventory. |
| `generated/tokenizer/` | Unicode tables produced by the tokenizer table generator. |
| `internal/app/` | Handwritten application assembly, invocation lifecycle, global policies, and ordered feature registration. |
| `internal/commands/` | Shared command-tree routing, compatibility aliases, task shortcuts, help-group metadata, and local-feature contracts. |
| `internal/commands/<feature>/` | Handwritten workflows grouped by feature: audio, images, files, models, admin, tokenize, and codex. |
| `internal/request/` | Shared request configuration, credentials, transport setup, headers, input decoding, and upload encoding. |
| `internal/input/` | Shared borrowed-file reads, preserving cancellation, descriptor flags, deadlines, and caller ownership. |
| `internal/output/` | Shared output policy, transformations, formatting, streaming, paging, and error presentation. |
| `internal/terminal/` | Shared completion, terminal rendering, and platform adapters. |
| `internal/tokenizer/` | Tokenization implementation used by the handwritten commands. |
| `internal/testutil/` | Shared synthetic test fixtures and mock helpers. |
| `tests/cli/`, `tests/terminal/` | Executable and terminal contracts that span features. |
| `scripts/` | Stable developer, build, and CI entrypoints. |
| `docs/`, `api_reference/` | Documentation and the existing separate API-reference module. |

Use focused subpackages where they have a distinct responsibility. For example,
`internal/output/transform/` selects and reshapes API responses; readable rendering
is a separate responsibility. Retain cohesive existing helpers until moving them
serves an actual dependency boundary. Avoid new `custom`, `common`, or `utils`
catch-all packages.

Unit tests stay with their implementation. Generated tests stay with generated
code. Move handwritten command tests to public command entrypoints or to the
handwritten implementation they exercise; never place them under `generated/`.

## Dependency direction

- The executable delegates to the application runner.
- Application assembly connects generated API commands, handwritten features,
  and shared policies. It owns registration order.
- Generated commands may depend on shared request/output code, but must not
  install handwritten feature packages themselves.
- Feature packages may use shared request, output, and terminal packages.
- Shared packages must not import application assembly or feature packages.
- Register feature-specific request/output hooks during assembly rather than
  adding dependencies from shared packages back to features.

Use explicit functions and small contracts at these boundaries. Do not introduce
a generic plugin framework or change CLI behavior to simplify a package move.

## Generation ownership

Every file under `generated/` is generator-owned, with its producer recorded.
Handwritten files are forbidden there. Generated files outside that tree require
an explicit integration reason: GitHub workflows must remain in
`.github/workflows/`; generation metadata and the transformed OpenAPI specification
retain their integration paths until their producers and consumers support a move.
Preserve `api_reference/go.mod` as a separate module.

Generated moves must originate in the authoritative generator, be regenerated,
and pass normal snapshot/provenance and ownership verification. Never edit a
manifest or weaken counting/exclusions to make a relocation pass. Moving trusted
CI tooling requires a transition supported by the checker on main before changing
its caller paths. Keep policy-only changes separate from implementation changes.

## Migration batches

Keep at most three migration PRs open, with each candidate independently buildable.
Merge a batch and rebase the next batch on main rather than accumulating a long
stack. Finish ownership-enforcement PRs #482 and #483 before merging layout work.

1. Foundation: this layout/coverage contract; response transformations into
   `internal/output/transform/`; application execution into `internal/app/`.
2. Shared behavior: separate request and output implementations. Temporary
   forwarding functions in `pkg/custom/` keep generated callers buildable and
   are removed when those callers migrate; they are not a public API commitment.
3. Generation boundary: land Castiron support first, then regenerate commands
   into `generated/commands/` and make handwritten assembly own configuration.
4. Features: extract a small local feature first, then move other features in
   cohesive PRs with their tests. Keep audio and image work separately reviewable.
5. Completion: migrate generated tooling/data through their producers, finish
   terminal/test organization, remove `pkg/`, and enforce ownership and import
   boundaries in CI. Keep necessary fixed-location integration exceptions narrow.

## Compatibility evidence

Reuse the existing public-entrypoint tests. Add focused regression coverage for
uncovered behavior changed by a boundary, not tests that mirror file locations.
This inventory is the starting point; an individual PR selects checks relevant
to its change rather than claiming every contract has been exercised.

| Contract | Existing coverage |
| --- | --- |
| Dispatch, empty invocation, completion adapter protocols | `cmd/openai/main_dispatch_test.go`, `completion_experience_test.go` |
| Command aliases, grouping, help, and request equivalence | `cmd/openai/command_subgroups_test.go`, `task_commands_test.go` |
| Request environment overrides and offline help | `cmd/openai/global_flag_environment_test.go`, `request_configuration_test.go`, `request_configuration_bash_test.go` |
| Error channels and wrapped exit codes | `cmd/openai/main_readable_errors_test.go`, `main_api_error_test.go` |
| Machine output, output options, streaming, and early exit | `cmd/openai/deterministic_output_test.go`, `deterministic_stream_test.go`, `finite_json_list_output_test.go` |
| Admin credentials and mTLS | `cmd/openai/main_admin_credentials_test.go`, `main_admin_credentials_mtls_test.go` |
| Uploads, downloads, paths, and saved-image results | `cmd/openai/files_workflow_test.go`, `image_generation_saving_test.go` |
| Large synthetic JSON and streaming payloads | `cmd/openai/image_generation_saving_test.go` and existing package-level payload regression tests |
| Platform-specific terminal, shell, and cancellation contracts | Native-shell and WSL jobs in `.github/workflows/help-compatibility.yml` and OS-specific executable tests |

For package moves, compile all packages, run the moved unit tests and affected
entrypoint contracts, check platform compilation, and verify that generated files
and dependency manifests are untouched. For generator changes, additionally
verify regenerated snapshots and a repeat generation with no unexplained drift.
Keep release, signing, CODEOWNER, merge-queue, and trusted-main controls intact.
