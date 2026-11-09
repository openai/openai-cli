# Reading command results

Single API objects print as labeled text in terminals, pipes, and redirected
files. In interactive terminals, lists show tables when the data fits. Select
`--format text` to print all requested records as labeled text.

```sh
openai files retrieve --file-id file-example
openai files retrieve --file-id file-example | cat
```

Objects show labeled fields. Nested values stay grouped. In a terminal, list
commands show a table below your command when the columns fit. Wider or nested
records use labeled fields. Pipes, explicit `--format text`, and live streams
print individual records as they arrive. An empty list shows `No results.`
and `--max-items 0` writes nothing.

## Browsing a list

Standard list commands now browse by default when both input and output are terminals.
In v1.51.5, only Files, Batches, Models, and organization Projects browsed by default.
Other list commands, including `openai webhooks list` and `openai skills list`, printed all requested rows and returned.
Use `--pager` to open the full-screen viewer immediately.

This is a breaking change for terminal-attached scripts and shell chains.
If more results remain, the list waits for keyboard input before the next command runs.
Use `--browse=false` to print all requested rows into scrollback and continue automatically.

```sh
openai skills list --browse=false && echo "Skills listed"
```

Pipes, redirected output, and runs with a nonempty `CI` environment variable still paginate without keyboard input.

The preview shows as many loaded rows as fit in your terminal. A wrapped record can use several lines.
Complete results that fit return to the shell. Longer results show a footer, such as `Files 1–38`.

| Key | Action |
| --- | --- |
| ↓ or ↑ | Scroll one row. |
| Space | Open the full-screen view at the next unread row, then advance one screen at a time. |
| b | Return to the previous screen. |
| i | Leave the full-screen viewer, print all loaded rows in order, and continue in the terminal. |
| q | Quit. |

When browsing, the CLI requests one screen of rows wherever the API supports page sizing.
The CLI reserves room for the heading, controls, and any visible hint.
Wrapped records can take multiple lines. The footer counts the records actually visible.
API page limits and the remaining `--max-items` count can shorten a request.
An explicit `--limit` sets the API page size. Resizing changes the next automatic request.
Single-response catalogs, such as webhook event types, use the same controls over the rows already loaded.
Models also pages locally with a filter, sort override, or finite `--max-items`.

The browser waits after the first API page when more results remain.
Space shows unread loaded rows without making a request.
At the last loaded row, Space requests one API page if more results remain.
A short page waits for another key, even when more rows would fit on screen.
Moving back uses cached rows. Pressing q cancels a pending fetch.
The full-screen viewer keeps headings visible and waits on the final screen.
Its footer shows the visible record range. A known total appears after a slash, such as `26–50/1000`.
When the API omits a total, a complete traversal establishes it.
Starting from a cursor or reaching an item cap can leave the total unknown.
The CLI does not fetch ahead to count records.
For filtered Models, a known total counts matching models before the item cap.
After `i`, compatible pages share one heading and aligned columns in your terminal history.
When columns change or a complete ID cannot fit, the CLI uses labeled records.
Space continues inline. The final inline page returns to the shell automatically.
Use `--format json` for the full API fields.

The CLI discovers `list`, `ls`, and `list-*` actions during registration, including nested commands and aliases.
New list actions using the standard response or iterator output inherit this default automatically.

Ordinary text and unknown fields remain complete. Known base64 media fields and
numeric embedding vectors show a size or count with a `--format json` hint.
Readable text escapes terminal controls and directional characters, including
when redirected. This escaping does not redact secrets from API data.

## Scripts and complete API data

The default for pipes has changed from JSON to readable text. Scripts that parse
responses must select their format explicitly:

```sh
openai --format json files retrieve --file-id file-example
openai --format jsonl models list | jq '.id'
```

Explicit `json`, `jsonl`, `raw`, `yaml`, `pretty`, and `explore` preserve access
to the original API data. Format names are case-insensitive. Lists in `raw`
format retain their page envelope. In `json` format, lists that normally print
separate items now produce one JSON array. Other data formats retain item output.
Commands returning a single response object keep that object, including list
envelopes such as `webhooks:event-types list`.
`explore` keeps its interactive viewer and falls back to JSON off a terminal.

`--transform` and `--raw-output` keep their extraction behavior:

```sh
openai files retrieve --file-id file-example --transform filename --raw-output
```

### Saving finite lists as JSON

```sh
openai files list --format json > files.json
openai files list --format jsonl > files.jsonl
```

`files.json` contains one array, including `[]` for an empty list or
`--max-items 0`. `--max-items -1` retains unlimited pagination.
On lists with both flags, `--limit` sets the API page size and `--max-items` caps total listed items.
For example, `openai files list --limit 10 --max-items 25 --format json` lists up to 25 files across pages.
With `openai files list --format raw`, the CLI returns the first API page as received.
Pipes and redirected files receive each item before the CLI requests more items.
The CLI does not collect the entire list. The SDK still loads individual pages.
Models sorting retains its existing single-response collection.

This changes `json` output at finite item boundaries, which previously emitted
separate pretty-printed values. Migrate record consumers to `--format jsonl`.
Alternatively, update document consumers to read array elements, such as `jq '.[]'`.
Single responses and event streams retain their existing JSON framing.
Explicit `--transform` or `--raw-output` retains separate-record behavior.
`explore` fallback also retains separate records.

The CLI writes the closing bracket only after iteration succeeds. An upstream failure,
cancellation, malformed item, or ordinary write failure returns a nonzero status.
Already written bytes remain and may contain incomplete JSON. An initial API failure
writes no payload. A closed consumer stops pagination after the write fails.
The CLI retains any upstream error already observed alongside that write failure.
Even valid JSON can accompany a final write failure. Always check the exit status.

The existing closed-pipe convention still applies: a consumer that closes stdout
early can end the command successfully. This does not promise a complete document
for that consumer. A joined upstream error or cancellation still returns nonzero.

Check the exit status before using the saved document. Shell redirection opens
or truncates the destination before the CLI runs. The CLI cannot undo that action
or replace a redirected file atomically. A `.json` filename does not select JSON;
always pass `--format json`.

Binary downloads keep their byte or file behavior. Errors use readable summaries
on stderr unless a data format is selected. `--format-error` overrides the error
format independently; see [error output](../README.md#usage) for precedence and
structured error details.

If the SDK returns an error event after a stream starts, stderr explains that
output may be incomplete. Explicit error formats preserve the event JSON;
`--transform-error error.code` extracts its error code. Already printed output
stays on stdout and the command exits unsuccessfully.

## Scope of this change

This is the generic presentation foundation extracted from PR #238. Responses
and Chat Completions still show their fields, rather than only the generated
text. Resource-specific list/get summaries, empty-response confirmations,
streamed-text assembly and deduplication, classification of failure events the
SDK returns as normal results, and image saving/previews are separate changes.
Audio text, subtitle and SSE handling is described in
[reading audio results](readable-audio.md).
