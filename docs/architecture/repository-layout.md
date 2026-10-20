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

Single-result formatting, automatic format resolution, GJSON extraction,
raw-string escaping, JSON/YAML/text rendering, and JSON color policy live in
`internal/output/`. Callers retain classification of their final destination,
including wrapped batch output and terminals behind pagers. Feature-specific
selection, interactive exploration, and output lifecycle retain their current
owners. The format list still reaches the generated runtime bridge through a
temporary `pkg/custom.OutputFormats` forwarding value.

The lazy transformed-output iterator and its raw-JSON carrier live in
`internal/output/`. Direct output, lists, streams, and the explorer share the
same transformation-once and item-limit boundary, cancellation checks, stream
failure/completion classifications, and completed-step error snapshots. Assembly
retains feature-specific transformer selection and presentation; local error
presentation uses the shared fixed stream-error type. Unit contracts for read
limits and concurrent explorer error inspection live beside the iterator.

Process stdout streaming and the pager runtime live in `internal/output/`,
including PAGER argument parsing, Unix socket ownership, pipe fallback, child
reaping, and pager-error classification. Feature presentation still chooses when
to page and supplies output generation. Pager implementation contracts live with
the runtime; native and WSL jobs select the moved descriptor test without changing
its cases. The WSL command-contract supplement retains its separate custom test
binary and fixed inventory.

Finite JSON list validation, incremental array framing, terminal prefix paging,
and incomplete-output error classification live in `internal/output/`. Assembly
retains format eligibility, initial error checks, and models selection before
calling the shared writer. Existing public-entrypoint contracts retain finite
versus streaming boundaries, item limits, prompt delivery, malformed-item privacy,
large records, cancellation, and joined failures. The terminal-prefix unit contract
lives beside the shared runtime; native shell inventories remain unchanged.

Readable record iteration, incremental text/audio stream projection, and resource
summary rendering live in `internal/output/`. Assembly supplies the selected
route, extraction/raw options, destination classification, and diagnostic
permission; feature selection and invocation policy retain their owners. Lists
still emit one final summary notice on stderr, and event streams write as they
arrive without preloading or paging. Public-entrypoint tests remain with assembly.

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

Command typo matching and recovery guidance live with help rendering in
`internal/clihelp/`, including the shared display of escaped local invocation
metadata. Error presentation retains diagnostic classification and format policy.
Recovery uses parsed command positions and declared names; it neither echoes
private operands nor executes corrections. Assembly installs the same framework
suggestion hook in its original position.

Request-input help annotations and their tests live in `internal/clihelp/`.
Assembly annotates generated handlers before subgroup and task cloning. Positional
metadata preserves declaration order and authored flag descriptions without
changing request declarations or inventing positional inputs for handwritten commands.

Literal request-header flags and validation live in `internal/request/`. Request
preparation and admin authentication use the same parser, preserving validation
before input reads and credential checks. `pkg/custom.NewRequestHeaderFlag` is a
temporary forwarding function for the generated runtime bridge; remove it through
an authoritative generator update rather than editing generated callers by hand.

mTLS transport construction and its unit tests live in `internal/request/`.
The factory preserves certificate-chain selection, default transport settings,
response-header timeout, proxy handling, same-origin redirects, and value-safe
file errors. Assembly retains flag registration and the setup hook in their
existing positions. Shared synthetic certificate fixtures live in
`internal/testutil/mtls/`; no runtime package imports these fixtures.

Root request inheritance, mTLS flag declarations and lifecycle, and mTLS client
metadata setup live in `internal/request/`. Assembly invokes them in their
existing positions and retains the local-command bypass. Native request-configuration
contracts remain with assembly and call the shared APIs directly, preserving
Windows and WSL selections, test cases, and assertions.

Default client options and base-URL scheme validation live in `internal/request/`.
They preserve CLI metadata, credential and root-project overrides, selected mTLS
transport, and caller-supplied middleware order. Feature middleware composition
stays with request assembly. The generated runtime bridge still reaches the
shared implementation through temporary `pkg/custom/` adapters; remove these
adapters through an authoritative generator update.

Query, header, and final-body serialization live in `internal/request/`.
Assembly supplies the existing image preparation, project validation, JSON
observation, and multipart cleanup-tracking callbacks at their original encoding
points. Literal header overrides remain last; assembly retains upload cleanup
until serialization succeeds. Body-content aliases remain in `pkg/custom/` for
the generated runtime bridge until its producer is updated.

Multipart encoding and owned upload sources live in `internal/request/`, with
streaming, retry/redirect, exact-length, header-validation, and cleanup unit tests.
Request preparation and image/audio settings use the shared upload type while
retaining feature inspection and value-safe input errors. Assembly integration
tests exercise borrowed stdin ownership and aborted image actions through request
options; encoder tests check owned-file cleanup and completion directly.

Stdin provenance and consumer arbitration live in `internal/request/`, together
with request-data alias normalization, explicit file-path markers, and binary
file-input wrapping. Request preparation invokes these policies in its existing
order, before file reads. Unit tests live beside the shared policies; the CLI
contracts for literal untrusted input and trusted explicit overrides remain with
request preparation. File expansion, provenance-aware file reads, and the single-use parameter stdin
reader now live in `internal/request/`, alongside their unit tests. Shared errors
retain declared option/operation metadata and original causes; CLI error presentation
remains with its existing owner. Piped YAML/JSON reading, request-location routing,
explicit-flag merging, and provenance propagation live in `internal/request/`.
Shared flag-to-request preparation, whole-request input eligibility, literal-header
validation, debug middleware setup, required-flag checks, and failure cleanup live
in `internal/request/`. Assembly retains prepared-image reuse and supplies audio
preflight/expansion, admin credential validation, and encoding callbacks in their
existing order. The temporary `pkg/custom.FlagOptions` adapter connects generated
callers to this shared pipeline; feature implementations remain with their owners.

