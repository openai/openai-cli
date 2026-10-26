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
Upload and create one batch with one command:

```sh
openai batches submit --file requests.jsonl \
  --endpoint /v1/responses --completion-window 24h
```

`submit` requires a regular local file.
It accepts the same request fields as `create`, except `input_file_id`.
You can provide those fields through flags or piped JSON/YAML.
Stdin remains available for request fields; `--file -` is not supported.
Use `files upload` followed by `batches create` for stdin or custom input-file retention.

The CLI validates local request fields before uploading.
The API performs content and account validation.
Upload and creation each run once, without automatic retries.
If creation fails, the CLI keeps the uploaded file and reports its ID.
Direct HTTP 401/403 responses report an access rejection.
Correct access before retrying the failed operation.
If upload succeeded, reuse its file ID with `create` instead of repeating `submit`.
Transport failures, server failures, and redirected access failures can leave the mutation outcome uncertain.
Check `files list` after an uncertain upload, or `batches list` after uncertain creation.
Reuse the same connection and project settings.
Then use `create --input-file-id` if you confirm that no batch exists.
Structured submission errors include the original error under `error` and recovery data under `batch_submission`.

You can also upload separately:


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

## Wait and save results

Wait for an existing batch and save its available result files:

```sh
openai batches wait --batch-id batch_example --output-dir ./results
```

The command creates `results` if absent. Its parent directory must already exist.
It saves exact bytes as `output.jsonl` and `error.jsonl` when the API returns those file IDs.
An absent file produces no file. Human terminals show which files were saved or absent.
The final stdout response remains the Batch object, including explicit JSON and extraction modes.
Quiet mode suppresses optional progress and receipts.

```sh
openai batches wait batch_example --output-dir ./results --poll-interval 5s --wait-timeout 30m
openai --format json batches wait batch_example --output-dir ./results > batch.json
```

The deadline includes polling, downloads, and final output.
The command saves available files for completed, failed, expired, and cancelled batches.
Failed requests and unsuccessful terminal states still return nonzero after saving available files.
An unknown status prints the response and exits nonzero without downloading.
Neither command creates another batch or cancels remote work.

Existing result paths fail preflight before API requests. Downloads never overwrite another process's files.
A new directory uses private permissions. Each new file uses private permissions where the operating system supports them.
Successful files remain saved if another download fails or local work stops.
Ordinary download failures still allow the other available file to download.
Ctrl+C stops further local work.

After a partial download, retry only the missing file to a new path:

```sh
openai batches download batch_example --file error --output ./results/errors-retry.jsonl
```

Reuse the same connection options. Do not create another batch because waiting or downloading failed.
Omit `--output-dir` to wait without downloading files.
The existing `batches retrieve --wait` interface also remains available.

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

Readable output includes UTC lifecycle dates, the completion window, and metadata.
Completed batches include the duration from creation to completion.
Explicit formats and extraction retain the original timestamps and API fields.

Refresh the selected Batch list every ten seconds:

```sh
openai batches list --watch --limit 100 --max-items -1
```

Each refresh prints another snapshot without clearing terminal history.
Pagination runs serially within each refresh.
Each refresh uses private temporary storage for its records and pagination cursors.
Memory use depends on one API page and one rendered record, instead of the complete Batch history.
Temporary storage must hold the current snapshot and its index.
The command removes this storage before the next refresh, including after failure or handled interruption.
The API does not provide an atomic snapshot across pages.
API failures stop watching with a nonzero exit.
A partial snapshot is marked as incomplete.
Ctrl+C stops local watching without changing remote batches.

Use JSONL for machine consumption:

```sh
openai --format jsonl batches list --watch --poll-interval 5s --watch-count 3
```

