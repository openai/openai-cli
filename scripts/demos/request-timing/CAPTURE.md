# Redacted capture terminal replay

This recipe reuses `scripts/demos/capture_and_render.sh`.
The recorder uses synthetic Responses fixtures and fake credentials.
It never calls the live API.
The fixture supplies delays; the CLI measures and prints actual durations.

## Scenes

| Scene | Diagnostic options | Visible result |
| --- | --- | --- |
| Before body | `--debug` | Existing headers and extracted usage |
| After body | `--debug --debug-body` | Redacted input, visible operational values, and usage structure |
| Before stream | `--debug` | Existing headers and parsed event types |
| After stream | `--debug --debug-body --debug-sse --debug-event-timing` | Escaped frames and measured event timing |
| After limits | Previous options plus `--debug-max-bytes 256 --debug-max-events 1` | Limit notices with all three parsed events preserved |

The finite request shows `store: false` and `max_output_tokens: 8`.
The response shows numeric usage values.
Diagnostics redact the prompt, response text, comment text, and arbitrary string values.
The echoed command intentionally shows its synthetic prompt.

The validator checks matching request hashes within the body and streaming comparisons.
It requires complete parsed stream output despite diagnostic limits.
It also checks redaction, actual timing records, escaped line endings, and limit notices.

## Dependencies

Use installed Bash, Python 3, asciinema, agg, FFmpeg, FFprobe, and Menlo.
Use immutable before and after binaries with verified source commits and binary hashes.
Use current main before this feature as the baseline. The baseline must not report HTTP attempt timing.
The latest October 23 refresh uses main `c53b5891` as its baseline.
Each scene runs `openai --version` and identifies the binary's source commit.
Current builds retain the SDK's `X-Stainless-*` headers and the release version, `1.40.6`.
These identifiers do not establish the recording date. Use its source commit and capture metadata.
Do not install tools during the recording reservation.
Keep both output directories empty and outside the repository.

After source checks pass, freeze exact binary paths and hashes in the recording evidence.
Reserve the required local resources before running either command.

```sh
/bin/bash scripts/demos/request-timing/record_capture.sh \
  /ABSOLUTE/REPOSITORY /ABSOLUTE/BEFORE_BINARY /ABSOLUTE/AFTER_BINARY \
  BEFORE_40_CHARACTER_COMMIT AFTER_40_CHARACTER_COMMIT \
  /ABSOLUTE/EMPTY_OUTPUT/normal normal

/bin/bash scripts/demos/request-timing/record_capture.sh \
  /ABSOLUTE/REPOSITORY /ABSOLUTE/BEFORE_BINARY /ABSOLUTE/AFTER_BINARY \
  BEFORE_40_CHARACTER_COMMIT AFTER_40_CHARACTER_COMMIT \
  /ABSOLUTE/EMPTY_OUTPUT/narrow narrow
```

The normal replay uses 112 columns and 68 rows.
The narrow replay uses 72 columns and 84 rows.
Both use Menlo at 18 pixels, line height 1.4, and monochrome output in isolated Bash PTYs.
The dark background is `#171717`; the foreground is `#eeeeee`.
Each scene isolates configuration and cache files. Update checks are disabled.
Each replay makes five API requests. Five additional CLI processes display the version.

## Evidence

Each replay retains scene casts, transcripts, GIFs, PNGs, request hashes, validation output, and metadata.
The recorder also creates a combined GIF and transcript.
Review the full GIF and both widths before selecting screenshots.
Wrapped diagnostics can extend beyond the final screenshot.

Label every image and GIF as a **terminal replay**.
This evidence does not establish native Terminal appearance or live API behavior.
Keep binary media outside Git.
Publish only reviewed attachments under separate authorization.
Keep metadata private because it contains local paths.

The shared recorder owns its capture and fixture processes.
It does not enforce a global renderer deadline.
The recording wrapper must provide an outer deadline and process cleanup.
The fixture's socket timeout does not replace that deadline.

Historical timing recordings retain their original binary identities.
They do not establish full capture behavior for the refreshed candidate.
