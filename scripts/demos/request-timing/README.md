# HTTP timing terminal replay

Use immutable before and after binaries with their source commits. Keep generated media outside the repository.
The recorder uses the existing `scripts/demos/capture_and_render.sh` lifecycle.
It requires Python 3, asciinema, agg, FFmpeg, and FFprobe.

```sh
bash scripts/demos/request-timing/record.sh \
  "$PWD" /absolute/path/before/openai /absolute/path/after/openai \
  BEFORE_COMMIT_SHA AFTER_COMMIT_SHA /absolute/path/empty-output-directory
```

Both commits must use full 40-character hashes.
The fixture serves one synthetic model on loopback with a fake API key.
It delays headers by 180 ms, then sends body halves after separate 240 ms delays.
The CLI measures actual durations; the recorder never inserts timing diagnostics.

The recorder captures `openai --debug models retrieve model_synthetic` in isolated Bash PTYs.
It checks command status, credential redaction, two requests, and three distinct timing stages.
The comparison includes both command runs, transcripts, screenshots, GIFs, source identities, and binary hashes.
Inspect the screenshots and comparison GIF before sharing them.

This is a terminal replay. It does not verify native Terminal appearance or live API behavior.
