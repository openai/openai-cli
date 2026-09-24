# Reading command results

Successful JSON responses now print labeled text by default, both in a terminal
and when stdout is piped or redirected. `--format auto` and `--format text`
select this behavior explicitly. For example:

```sh
openai files retrieve file-example
openai files retrieve file-example | cat
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
openai --format json files retrieve file-example
openai --format jsonl models list | jq '.id'
```

Explicit `json`, `jsonl`, `raw`, `yaml`, `pretty`, and `explore` preserve access
to the original API data. Format names are case-insensitive. Lists in `raw`
format retain their page envelope; other data formats retain item output.
`explore` keeps its interactive viewer and falls back to JSON off a terminal.

`--transform` and `--raw-output` keep their extraction behavior:

```sh
openai files retrieve file-example --transform filename --raw-output
```

Binary downloads keep their byte or file behavior. API error details go to
stderr with the existing HTTP summary and default JSON formatting. Readable
success output does not enable a new error presenter.

## Scope of this change

This is the generic presentation foundation extracted from PR #238. Responses
and Chat Completions still show their fields, rather than only the generated
text. Resource-specific list/get summaries, empty-response confirmations,
streamed-text assembly and deduplication, stream failure classification, native
audio text/SSE handling, and image saving/previews are separate changes.
