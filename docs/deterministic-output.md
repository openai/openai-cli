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
Quiet does not change the format or discard stdout.

`--verbose` reports the command, format option, and command result on stderr.
The format option does not change an existing binary response into text.
It excludes argument values, credentials, URLs, headers, prompts, filenames, and response bodies.
Command completion does not imply that asynchronous API work has finished.
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

## Lists and streams

Lists retain their existing record formats.
`json` emits independent JSON values, not one JSON array.
`jsonl` emits one compact JSON value per line.
Empty structured lists emit no bytes.
`raw` retains the page envelope and its existing pagination behavior.
`yaml` retains its existing independently converted values; it does not add document separators.
Use JSONL when a consumer requires unambiguous record boundaries.

```sh
openai files list --format jsonl --max-items 100 > files.jsonl
openai files list --format json --transform id --raw-output
```

`--max-items -1` remains unlimited.
`--max-items 0` emits no items; the SDK can still make its initial request.
GJSON extraction applies to each item. Missing paths retain the original item.
`--raw-output` unquotes strings; `--format raw` selects original response data.

Noninteractive event streams emit each event as it arrives.
The CLI does not collect event streams into arrays or send them through an automatic pager.
Explicit terminal explore retains its interactive viewer.
Nonterminal explore retains its JSON fallback.
Piped stdout never opens a pager or picker.
CI does not infer a format or introduce a new output mode.

## Failures and saved output

A later page or stream failure leaves earlier output available and returns nonzero.
The CLI preserves source errors when an output sink also fails.
Existing handling of a lone closed stdout pipe remains unchanged.
A broken output sink cannot guarantee a complete output document.

Binary stdout preserves every byte.
Redirecting stderr does not redirect selected stdout data.
Explicit error formats control stderr data, including API error payloads.

Shell redirection opens or truncates its destination before the CLI starts.
The CLI cannot restore a file truncated by `>` after a failed request.
Explicit `--output` files follow the download command's save contract.
