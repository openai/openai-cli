# Reading command results

Successful JSON responses now print labeled text by default, both in a terminal
and when stdout is piped or redirected. `--format auto` and `--format text`
select this behavior explicitly. For example:

```sh
openai files retrieve --file-id file-example
openai files retrieve --file-id file-example | cat
```

Objects show labeled fields, nested values stay grouped, and list items and
stream events appear as separate records. Readable lists and streams print as
records arrive without opening an automatic pager. An empty list says
`No results.`; `--max-items 0` writes nothing.

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
Pipes and redirected files receive each item before the CLI requests more items.
The CLI does not collect the entire list. The SDK still loads individual pages.
Models sorting retains its existing single-response collection.
Terminal output retains automatic paging for long results. The CLI buffers at
most one screen plus the item that crosses that screen before opening the pager.

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
