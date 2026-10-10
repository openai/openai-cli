Audio workbench integration and replay recipe

The coordinator delegated these feature-local files on October 9, 2026.
No harness or PTY capture has run yet.

Read these references before execution or review:

- `/Users/vguvvala/code/cli-work/DESIGN.md`
- `/Users/vguvvala/code/cli-work/STATUS.md`
- `/Users/vguvvala/code/cli-work/PRE_PUSH_REVIEW.md`

Request a PTY reservation before running either command below.
Keep evidence outside the repository.
Use immutable binaries with recorded complete source identities.

```sh
python3 -I -B scripts/demos/audio-workbench/check.py \
  --binary /absolute/path/to/openai \
  --source CANDIDATE_COMMIT_SHA \
  --repo-root /Users/vguvvala/.codex/worktrees/1f6c/openai-cli \
  --output /absolute/evidence/audio-workbench-dark \
  --theme dark
```

The default run checks workflow, Ctrl+C, SIGTERM, stopped-reader cancellation, and optional-startup isolation.
Use `--case NAME` to repeat one affected case.
Use `--theme light` or `--theme no-color` for the additional palette checks.

```sh
DEMO_REPO_ROOT=/Users/vguvvala/.codex/worktrees/1f6c/openai-cli \
bash scripts/demos/audio-workbench/record.sh \
  /absolute/path/to/before-openai /absolute/path/to/after-openai \
  BEFORE_COMMIT_SHA CANDIDATE_COMMIT_SHA \
  /absolute/evidence/audio-workbench-replay
```

The recorder sources `scripts/demos/capture_and_render.sh` without modifying its lifecycle.
It requires Python 3, asciinema 3.2.1, agg 1.9.0, ffmpeg, and ffprobe.
The helper files must retain executable permission after placement.

The fixture serves only the loopback speech endpoint.
It validates the fake credential and known synthetic request fields.
It returns mono signed PCM16 at 24 kHz, with exactly 48,000 sample frames.
The WAV fixture wraps those same samples.
Eight amplitude sections create a measured waveform with exactly two seconds of duration.
The fixture does not synthesize spoken words.

The workflow drives the actual bare `openai audio speak` form.
It types multiline text and instructions with literal `@` and backslash-`@` values.
It opens model, voice, and format choices, then changes speed to 1.25.
Choices and typing must make no request.
Ctrl+G starts one explicit synthetic generation.
The harness checks request fields, measured duration, varying peaks, and exact saved audio bytes.
It exercises play/stop, save, History, 40×12, too-small blocking, an explicit audition, restoration, and Clear.
Audition entries must remain absent from ordinary History.
Clear must preserve retained results and exported files.

Playback uses a private fake FFplay executable.
That helper consumes the real fixture bytes, records their hash, and produces no sound.
Actual FFplay and audible device behavior require separate native evidence.
No microphone, real credential, paid API, or remote account is used.

The harness checks exit 130 for Ctrl+C and exit 143 for SIGTERM.
Stopped-reader cancellation must finish without releasing terminal backpressure.
The inherited PTY helper checks terminal modes, cursor visibility, and bracketed-paste restoration.
It also rejects alternate-screen use and whole-screen or scrollback clearing by the CLI.
Asciinema reconstructs visible terminal state; ANSI stripping alone does not prove row cleanup.
The final screen must contain no active audio-form title.

The startup case leaves `OPENAI_PICKER_SHELL` unset.
It keeps a native Bash or Zsh parent alive around every CLI process.
Its PATH resolves `openai` to the exact candidate executable.
Temporary HOME, cache, config, and ZDOTDIR paths isolate all local writes.
Canary `.zshrc`, `.bashrc`, and `.bash_profile` files must remain byte-identical.
The case covers the bare form and local history, inspect, export, and play commands.
Local commands also exercise root flags before the group, within the group, and after the leaf.
Unavailable native shells appear as not-run evidence, not passes.

Fake HOME triggers the startup implementation's later real-home guard.
This public case proves no profile/configuration writes under that isolation.
It does not prove the early eligibility gate excludes audio commands.
The independent reviewer owns that separate regression with all eligibility prerequisites enabled.

Public verification commands use the same isolated cache:

```sh
openai --format json audio history
openai --format json audio inspect RESULT_ID
openai audio export RESULT_ID --output saved.wav
openai audio export RESULT_ID --output - > original.wav
openai audio play RESULT_ID
openai --format json audio history --clear
```

The PTY workflow covers form Clear; it does not clear history during the screenshot sequence.
The public regression suite separately checks history clearing and export preservation.

The replay compares baseline explicit flags/save with the candidate guided form.
It labels fake playback and synthetic data throughout.
The baseline finishes with status 0; the candidate exits through Ctrl+C with status 130.
The recorder retains source hashes, process statuses, request evidence, casts, GIFs, and PNG checkpoints.
Every recorded frame comes from the actual CLI PTY.

Limitations remain explicit until execution:

- The staged source has no behavioral or rendering pass yet.
- This recipe is a terminal replay, not graphical Terminal or font acceptance.
- Native Windows/Linux, audible playback, and live API acceptance remain separate evidence.
- UI labels can evolve before the first run; update exact assertions rather than weakening behavior checks.
