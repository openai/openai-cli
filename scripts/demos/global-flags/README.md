# Global flag placement demo

The loopback fixture reflects the received `OpenAI-Project` header in a synthetic model ID.
The recorder uses matching commands and settings for both binaries.
Each command places `--project` and `--format=json` before, between, or after the command words.

Build the fixture:

```sh
go build -o /tmp/global-flags-demo-api ./scripts/demos/global-flags
```

Record the comparison with pinned binaries and commits:

```sh
DEMO_API_BINARY=/tmp/global-flags-demo-api \
  bash scripts/demos/global-flags/record.sh \
  /absolute/path/to/before/openai /absolute/path/to/after/openai \
  BEFORE_COMMIT_SHA AFTER_COMMIT_SHA /absolute/path/to/empty-output-directory
```

Put `asciinema`, `agg`, `ffmpeg`, `ffprobe`, and `python3` on `PATH` first.
The shared recorder requires an empty output directory outside the repository.
It runs isolated Bash sessions with a synthetic key and a loopback base URL.

The baseline must accept only the root placement.
The candidate must accept all three placements.
The recorder checks command exit statuses, JSON output, and actual request headers.
It retains these checks in `statuses.tsv`, `requests.jsonl`, and `validation.txt`.

Inspect `before.png`, `after.png`, and `comparison.gif` before sharing the captures.
The captures demonstrate terminal replay, not native terminal graphics or other platforms.
Keep all binary media outside Git.
