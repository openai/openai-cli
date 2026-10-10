# Output in scripts and terminals

The CLI uses readable text by default, including pipes and redirected files.
A filename such as `answer.json` does not select JSON.
Select the format explicitly:

```sh
openai models list --format jsonl > models.jsonl
openai responses create --model MODEL --input "Hello" --format json > response.json
```

`--format` supports `auto`, `text`, `explore`, `json`, `jsonl`, `pretty`, `raw`, and `yaml`.
Explicit formats keep their existing data selection and layout.
Pipes and files receive no added ANSI colors, including when FORCE_COLOR or CLICOLOR_FORCE is set.
NO_COLOR disables terminal colors.
Requested raw strings and binary data can contain escape bytes; the CLI preserves those bytes.

## Quiet and verbose

`--quiet` suppresses optional hints, progress, and success receipts.
It preserves selected data, requested help, errors, and exit status.
Saved image paths remain selected data.
Quiet suppresses partial-image progress but preserves explicitly requested final previews.
Quiet does not change the format or discard stdout.
The CLI writes completion receipts to stderr after successful API binary saves or manpage writes.
Quiet and machine error modes suppress these receipts.
Manpage generation reports completion after all selected files finish writing and close.
Disabling both manpage formats writes no files and emits no save receipt.

`--verbose` reports the command, format option, elapsed time, and command result on stderr.
Elapsed time includes argument parsing, request setup, command work, and cleanup.
Command work includes local operations, picker interaction, output, and saving.
It excludes process startup and the final verbose message; it is not API-only latency.
Verbose also reports request-setup failures that occur before the command action.
The format option does not change an existing binary response into text.
Verbose excludes argument values, credentials, URLs, headers, prompts, filenames, and response bodies.
Command completion and elapsed time do not imply that asynchronous API work has finished.
Verbose omits its final details after cancellation, deadlines, or an existing interruption status.
The original action error and exit status remain.
Quiet overrides verbose when both flags are present.
`--debug` remains a separate troubleshooting control.
`-v` still means version. There are no new JSON or raw aliases.

Machine error formats suppress optional verbose prose.
Select text errors explicitly to combine verbose diagnostics with machine stdout:

```sh
openai models list --format jsonl --verbose --format-error text > models.jsonl
openai models list --format jsonl --quiet > models.jsonl
```

Embedded callers use `custom.RunWithOutputPolicy` to report the complete invocation and cleanup.
Direct `Command.Run` calls retain action-scoped reporting and timing.

## Lists and streams

Finite item lists with `--format json` emit one JSON array, including `[]` for an empty list.
This changes the earlier independent-value contract. Use `--format jsonl` when a consumer needs independent records.
`jsonl` emits one compact JSON value per line.
Empty JSONL lists emit no bytes.
`raw` retains the page envelope and its existing pagination behavior.
`yaml` retains its existing independently converted values; it does not add document separators.
JSON extraction and `--raw-output` retain their independent-value behavior.
Single-response list envelopes retain their response objects.

```sh
openai files list --format jsonl --max-items 100 > files.jsonl
openai files list --format json > files.json
openai files list --format json --transform id --raw-output
```

`--max-items -1` remains unlimited.
`--max-items 0` emits `[]` for finite JSON arrays.
Other item formats emit no items; `--format raw` retains the page envelope.
The SDK can still make its initial request.
GJSON extraction applies to each item. Missing paths retain the original item.
`--raw-output` unquotes strings; `--format raw` selects original response data.

Noninteractive event streams emit each event as it arrives.
The CLI does not collect event streams into arrays or send them through an automatic pager.
Explicit terminal explore retains its interactive viewer.
Nonterminal explore retains its JSON fallback.
Piped stdout never opens a pager or picker.
CI does not infer a format or introduce a new output mode.
Explicit explore still opens its viewer when CI has a real terminal.
Quiet suppresses optional feedback; it does not disable explicitly selected interaction or prompts.
Existing CI guards for automatic previews and shell setup remain.

## Failures and saved output

A later page or stream failure leaves earlier output available and returns nonzero.
Failed JSON lists can leave an incomplete array. Check the exit status before consuming saved output.
For an invalid-record error, check the API service or configured proxy that supplied the response.
Confirm the outcome before repeating commands that change remote state.
The CLI preserves source errors when an output sink also fails.
Existing handling of a lone closed stdout pipe remains unchanged.
A broken output sink cannot guarantee a complete output document.

Binary stdout preserves every byte.
Redirecting stderr does not redirect selected stdout data.
Explicit error formats control stderr data, including API error payloads.
Human API errors show returned request IDs, including with `--quiet`.
The CLI omits missing IDs, unusual characters, IDs over 256 bytes, and IDs accompanying a custom `X-Request-ID` request header.
This display bound does not limit API payloads or change structured errors.

Shell redirection opens or truncates its destination before the CLI starts.
The CLI cannot restore a file truncated by `>` after a failed request.
Explicit `--output` files follow the download command's save contract.
