# Error recovery replay

The scenes run actual CLI binaries with isolated configuration and synthetic inputs.
They show commands and actual CLI or shell output without added exit-status labels.
Recovery scenes use a local synthetic API fixture.
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
Each recipe records command statuses in `before-statuses.tsv` and `after-statuses.tsv`.
Each line contains a fixed scene step and its observed numeric status, separated by a tab.
The recorder checks these files separately from the visible transcript.

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

The numeric replay compares published `18ae8487` with the reviewed candidate.
It shows explicit and piped invalid values, then a corrected piped request.
Each phase makes exactly one synthetic GET request.

```sh
bash scripts/demos/error-recovery/record-numeric.sh \
  /absolute/path/before/openai /absolute/path/after/openai \
  18ae848748d96e9b7f0d57378f9d89b981277816 AFTER_COMMIT \
  /absolute/path/new-numeric-output normal
```

Use `narrow` for a separate 40-column replay. Both profiles disable color.
The script checks the earlier executable hash, current source, request counts, and complete recovery text.


The executable-typo replay compares ordinary completion with the optional zsh suggestion hook.
It generates scripts with real binaries, then replaces the executable with a synthetic replay sentinel.
Both scenes retain status 127. The after scene also disables its session hook.
The scenes use an isolated home and do not execute an API command.
The separate public probe counts connection attempts; this replay has no loopback request counter.
The recorder checks source, binary, zsh, and recipe identities before and after capture.

```sh
bash scripts/demos/error-recovery/record-shell.sh \
  /absolute/path/before/openai /absolute/path/after/openai \
  BEFORE_COMMIT AFTER_COMMIT /absolute/path/new-shell-output normal
```

Use `narrow` for a separate 40-column replay. The zsh session starts without an existing command-not-found handler.
