# Batch requests

Prepare requests locally, upload the file, then create a batch.
Waiting and downloading use existing Batches and Files APIs.

## Prepare and submit

Each JSONL line contains one request with a unique `custom_id`.
Use the same model throughout one input file.
Use a model supported by your account and the selected endpoint.
This example uses two synthetic requests:

```jsonl
{"custom_id":"example-1","method":"POST","url":"/v1/responses","body":{"model":"gpt-4.1-mini","input":"Return the word blue."}}
{"custom_id":"example-2","method":"POST","url":"/v1/responses","body":{"model":"gpt-4.1-mini","input":"Return the word green."}}
```

Save these lines as `requests.jsonl`.
Upload the file with purpose `batch`:

```sh
openai files upload --file requests.jsonl --purpose batch
```

Use the returned file ID when creating the batch:

```sh
openai batches create \
  --input-file-id file_example \
  --endpoint /v1/responses \
  --completion-window 24h \
  --metadata '{"job":"example"}' \
  --output-expires-after.anchor created_at \
  --output-expires-after.seconds 86400
```

The expiration example keeps output and error files for one day after each file's creation.
Omit expiration flags to use the service default.
Create also accepts `--output-expires-after '{"anchor":"created_at","seconds":86400}'`.
Omission does not promise indefinite retention.
The [Batch guide](https://developers.openai.com/api/docs/guides/batch) documents automatic output deletion after 30 days.
Inspect the file's `expires_at` value for its returned expiration timestamp.

Input files have a separate retention policy.
[Files uploads](https://developers.openai.com/api/reference/resources/files/methods/create) with purpose `batch` expire after 30 days by default.
For example, keep an input file for seven days:

```sh
openai files upload --file requests.jsonl --purpose batch \
  --expires-after.anchor created_at --expires-after.seconds 604800
```

Current [Batch creation limits](https://developers.openai.com/api/reference/resources/batches/methods/create) include 50,000 requests and 200 MB per input file.
The completion window is `24h`.
Output expiration accepts 3,600 through 2,592,000 seconds, anchored at `created_at`.
The CLI forwards fields without adding endpoint or model restrictions.

The create reference includes `/v1/videos`.
However, the [deprecation notice](https://developers.openai.com/api/docs/deprecations#2026-03-24-sora-2-video-generation-models-and-videos-api) lists September 24, 2026 as its shutdown date.
This documentation conflict does not establish current Videos availability.

## Inspect and wait

Existing commands retrieve once or list batches:

```sh
openai batches list --limit 20 --max-items 20
openai batches retrieve --batch-id batch_example
openai --format json batches retrieve batch_example
```

Explicit JSON includes creation time, completion window, lifecycle timestamps, metadata, and complete errors.
The default readable view summarizes those fields.
For example, extract the exact ID without copying labels:

```sh
openai --transform id --raw-output batches retrieve batch_example
```

The following optional view recipes use `jq` or Python 3.
Neither tool is a CLI dependency.
In Bash or zsh scripts, use `set -o pipefail` to retain failures from upstream commands.

Show chronological lifecycle events, retaining Unix timestamps:

```sh
openai --format json batches retrieve batch_example |
  jq 'to_entries | map(select(.key != "expires_at" and (.key | endswith("_at")) and (.value | type == "number"))) | sort_by(.value)'
```

This includes returned lifecycle timestamps and excludes the expiration deadline.
Show elapsed completion time in seconds, or null before completion:

```sh
openai --format json batches retrieve batch_example |
  jq 'if (.completed_at | type) == "number" then .completed_at - .created_at else null end'
```

Add `--wait` to poll until the batch reaches a terminal status:

```sh
openai batches retrieve batch_example --wait
openai batches retrieve batch_example --wait --poll-interval 5s --wait-timeout 30m
```

The default polling interval is 10 seconds after each successful check.
The default wait timeout is zero, meaning no overall deadline.
A positive timeout includes requests, retries, and polling delays.
It does not change the SDK's individual request timeout behavior.
The CLI retains SDK retries and supported `Retry-After` handling; exhausted retries stop waiting.
Malformed batch JSON stops local waiting with an error.
Timing flags require `--wait`.

Human terminal progress uses returned counts:

```text
Processing: 420 of 1000 requests finished.
Completed: 995 succeeded, 5 failed.
```

These numbers illustrate output; actual counts come from the API.
Finished requests include successes and failures.
The final response retains the existing readable fields.
Explicit formats retain complete API data:

```sh
openai --format json batches retrieve batch_example --wait > batch.json
openai --transform status --raw-output batches retrieve batch_example --wait
```

Progress and create hints use stderr only with human output and terminal stdout/stderr.
Pipes, extraction, and machine formats for either results or errors receive no progress or hints.
A successful create can show a follow-up command containing its returned batch ID.
Explicit connection or authentication overrides instead show a reminder to reuse those options.
The CLI never copies private option values into a hint.
Interruption during final output can leave partial data on stdout.

Wait continues through `validating`, `in_progress`, `finalizing`, and `cancelling`.
It stops at `completed`, `failed`, `expired`, or `cancelled`.
An unknown future status prints the response and exits nonzero.

| Wait result | Exit status |
| --- | --- |
| Completed with consistent counts and zero request failures | 0 |
| Request failures, failed, expired, cancelled, unknown status, or missing/inconsistent counts | 1 |
| API or transport error | 1 |
| Overall local wait timeout | 124 |
| SIGINT while waiting or writing output | 130 |
| SIGTERM during local batch work | 143 |

Ordinary retrieve retains its existing HTTP-success exit behavior, regardless of batch status.
`--wait=false` also retains ordinary retrieval.
Ctrl+C and SIGTERM stop local work. Neither cancels the remote batch.
Inside the interactive explorer, `q` or keyboard Ctrl+C closes the view and retains the batch outcome.
The wait deadline also stops the explorer.
Terminal restoration gets up to 200 ms of cleanup time.
Cancellation diagnostics use a separate 250 ms delivery window.
A fallback diagnostic, if needed, has its own 250 ms window.
For canceled batch work, `--format-error explore` emits static JSON instead of opening another view.
Cancellation can omit its diagnostic when the interrupted output destination also carries errors.
The exit status still distinguishes interruption from timeout.

## Cancel remote work

Remote cancellation requires an explicit command:

```sh
openai batches cancel --batch-id batch_example
```

The service can remain in `cancelling` for up to ten minutes.
Cancelled and expired work can still have partial output or error files.
See the [Batch guide](https://developers.openai.com/api/docs/guides/batch) for lifecycle behavior.

## Download files

Choose a file path without a trailing path separator.
Directory-like destinations fail before an API request.

Download one selected batch file to an explicit destination:

```sh
openai batches download batch_example --output results.jsonl
openai batches download batch_example --file error --output errors.jsonl
openai batches download batch_example --file input --output original.jsonl
```

`--file` defaults to `output`; it also accepts `error` and `input`.
The command retrieves the batch once, then downloads its selected file through Files.
It works whenever that file ID exists, including partial results.
An absent file ID or unavailable remote file returns nonzero.
The command does not wait automatically.

Downloads preserve JSONL bytes and ordering exactly.
Result order can differ from input order; match records using `custom_id`.
The command never opens results or resubmits failed requests.

Existing destinations remain unchanged, including symlinks.
File downloads use private temporary files and publish only complete content without replacing another file.
Interrupted or failed downloads remove their owned temporary file.
A filesystem without atomic no-replace support fails safely.
Previously completed downloads remain intact.

Use `--output -` for an exact stream:

```sh
openai batches download batch_example --output - | jq -c '.custom_id'
```

Interrupted stdout can contain partial data.
Output formats affect saved-file receipts and errors, never file bytes.
Use the existing Files command when you already know the file ID:

```sh
openai files content --file-id file_example --output results.jsonl
```

Unlike the batch convenience, existing `files content --output PATH` overwrites its destination.

## Browse and manage linked files

Use the batch's input, output, or error file ID to inspect file details:

```sh
openai --format json files retrieve file_example
```

The response includes filename, byte size, purpose, creation time, expiration, and any returned status details.
Browse existing files before selecting an input ID:

```sh
openai --format json files list --order desc --max-items -1
```

Add `--purpose batch` to select uploaded batch inputs.
JSON list output emits successive records; use `jq -s` when you need one array.
Search filenames with a case-insensitive literal substring:

```sh
openai --format jsonl files list --order desc --max-items -1 |
  python3 -c '
import json, sys
needle = sys.argv[1].strip().lower()
for line in sys.stdin:
    item = json.loads(line)
    if needle in (item.get("filename") or "").lower():
        print(json.dumps(item, ensure_ascii=False))
' 'requests'
```

This streams local filtering across all returned pages, including Unicode filenames.
It preserves the server's newest-first ordering.

Files also provides explicit deletion:

```sh
openai files delete file_example
```

Deletion removes the file, including its use in vector stores.
File deletion and batch cancellation are separate operations.

## Access and safety

Use a supported API key with the required project access.
Existing organization, project, custom header, base URL, and mTLS options also apply to waiting and downloading.
The CLI does not enforce dashboard permissions locally.

Public [RBAC documentation](https://developers.openai.com/api/docs/guides/rbac#batch-permission-implications) defines `api.batch.read` and `api.batch.write`.
Batch permissions imply relevant Files permissions.
The server checks both API key permissions and the caller's project role.
Admin keys do not authorize ordinary Batch operations.

Select regional endpoints according to your project's approved [data controls](https://developers.openai.com/api/docs/guides/your-data).
Batches and Files are not eligible for Zero Data Retention.
Availability, model access, retention, and residency remain server-side policies.

Authentication, permission, validation, conflict, and transport errors retain the CLI's normal error presentation.
Local synthetic tests verify client behavior; they do not establish account entitlements or live server policy.
