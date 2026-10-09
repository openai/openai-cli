# HTTP request timing

Use the existing `--debug` flag to inspect HTTP headers and timing on stderr:

```sh
openai --debug models list
```

Each HTTP attempt reports when response headers arrive. Retries and additional pages receive separate attempt numbers.
Numbers increase within the request logger used by the command.
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
