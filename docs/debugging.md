# HTTP request timing

Use the existing `--debug` flag to inspect HTTP headers and timing on stderr:

```sh
openai --debug models list
```

Each HTTP attempt reports when response headers arrive. Retries and additional pages receive separate attempt numbers.
Numbers increase within the request logger used by the command.
An attempt covers one SDK dispatch. Redirected HTTP hops share that attempt number and contribute to its headers latency.
For example, measured output can look like this:

```text
HTTP attempt 1: response headers received after 184 ms (200 OK)
HTTP attempt 1: first response data read after 185 ms
HTTP attempt 1: response body fully consumed after 186 ms
```

These numbers are illustrative. The CLI measures each duration with a monotonic clock, starting immediately before dispatch.
Retry delays before that attempt are excluded.

- **Response headers received** measures the wait for headers. It does not measure total request time.
- **First response data read** measures the first nonempty body read. It does not identify the first parsed streaming event.
- **Response body fully consumed** means the reader reached EOF. It does not confirm application success or asynchronous job completion.
- **Response body closed before EOF** means the caller stopped reading. This can occur during retries or early stream closure.
- **Read, close, transport, or cancellation diagnostics** identify interrupted attempts without printing raw error details.

Body durations include time between caller reads. They can include parsing, output backpressure, and pauses in the consumer.
Empty bodies have no first-data diagnostic. The CLI never reads ahead to collect timing.
Streaming output continues as data arrives. The caller retains responsibility for closing the response body.

Binary-response commands record body timings during reads and report them after body closure and consumer cleanup.
This includes downloads and speech responses. Their elapsed durations measure the original reads, not the later reporting time.
This ordering lets managed downloads remove temporary files before diagnostic output can block.
Writing diagnostics can still wait for stderr. The CLI does not make stderr globally nonblocking.

For SSE streams, a comment or an incomplete event can trigger the first-data diagnostic.
The CLI cannot display a parsed event until enough data arrives to decode it.
These client measurements also differ from the server's `openai-processing-ms` header.
The CLI retains its existing response-header redaction policy; debug output can redact that server header.

## Interpret a stalled request

- Missing diagnostics do not establish the current request stage. Stalled stderr can delay any diagnostic.
- For immediate diagnostics with draining stderr, headers without first data mean the observer has not recorded a nonempty read.
- First data without EOF does not establish whether the body remains open. Check for closure, failure, or cancellation diagnostics.
- Failure or cancellation: inspect the command's normal error and exit status. A timing line does not replace that error.

Binary responses defer body diagnostics until cleanup. Missing body lines during a download therefore do not identify its current read stage.

Use the returned `x-request-id` when investigating a request with support.
Missing request IDs do not prove the API received the request.
See the official [request debugging guidance](https://developers.openai.com/api/reference/overview#debugging-requests).

For Responses, `--stream true --format jsonl` exposes parsed events on stdout through the existing streaming interface.
`--raw-output` changes string formatting; it does not expose raw SSE frames.
Timing does not add request-body capture, response-body capture, or per-event tracing.
See the official [streaming guide](https://developers.openai.com/api/docs/guides/streaming-responses) for event semantics.

## Output and privacy

Timing follows the existing debug logging policy. It uses stderr and leaves command data on stdout:

```sh
openai --debug models list --format jsonl > models.jsonl
```

`--quiet` does not disable explicit debug logging. Debug logging also remains enabled with machine error formats.
Omit `--debug` when stderr must contain only structured errors.
Use `--verbose --format-error text` for command-level completion details instead of HTTP diagnostics.

Timing adds only attempt numbers, elapsed durations, fixed event labels, and standard HTTP status descriptions.
It never adds request bodies, response bodies, prompts, URLs, query values, or raw streaming frames.
Existing header redaction and query omission still apply. Review debug output before sharing it, especially custom request headers and paths.
