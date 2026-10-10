# Error recovery replay

The scenes run actual CLI binaries with isolated configuration and synthetic inputs.
They make no API calls and show real failing exit statuses.
Use matching binaries and record their full commit IDs.

```sh
export PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH"
bash scripts/demos/error-recovery/record.sh \
  /absolute/path/before/openai /absolute/path/after/openai \
  BEFORE_COMMIT AFTER_COMMIT /absolute/path/new-output-directory
```

The script reuses `scripts/demos/capture_and_render.sh`.
It writes before/after PNGs, a comparison GIF, transcripts and metadata outside the repository.
These assets are terminal replays, not native Terminal screenshots.

The nonterminal comparison uses the same public commands across more command families:

```sh
python3 scripts/demos/error-recovery/probe.py \
  /absolute/path/openai /absolute/path/probe.json --source FULL_COMMIT
```

The probe records stdout, stderr, exit status and loopback request count.
Its request count should remain zero.

The option-scope replay compares the prior PR head with the revised candidate.
It uses a synthetic loopback Files response after the user moves the option to its supported scope.

```sh
bash scripts/demos/error-recovery/record-scope.sh \
  /absolute/path/before/openai /absolute/path/after/openai \
  BEFORE_COMMIT AFTER_COMMIT /absolute/path/new-normal-output normal
```

Use a separate output directory with `narrow` for the 40-column light replay.
Both profiles set `NO_COLOR=1`. The shared recorder owns server shutdown and capture cleanup.
