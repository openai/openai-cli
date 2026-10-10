# Project lifecycle demo

The recording compares two real CLI binaries against a loopback fixture.
All responses and credentials are synthetic. The fixture makes no external requests.
The images show terminal replays, not graphical-terminal acceptance or live API validation.

Prerequisites: Python 3, Bash, asciinema, agg, ffmpeg, and ffprobe.
Build the candidate with `go build -o /tmp/project-after ./cmd/openai`.
Build the comparison binary from the recorded baseline in its own checkout.

Run from the repository root:

```sh
bash scripts/demos/project-lifecycle/record.sh \
  /tmp/project-before /tmp/project-after \
  FULL_BASE_SHA FULL_CANDIDATE_SHA /tmp/project-lifecycle-demo
```

Use a new output directory for each recording.
`DEMO_WINDOW_SIZE=40x48` selects a narrow terminal.
`DEMO_THEME=github-light` selects a light replay theme.
The scene uses `NO_COLOR=1`; results do not require color.

The recorder verifies real exit statuses, response text, and matching requests.
It keeps casts, transcripts, screenshots, a GIF, and source hashes outside the repository.
It stops its fixture and removes its temporary homes when it exits.
Remove the chosen output directory when you no longer need its evidence.

See [the workflow guide](../../../docs/project-lifecycle.md) for ordinary CLI usage and validation limits.
