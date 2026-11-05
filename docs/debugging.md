# HTTP debugging

Use `--debug` to inspect redacted HTTP headers and request timing on stderr:

```sh
openai --debug models list
```

Ordinary `--debug` omits payloads. Select body inspection, SSE frame inspection, or event timing explicitly.

| Flag | Diagnostic view | Default |
| --- | --- | --- |
| `--debug-body` | Redacted JSON request and response structure | Off |
| `--debug-sse` | Escaped, redacted SSE frame previews | Off |
| `--debug-event-timing` | Timing for complete observed SSE data frames | Off |
| `--debug-max-bytes` | Capture budget, in bytes | `16384` |
| `--debug-max-events` | SSE frame-preview and data-frame-timing limits | `64` |

Enabling body inspection, SSE inspection, or event timing requires `--debug`.
Explicit `false` values leave capture disabled and do not require `--debug`.
Every explicitly supplied limit requires `--debug`, including default-valued limits and combinations with disabled capture flags.
Byte limits accept `1` through `1048576`. Event limits accept `1` through `4096`.
Invalid settings fail before the CLI sends a request.
Limits alone do not enable body or SSE inspection.

## Inspect JSON bodies

```sh
openai --debug --debug-body responses create \
  --model gpt-4.1-mini --input 'Explain HTTP caching.' --format json
```

Body previews show permitted JSON structure and safe control fields.
They redact string values, credentials, prompts, uploaded content, sensitive fields, and unknown field names.
They preserve the original request and response bytes for the API and normal output.

Capture observes reads that the transport or response consumer already performs. It never reads ahead to complete a preview.
Request previews describe bytes consumed during that attempt. A partial read remains labeled partial.
Redirected request previews show the latest replay body that the transport reads, without combining repeated payloads.
Opening an unused replay does not replace an already observed request preview.
Response previews describe the consumed body, including error responses.
Non-JSON bodies, including multipart uploads and binary responses, receive an omission marker.
Malformed or incomplete JSON receives a safe omission marker instead of an unredacted fallback.
Capture limits can omit a preview or mark a permitted structural prefix as truncated.

`--debug-max-bytes` bounds each body capture within an HTTP attempt.
Large API bodies continue normally when diagnostic capture reaches its limit.
Previews can also be omitted when their escaped representation exceeds the rendering budget.

## Inspect streamed frames and timing

```sh
openai --debug --debug-sse --debug-event-timing responses create \
  --model gpt-4.1-mini --input 'Explain HTTP caching.' --stream true --format jsonl
```

`--debug-sse` shows observed SSE line and frame structure using escaped text.
It redacts field values, comments, unknown field names, and sensitive JSON data.
Line endings and framing remain visible in the escaped representation.
These redacted previews cannot reconstruct the original payload.

`--debug-event-timing` reports complete observed data-frame boundaries and gaps between them.
The first gap starts at dispatch. Later gaps start at the previous complete data-frame observation.
A comment can trigger first-body-data timing without completing a data frame.
An incomplete frame does not receive a complete-event timing label.
Unknown event types remain available through the normal streaming output.

These measurements occur when the body reader observes bytes containing a frame boundary.
They do not measure SDK decoding, model generation, or stdout delivery.
Several frames returned by one read can share a timestamp.
The existing SDK and command-specific decoders retain their own framing and completion behavior.

SSE byte budgets bound retained frame previews and their escaped diagnostic representation within each attempt.
The event limit bounds frame previews and data-frame timings separately.
The CLI reports diagnostic truncation while continuing to deliver API events normally.
It does not add a payload, line, or event-size limit to the API stream.

Use smaller diagnostic limits when inspecting a long stream:

```sh
openai --debug --debug-sse --debug-event-timing \
  --debug-max-bytes 4096 --debug-max-events 8 \
  responses create --model gpt-4.1-mini --input 'Explain HTTP caching.' --stream true --format jsonl
```

`--raw-output` retains its existing string-formatting behavior. It never enables SSE diagnostics.
See the official [streaming guide](https://developers.openai.com/api/docs/guides/streaming-responses) for event semantics.

## Read request timing

Each HTTP attempt reports when response headers arrive. Retries and additional pages receive separate attempt numbers.
Numbers increase within the request logger used by the command.
An attempt covers one SDK dispatch. Redirected HTTP hops share that attempt number and contribute to its headers latency.
Measured output can look like this:

```text
HTTP attempt 1: response headers received after 184 ms (200 OK)
HTTP attempt 1: first response data read after 185 ms
HTTP attempt 1: response body fully consumed after 186 ms
```

These numbers are illustrative. The CLI measures monotonic elapsed time from immediately before dispatch.
Retry delays before that attempt are excluded.

- **Response headers received** measures the wait for headers. It does not measure total request time.
- **First response data read** measures the first nonempty body read. It does not identify the first parsed streaming event.
- **Response body fully consumed** means the reader reached EOF. It does not confirm application success or asynchronous job completion.
- **Response body closed before EOF** means the caller stopped reading. This can occur during retries or early stream closure.
- **Read, close, transport, or cancellation diagnostics** identify interrupted attempts without printing raw error details.

Body durations include time between caller reads. They can include parsing, output backpressure, and pauses in the consumer.
Empty bodies have no first-data diagnostic. The caller retains responsibility for closing the response body.
Protocol upgrades retain header timing. Upgraded protocol data and connection lifetime remain outside HTTP-body diagnostics.

Binary-response commands record body diagnostics during reads and report them after body closure and consumer cleanup.
This includes downloads and speech responses. Durations retain their original measurement times.
Logger timestamps show when the CLI reports them. Save receipts follow these deferred diagnostics.
This ordering lets managed downloads remove temporary files before diagnostic output can block.
Diagnostic writes can still wait for stderr.

## Interpret a stalled request

- Missing diagnostics do not establish the current request stage. Stalled stderr can delay diagnostics.
- Immediate headers without first data mean the observer has not reported a nonempty read.
- First data without a complete-frame timing can indicate a comment or an incomplete frame.
- First data without EOF does not establish whether the body remains open. Check closure, failure, or cancellation diagnostics.
- For failure or cancellation, inspect the command's normal error and exit status. Diagnostics do not replace that error.

Binary responses defer body diagnostics until cleanup. Missing body lines during a download do not identify its current read stage.
Capture limits can omit additional frame diagnostics. Their absence does not establish that the server stopped sending events.

Client measurements differ from the server's `openai-processing-ms` header.
The CLI retains its response-header redaction policy, which can redact that server header.
Use the returned `x-request-id` when investigating a request with support.
Missing request IDs do not prove that the API received the request.
See the official [request debugging guidance](https://developers.openai.com/api/reference/overview#debugging-requests).

## Preserve scripts and privacy

Diagnostics use stderr. Command data retains its selected format on stdout:

```sh
openai --debug --debug-body models list --format jsonl > models.jsonl
```

`--quiet` does not disable explicitly requested debug diagnostics.
Debug diagnostics also remain enabled with machine error formats.
Omit debug options when stderr must contain only structured errors.
Use `--verbose --format-error text` for command-level completion details instead of HTTP diagnostics.

Capture changes only diagnostic copies. It preserves retries, cancellation, streamed output, and response-body ownership.
Header redaction and complete query omission remain in effect for all debug modes.
Review diagnostic output before sharing it, especially custom request headers and paths.
The CLI does not create a capture file or introduce a server-side tracing workflow.
