# Export stored Chat Completions

`openai chat completions export` exports every matching page from the stored Chat Completions API.
Only completions created with `store=true` are available. This command does not export all historical API traffic.

```sh
openai chat completions export --model model-demo --output completions.jsonl
```

The destination must be new. The command never overwrites existing files, directories, or symlinks.
Use `--output -` to write JSONL to stdout. Use `--quiet` to suppress the success receipt.
The compatibility route `openai chat:completions export` behaves identically.

## Filters and pages

The command accepts the existing list controls:

| Flag | Meaning |
| --- | --- |
| `--model MODEL` | Match the model that generated the completion. |
| `--metadata '{"batch":"demo"}'` | Match metadata key-value pairs through the API. |
| `--order asc\|desc` | Request timestamp order. The API defaults to `asc`. |
| `--after ID` | Start after this completion; fetch all following matching pages. |
| `--limit N` | Set the API page size. The API defaults to 20. This is not a total-record limit. |

JSON/YAML stdin, query `@file` references, explicit headers, and normal request configuration follow existing CLI rules.
Explicit flags take precedence over conflicting piped query values.
The command preserves API order. It does not sort, deduplicate, or filter records locally.
It rejects malformed pagination and detected cursor cycles instead of reporting a complete export.

The API does not promise snapshot isolation across pages.
Concurrent creation, deletion, or metadata changes can affect an export.
Date, full-text, and tool searches are unavailable through this command.
This command does not start an asynchronous server export.

## Record schema

Each UTF-8 JSONL line contains one complete object returned by the completion list API.
There is no export envelope or additional field. Empty exports contain zero bytes.

```json
{"id":"chatcmpl_demo","object":"chat.completion","created":0,"model":"model-demo","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"Synthetic result"}}],"metadata":{"batch":"demo"}}
```

This example is synthetic. Actual records can contain more fields.
The export preserves all returned fields, nulls, number precision, arrays, and unknown nested data.
This includes choices, usage, metadata, tool calls, refusals, and annotations when the API returns them.
The command removes only JSON whitespace outside strings and adds a newline after each record.
It does not change string escapes, field order, duplicate keys, or number spelling.
This is a CLI export format, not an import format or a complete backup of account activity.

The export does **not** call the separate messages endpoint.
Assistant messages inside returned `choices` remain present. Original input-message history is not included.
Retrieve that history separately through the existing paginated command:

```sh
openai chat completions messages list --completion-id chatcmpl_demo --format jsonl --max-items -1
```

Existing `list` already supports unlimited pagination and other output modes:

```sh
openai chat completions list --format jsonl --max-items -1
```

Export always writes full JSONL records. It accepts `--format jsonl` and `--format auto`.
It rejects other explicit formats, extraction, and `--raw-output` before sending a request.
Use `list` for those modes. Existing `list --format raw` intentionally returns one API page.

## Files, failures, and receipts

File exports use a private staging file in the destination directory.
Memory usage scales with the current API page, not the total number of pages.
No new body, record, or total-export size limit applies.
The command publishes the destination only after every page and record succeeds.
Publication uses an exclusive hard link where supported.
Other filesystems use an exclusive copy of the completed staging file.
That fallback can expose partial destination bytes during the final local copy.
The command removes partial files it still owns when cleanup succeeds.

Existing destinations remain unchanged, including destinations created by another process during export.
An API, cancellation, or staging-write failure leaves no new destination after successful cleanup.
A final publication, verification, or cleanup failure can leave a file; the error describes that outcome.
Forceful termination can leave a private `.openai-download-*.tmp` staging file.
Inspect such files before removing them.

Stdout is incremental. Failure can leave complete prior lines and a partial final line.
The process returns a nonzero status after incomplete output.
Only newline-terminated records count as complete.
Check the exit status before using an export as complete data.
Use a managed `--output PATH` when failed requests must leave no destination.

After successful file publication and cleanup, stderr receives:

```text
Saved completions.jsonl
Stored completions: 1
All pages fetched.
```

Stdout exports omit the `Saved` line. Zero-record exports report `Stored completions: 0`.
Quiet mode and structured error modes suppress optional receipts under the normal CLI output policy.
Structured API errors retain the existing API payload and error extraction behavior.
Readable failures include the completed-record count and destination outcome.
A receipt failure returns a nonzero status but preserves completed exported data.

## Public API references

- [List stored Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/list)
- [List stored completion messages](https://developers.openai.com/api/reference/resources/chat/subresources/completions/subresources/messages/methods/list)
