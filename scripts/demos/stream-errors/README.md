# Stream and error compatibility recording

This compares two CLI binaries against identical synthetic failures on a loopback API.
The first command resumes after lifecycle events, then receives a reasoning delta followed by EOF.
The second command receives HTTP 502 with an empty error object.
The recorder checks stdout, stderr, exit status, and request count before assembling media.
The PTY displays captured output. The CLI writes to separate files during execution.

Use the existing asciinema, agg, ffmpeg, and Python tools. No installation or live API is required.
Keep media outside the repository:

```sh
bash scripts/demos/stream-errors/record.sh \
  /absolute/path/to/openai-before /absolute/path/to/openai-after \
  FULL_BASE_SHA FULL_CANDIDATE_SHA /absolute/path/to/empty-output-directory
```

The output includes comparison.gif, before.png, after.png, captured streams, and source metadata.
The shared capture helper owns the API process, capture process, and temporary settings cleanup.
This is a terminal replay, not native terminal appearance evidence.