Each line contains `object`, `sequence`, `observed_at`, `complete`, and the `data` array.
`--watch-count 0` watches until interrupted.
Watch supports readable output and JSONL.
Omit `--watch` for other formats, extraction, or raw output.

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
On `batches retrieve`, timing flags require `--wait`.

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
A trailing path separator fails before any API request.

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
openai files download --file-id file_example --output results.jsonl
```

Unlike the batch convenience, `files download --output PATH` can overwrite its destination.

## Browse and manage linked files

Use the batch's input, output, or error file ID to inspect file details:

```sh
openai --format json files get file_example
```

The response includes filename, byte size, purpose, creation time, expiration, and any returned status details.
Find existing Batch input files by filename or ID:

```sh
openai batches inputs --search requests --max-items -1
```

Search uses a case-insensitive literal substring.
The command searches subsequent pages until it finds the requested number of matches or reaches the end.
The default limit is 100 matching files; `-1` searches all pages.
Files retain their newest-first order and complete metadata.

Select one file interactively:

```sh
openai batches inputs --select
```

Selection requires terminal stdin and stderr.
The picker returns the selected File metadata without creating a batch.
Use its ID with `batches create --input-file-id`.
For structured stdout, explicitly keep picker errors readable:

```sh
openai --format json --format-error text batches inputs --select
```

Browse a batch's JSONL records without a separate download:

```sh
openai batches results batch_example --file input
openai batches results batch_example --custom-id example-1 --match-input
openai --format explore batches results batch_example --match-input
```

`--file` defaults to `output`; it also accepts `input` and `error`.
`--custom-id` filters by an exact, case-sensitive ID.
`--match-input` groups inputs and selected results by `custom_id`, never by row position.
Each group includes `inputs`, `results`, a `match` status, and any `issues`.
Duplicate IDs remain grouped and visibly ambiguous.
Missing IDs and unmatched records remain visible.
Without matching, records retain their original order.

Default readable results show the request outcome and its meaningful content.
The CLI reports refusals and incomplete answers separately from HTTP success.
Multiple answers, tool calls, annotations, and unfamiliar content remain visible.
`--match-input` also shows the original input instructions and content.
Duplicate IDs do not create artificial input/result pairs.
Missing results refer only to the selected file, not the complete batch.
Inspecting a failed record successfully exits zero; this does not mean the request succeeded.
The default output remains readable when piped.

Use explicit JSON when you need every API field:

```sh
openai --format json batches results batch_example --custom-id example-1 --match-input
```

Readable results omit reviewed configuration fields that repeat request settings.
JSON, JSONL, raw output, and extraction retain the complete selected data.
Large records and unfamiliar structures use a labeled detailed view.
The detailed view preserves complete content without imposing a new size limit.

The response contains a `data` array and supports normal output formats and extraction.
The explorer supports interactive inspection when a terminal is available.
The command reports unavailable files and malformed JSONL without submitting requests again.
Use `batches download` when you need exact file bytes.
Browsing and matching use private temporary storage for records and their index.
The command removes this storage after success, failure, or handled interruption.
Temporary storage must have enough space for the selected records and their index.
Unselected records can temporarily require space while the command validates them.
JSON, JSONL, raw, and readable text output stream without retaining complete payloads in memory.
Readable projections buffer at most 256 KiB per record before switching to streaming detail.
Matching keeps groups on disk and renders their records separately.
Large IDs and object keys also use this storage without imposing size limits.
Extraction, terminal exploration, pretty output, and YAML retain the selected document in memory.
Use `--custom-id` to reduce that document before these operations.

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
Readable Batch errors state the outcome, the supported cause, and the next safe action.
Unknown submission outcomes remain explicit because a disconnected request can still reach the API.
Missing receipt IDs identify which response was incomplete.
Check existing files or batches before repeating a submission.
Connection failures suggest checking the connection, proxy, and `--base-url` setting.
Partial input lists retain received records and identify the incomplete result.
Retry that read-only search with the same connection and project settings.
Explicit machine errors preserve API fields; submission errors add the documented recovery envelope.
Local synthetic tests verify client behavior; they do not establish account entitlements or live server policy.
