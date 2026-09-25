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
format retain their page envelope; other data formats retain item output.
`explore` keeps its interactive viewer and falls back to JSON off a terminal.

`--transform` and `--raw-output` keep their extraction behavior:

```sh
openai files retrieve --file-id file-example --transform filename --raw-output
```

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
SDK returns as normal results, native audio text/SSE handling, and image
saving/previews are separate changes.
