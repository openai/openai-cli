# Deterministic output comparison

This recipe records real CLI executables against one synthetic loopback API.
The comparison contains three short scenes:

1. The baseline sends forced ANSI color into a JSON parser and delays its first structured stream event.
2. The candidate preserves plain piped JSON and emits the first event before the delayed second event.
3. Quiet preserves selected data, while verbose keeps optional details on stderr.

The visible examples use `openai`.
The recorder creates temporary executable links; it does not install either executable.
Every scene uses an isolated home and environment with a fake key.
The fixture rejects unexpected routes, request bodies, query parameters, and credentials.
It logs validated synthetic request fields only.

`arrival.py` adds consumer timestamps for the demo.
The CLI itself does not add timestamps or change its event bytes.
The fixture sends its second event two seconds after its first event.
The validator compares complete original events and checks their measured arrival gap.
These measurements illustrate timing; handshake regression tests provide the separate correctness gate.

The recipe reuses `../capture_and_render.sh` for capture, rendering, cleanup, and assembly.
It requires existing asciinema 3.2.1, agg 1.9.0, ffmpeg, ffprobe, Python 3, and Bash.
The fixture uses only the Go standard library.
No dependency installation, upload, live API call, or CLI installation occurs.

Build the fixture into the task's evidence directory:

```sh
GOMAXPROCS=2 go build -p 2 -o /path/to/evidence/bin/output-demo-api ./scripts/demos/output
```

Verify both CLI executables against their source revisions before recording.
The original baseline is `da762ffff4f35732f4720ac4db531d8d764f2cbe`.
Use an empty output directory outside Git.
Obtain the coordinator's serialized PTY window before running the recorder.

```sh
DEMO_API_BINARY=/path/to/evidence/bin/output-demo-api \
  bash scripts/demos/output/record.sh \
    /path/to/evidence/bin/openai-before /path/to/evidence/bin/openai-after \
    "$BEFORE_SHA" "$AFTER_SHA" /path/to/evidence/demo/capture
```

The recorder retains individual and combined casts, GIFs, screenshots, transcripts, request records, and command statuses.
It also retains raw piped output, stderr files, source hashes, executable hashes, and tool versions.
No visible exit-status footer appears; `statuses.tsv` preserves all process results.

Inspect all three screenshots and representative GIF frames before sharing.
Check clipping, command spacing, stream timing, and the separation of data from diagnostics.
This is an asciinema terminal replay, not native graphical-terminal or Windows validation.
Keep recordings outside Git and do not publish them without authorization.
