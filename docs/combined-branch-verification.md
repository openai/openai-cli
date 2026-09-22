# Combined CLI branch verification

## Purpose and inputs

`codex/combined-cli-features` combines PR #227 (`b1fe75c3e28d88bee10069891f3793cca2864cfb`)
with the complete PR #226 branch (`9d405351a3a9d3822d4c2db4f735c5c61634671b`).
Both histories remain available. The draft PR targets `codex/inline-image-output`.
This is the complete working version and a source for smaller feature PRs.
Independent features such as help can branch from main; image-dependent work can
branch from #227. The combined draft is not intended to release all features together.

## Reconciliation

- Retained #227's generated commands, dependency versions, terminal-renderer hook,
  native protocols, cancellation, font rollback, per-tab gallery ownership and cleanup.
- Retained #226's help, defaults, image saving, uploads, progress previews, model
  discovery, preview management, readable output and structured-error fixes.
- One configured image workflow owns saving and previewing. Explicit API data or
  URL output cannot fall through to a second renderer. Unconfigured output callers
  retain #227's implicit-auto terminal renderer.
- Legacy image galleries remain usable for existing scrollback; unrelated locked
  or damaged caches do not block a healthy selected gallery. New tabs are isolated.
- Plain preview status reads local cache metadata without Terminal automation.
  Reset checks all galleries for active tabs before clearing owned preview data.
- `main.go` remains identical to #226: lifecycle, completion, help setup, error
  routing and exit status stay visible. No additional entrypoint restructuring.
- Generated sources, workflows, generation metadata, dependencies and budget policy
  are unchanged relative to #227.

## Automated checks (2026-09-22)

Host: macOS arm64, Go 1.27.0. API tests used fake credentials, synthetic images,
owned temporary directories and localhost servers. No live generation requests.
The full API suite used the repository's pinned, checksum-verified Steady source
and Deno runtime with the checked-in OpenAPI specification.

| Area | Evidence |
| --- | --- |
| Welcome, setup, concise/full help, option guides | Entrypoint and help tests; rebuilt executable smoke checks |
| Model IDs, defaults, request precedence, counts and validation | Model, settings and generated-request tests |
| Image generation/edit/variation | Actual executable tests against synthetic HTTP and multipart servers |
| PNG/JPEG/WebP, prompt names, collisions, save failures | Image-output and command tests; originals retained |
| Progress previews and final files | Stream tests including early completion, cancellation and temporary cleanup |
| Preview preferences, setup, status, repair and reset | Command tests and mocked native-service tests |
| Native protocol output | Captured PTY tests; exactly one preview/file, explicit JSON/URL bypass |
| Font preservation, spacing, gallery migration | Font-source checks, mocked Terminal JavaScript and geometry/cache tests |
| Readable resources, audio, binary output, stream status | Entry/process tests, original explicit data formats preserved |
| API/local errors, including malformed SDK responses | Entrypoint regression tests under #227's SDK version |
| Boundaries and dependencies | Architecture tests, vet, module verification and unchanged generated-source check |
| Windows and Linux | All packages and tests cross-compiled; runtime was not exercised |

Fresh full `go test -json -p 1 ./... -count=1 -timeout 30m`: **3,882 tests/subtests passed,
13 skipped, one failed**. All packages except `pkg/cmd` passed. The single failure
is reproduced on untouched #227, as detailed below. The final terminal migration
changes also passed a fresh complete `internal/terminalimage` run.

Additional checks passed: `go vet` over command/custom/transformer/internal/
architecture packages; `go mod verify`; `git diff --check`; and race tests for
`internal/imagegallery`, `internal/terminalimage` and `pkg/transformers`.
The newest legacy-cache regression also passed separately with the race detector.
The six top-level terminal-only output tests skipped by the noninteractive full
run subsequently passed using the compiled test binary in an allocated PTY:
binary-file output, explorer dispatch, output colors, pager control escaping,
pager colors, and interactive raw-output escaping.

## Parity recheck against both PRs

The source audit compared combined commit `1346df4` with the exact PR heads above:

- #226: all 484 original files remain, 458 byte-identical; all 217 original Go
  test files and all 932 named test/fuzz/benchmark functions remain.
- #227: all 391 original files remain, 356 byte-identical. The native renderer
  and font builder are unchanged. Five native-font tests and five geometry tests
  moved with only helper/name changes; their assertions remain identical.
  Gallery-capacity and default-transformer tests were renamed and extended.
- Default piped output and explicit `auto` deliberately follow #226's readable
  output and automatic saving. Explicit JSON/raw/extraction retain the full API
  response. Those two #227 JSON-default assertions were replaced with tests of
  the requested behavior, not claimed as unchanged output.

The audit found one integration regression: legacy and per-tab caches missing
`state.json` could appear absent to status/reset. Cache discovery now includes
remaining font/image artifacts so the existing recovery diagnostic runs. It
keeps their bytes and makes no native font changes. Empty lock tombstones and
unrelated files remain ignored. Regression tests failed before this fix and
passed afterward, including status and reset for both cache layouts.

The full suite above ran before this final discovery fix; the complete terminal
package was checked afterward with the race detector. The six terminal-only
output checks also passed in an allocated PTY. Fresh logs are local at
`/private/tmp/openai-parity-*`. Actual native rendering and foreign-platform
runtime limits below still apply.

## Known inherited failure

`TestFilesCreateCLICancelClosesStalledFIFO` expects EPIPE on the first producer
write after request cancellation. It failed three focused runs on the combined
branch and three on an untouched archive of #227 at the exact base SHA above.
Multipart ownership code and this test are unchanged.

On this macOS/Go combination, a blocked FIFO read can retain the kernel file
descriptor after Close marks the file closed. The API request exits canceled, but
the first subsequent producer write can succeed before the pending read releases
its reference. This is an inherited limitation; the test remains intact. It is
not reported as a passing check or silently excluded from the full suite.

## Limits and remaining manual checks

- Native visual rendering was not verified: the computer-control tool refused
  access to Terminal. PTY byte tests cannot prove how a terminal paints pixels.
- Check Basic, Pro, Clear Dark/Light, Grass, Homebrew and custom font/size/spacing
  profiles in actual Apple Terminal, including old scrollback and multiple tabs.
- Check native iTerm, Kitty, Ghostty, WezTerm and Warp where available. Test cmd.exe,
  PowerShell and Windows Terminal on Windows; cross-compilation is not runtime QA.
- Real model access, service availability and progress timing require live API
  calls. These were deliberately not inferred from mock responses.
- Remaining environment-specific skips include absent Fish, Linux procfs and
  opt-in release-history/preflight fixtures. Release workflows were not dispatched.

## Reproduction

Use the repo's Go caches if configured. Start an owned Steady server from the
reviewed installer on an unused localhost port, then run:

```sh
TEST_API_BASE_URL=http://127.0.0.1:PORT go test -json -p 1 ./... -count=1 -timeout 30m
go vet ./cmd/openai ./pkg/custom ./pkg/transformers ./internal/... ./tests/architecture
go test -race ./internal/imagegallery ./internal/terminalimage ./pkg/transformers
go mod verify
GOOS=windows GOARCH=amd64 go test -p 1 -exec /usr/bin/true ./... -run '^$'
GOOS=linux GOARCH=amd64 go test -p 1 -exec /usr/bin/true ./... -run '^$'
```

Remove live API credentials from the test environment. Stop only the owned mock
process. The cross-compilation commands use a Unix no-op runner and do not execute
the foreign binaries. Detailed local logs are in `/private/tmp/openai-combined-*`;
they are not repository artifacts.