Non-consuming stdin classification and platform readiness probes live in `internal/input/`. They preserve the regular-file size
check, short Unix/Windows probe windows, and caller ownership. Native Windows
probe tests live beside the helper; CI retains their existing cases and flags.

Streaming-mode resolution and its unit matrix live in `internal/request/`.
Request preparation resolves dispatch from actual final-body Booleans at the
existing JSON and multipart call sites, preserving explicit flag precedence and
request bytes. Feature-specific inspection and body construction retain their
current owners.

File upload/get/download route configuration and upload receipts live in
`internal/commands/files/`, with their command, privacy, shell, and rendering tests.
Assembly retains output eligibility and destination classification. Shared shell
quoting and immediate-parent shell detection live in `internal/terminal/`, used by
files, image picker, and audio previews. Safe request options for suggested next
commands live in `internal/request/`; file receipts retain their Go-launcher
traversal policy. Native shell CI selects the moved quoting test unchanged.

Batch wait/download workflows, polling, cancellation, private directory creation,
and output helper processes live in `internal/commands/batches/`. Assembly supplies
request preparation, client middleware, output rendering, and diagnostic policy;
startup dispatches the private helper entrypoints directly. Shared private download
file creation lives in `internal/output/`, and the borrowed terminal writer adapter
lives in `internal/terminal/`. Native download ACL assertions and borrowed socket
fixtures live in `internal/testutil/`, preserving their existing test contracts.

Model-list flags, filter parsing, selection, loading lifecycle, ID/owner tables,
and loaded-response presentation live in `internal/commands/models/`. Assembly
retains eligibility and diagnostic policy, supplies loading feedback, and reconnects
selected records to the normal output pipeline. Selection still admits only the
SDK single-response models pager; cursor pagers remain lazy.

Shared list navigation, page fetching, loaded-page tables and labels, viewport
sizing, worker cleanup, and post-restoration printing live in
`internal/output/listview/`. Assembly supplies rendering and feature-specific
print-hint/interrupt callbacks, so shared output does not import model commands.
Assembly retains the `ShowJSONPages`/`InteractiveListOutput` entrypoints; current
generated commands do not call them. The generated runtime bridge still targets
the remaining request/output adapters in `pkg/custom/`. The guarded terminal writer shared with the JSON explorer
lives in `internal/output/` and retains its original file-close behavior.

Admin credential requirements, setup help, verification, hidden key entry,
and native input/output handling live in `internal/commands/admin/`. Assembly
retains registration order and supplies output eligibility, terminal classification,
diagnostics, and default request options. Request preparation still validates
credentials at its original point. Native Windows executable contracts build the
observer from the feature package; cross-layer verification/error tests retain
actual assembly policies through test-only accessors.

Audio speech controls, local history/inspect/play/export commands, voice creation,
transcript capture, and speech streaming live in `internal/commands/audio/`.
Application assembly preserves registration order, local-command bypass, first-run
setup eligibility, request options, output formatting, and receipt policy. Audio
storage and media processing remain in `internal/audiohistory/` and
`internal/audiomedia/`; the feature does not import assembly or image commands.

Shared download staging, destination verification, cancellation-aware copying,
and process-signal handling live in `internal/output/`, alongside safe error
metadata and receipt failure types. Assembly retains binary response selection
and receipt eligibility. Scalar-upload reading, owned-source cleanup, and replay
live in `internal/request/`; image-specific scalar normalization stays with image
handling. Shared choice labels live in `internal/readable/`. Native Windows
publication and voice validation contracts retain their exact test/subcase
inventories and now verify their owning packages.

Image model discovery, picker/editor state, image request preparation, saving,
stream handling, local previews/preferences, and shell setup mechanics live in
`internal/commands/images/`. Assembly supplies request preparation and presentation
callbacks, retains output-route eligibility, and owns first-run worker admission
and the foreground timeout. Feature metadata and saved-image stream cleanup stay
private to the feature. Cross-layer tests use real assembly callbacks; native
state-persistence checks retain their exact test and subcase inventories.

Delayed loading feedback shared by images and model listing lives in
`internal/output/`. Terminal writer classification lives in `internal/terminal/`
and preserves both file and borrowed-file handling. These shared packages do not
import command features or assembly.

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
| `internal/clihelp/` | Help rendering, local onboarding, command typo matching, and safe recovery guidance. |
| `internal/commands/<feature>/` | Handwritten workflows grouped by feature: audio, images, files, models, admin, tokenize, and codex. |
| `internal/request/` | Shared request configuration, credentials, transport setup, headers, input decoding, and upload encoding. |
| `internal/input/` | Shared non-consuming input probes and borrowed-file reads, preserving cancellation, descriptor flags, deadlines, and caller ownership. |
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

Keep exactly one migration PR open, including drafts, with each candidate
independently buildable. Merge each PR and start the next slice from main rather
than accumulating a stack. Ownership-enforcement PRs #482 and #483 are complete.

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

Help assembly and its contracts live in `internal/app/`: root flag descriptions,
feature help content, flag groups, and admin setup guidance are configured there
in the existing order. Rendering remains in `internal/clihelp/`.

Optional first-run shell setup admission lives in `internal/app/`, alongside
invocation. Its existing terminal checks, synchronous audio command traversal,
argument copy, single-worker limit, and 250 ms foreground wait are unchanged.
Image shell installation mechanics remain in `internal/commands/images/`.
