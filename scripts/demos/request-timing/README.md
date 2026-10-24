# HTTP timing terminal replay

Use immutable before and after binaries with their source commits. Keep generated media outside the repository.
Use current main before this feature as the baseline. The baseline must not report HTTP attempt timing.
The recorder uses the existing `scripts/demos/capture_and_render.sh` lifecycle.
It requires Python 3, asciinema, agg, FFmpeg, and FFprobe.

```sh
bash scripts/demos/request-timing/record.sh \
  "$PWD" /absolute/path/before/openai /absolute/path/after/openai \
  BEFORE_COMMIT_SHA AFTER_COMMIT_SHA /absolute/path/empty-output-directory
```

Both commits must use full 40-character hashes.
The latest October 23 refresh uses main `c53b5891` as its baseline.
Each scene runs `openai --version` and identifies the binary's source commit.
Current builds retain the SDK's `X-Stainless-*` headers and the release version, `1.40.6`.
These identifiers do not establish the recording date. Use its source commit and capture metadata.
The fixture serves one synthetic model on loopback with a fake API key.
It delays headers by 180 ms, then sends body halves after separate 240 ms delays.
The CLI measures actual durations; the recorder never inserts timing diagnostics.

The recorder captures `openai --debug models retrieve model_synthetic` in isolated Bash PTYs.
It checks command status, credential redaction, two requests, and three distinct timing stages.
The comparison includes both command runs, transcripts, screenshots, GIFs, source identities, and binary hashes.
Inspect the screenshots and comparison GIF before sharing them.

The replay uses 112 columns, 47 rows, Menlo at 18 pixels, and line height 1.4.
Its dark background is `#171717`; its foreground is `#eeeeee`. CLI diagnostics remain monochrome.
Each scene isolates configuration and cache files. Update checks are disabled.

This is a terminal replay. It does not verify native Terminal appearance or live API behavior.
