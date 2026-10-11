Audio workbench integration and replay recipe

The coordinator delegated these feature-local files on October 9, 2026.
Keep each result manifest with its binary hash, source identity, and recorded environment.
A pass applies only to the recorded cases and environment.

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
  --navigation-observer /Users/vguvvala/code/cli-work/new-features-20261009/transcript-timestamps/audio-parity/navigation_observer_pyte.py \
  --output /absolute/evidence/audio-workbench-dark \
  --theme dark
```

The default run checks workflow, compact generation, idle and busy cancellation, stopped-reader cancellation, and optional-startup isolation.
It also checks zero-request navigation at 90×34 and 40×12.
The `failure` and `failure-compact` cases cover one synthetic HTTP 401 followed by an explicit successful retry.
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
The renderer uses the exact font chain `Menlo,Apple Symbols` at 22 pixels.
Menlo supplies the text metrics; Apple Symbols supplies missing glyphs, including Braille waveform cells.

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
Save coverage rejects `-`, then verifies the corrected destination contains the original audio bytes.
Audition entries must remain absent from ordinary History.
Clear must preserve retained results and exported files.

The `compact` case launches its actual PTY at 40×12 and keeps that size throughout.
It generates one synthetic result without navigating wide settings labels.
It verifies measured duration, varying peaks, exact exported audio bytes, and strict cancellation cleanup.
Its original cast header records 40×12; the recorder does not rewrite headers or extract a final frame's output bytes.

The wide comparison uses `--emit --no-resize` and stays at 90×34.
The `--no-resize` option is restricted to that emitted workflow.
The ordinary workflow retains all resize checkpoints and strict Clear and absence assertions.
The recorder renders a separate `compact` capture for the 40×12 screenshot.
This separation avoids replaying resize events that agg 1.9.0 ignores.

The `navigation` and `navigation-compact` cases use monochrome focus markers.
They cover CSI and SS3 arrows, editor visual boundaries, multiline text, wrapped Unicode, modifiers, and active selection.
They check forward and reverse Tab, settings boundaries, chooser navigation, and command-preview scrolling.
A private synthetic History entry supplies Result navigation without generation.
Both cases require zero accepted or rejected fixture requests and strict cancellation cleanup.
The ordinary workflow selects Clear with End; it does not depend on settings wraparound.
The navigation cases test Alt+Enter without submitting editor text.
They leave Enter generation and the existing repeat-submit guard unchanged.

Navigation and failure cases require the explicit reviewed `--navigation-observer` adapter.
The default all-cases run also requires that argument because it includes these cases.
The checker verifies the adapter hash before loading it.
Observer construction verifies pinned pyte 0.8.2 and wcwidth 0.2.13 sources before importing them.
No package installation or project dependency change is required.
The adapter remains outside the repository beside its reviewed package directory.

This local command uses the immutable navigation runtime from source `d024992`:

```sh
python3 -I -B /Users/vguvvala/.codex/worktrees/1f6c/openai-cli/scripts/demos/audio-workbench/check.py \
  --binary /Users/vguvvala/code/cli-work/new-features-20261009/transcript-timestamps/audio-parity/source-run-14-navigation/openai-navigation \
  --source d024992aa2ec3457c1f1131e96105a199048b223 \
  --case navigation \
  --navigation-observer /Users/vguvvala/code/cli-work/new-features-20261009/transcript-timestamps/audio-parity/navigation_observer_pyte.py \
  --output /Users/vguvvala/code/cli-work/new-features-20261009/transcript-timestamps/audio-parity/navigation-observer-wide-review
```

Use `--case navigation-compact` with a different fresh output directory for the 40×12 case.
Record the checker commit separately from the binary runtime commit.
The observer directory must retain the sources and manifest described in its external `REVIEW.md`.

Every navigation focus assertion reads a persistent combining-aware viewport.
Final navigation checks separately require a blank viewport and no retained owned scrollback rows.
Results record `navigation_observer` provenance and `navigation_scrollback_clear`.
Product History preservation remains a separate assertion.
The checker rejects navigation resizing and preserves all original raw bytes and cast events.
Failure cases also use the current viewport for safe-error and obsolete-feedback assertions.
Other cases retain the existing asciinema observer, including the ordinary resize workflow.
pyte has a combining-at-right-margin limitation and does not establish universal grapheme or native-terminal behavior.
Reaching the adapter's scrollback bound fails the check instead of allowing a cleanup pass.

Failure recovery cases

Use `--case failure` at 90×34 or `--case failure-compact` at an actual fixed 40×12.
Pass the reviewed `--navigation-observer` adapter and a different fresh output directory for each case.
These cases do not resize, rewrite cast headers, or alter captured bytes.

The first explicit Ctrl+G sends the known synthetic text and instructions.
The fixture returns HTTP 401 with valid Request ID `req_audio_demo_401` and malicious synthetic message canaries.
The form must show `HTTP 401: Unauthorized.`, `Check key, project, organization.`, and `Setup: openai help setup`.
The fixture's message does not justify claiming a particular credential is invalid.
SS3 F1 must open `Audio error` with safe details; End must make the valid Request ID visible.
Escape must return to the form without quitting or sending another request.
Tab navigation and a model chooser round trip must not repeat generation.
The failed response must not enter History.
The harness follows the printed `openai help setup` instruction through the same immutable candidate and isolated synthetic HOME.
It requires useful API-key setup help, zero added requests, unchanged synthetic HOME, and no canary or fake credential value.
The harness does not run setup instructions, enter a credential, or modify live configuration.

Only another explicit Ctrl+G receives the synthetic WAV response.
The second request must preserve the exact original input, instructions, and settings.
Successful recovery must remove obsolete HTTP 401, authentication guidance, Request ID, and F1-error hints from the current viewport.
The harness verifies two-second measurement, one retained result, and byte-identical WAV export.
It rejects every API-message canary across the complete raw terminal capture, including earlier erased frames.
The successful-retry session finishes through Ctrl+C with exit 130, blank viewport, and empty owned scrollback.
Each case then launches a second owned session and returns one fresh synthetic 401.
Escape must exit that failed form with status 1 and a safe HTTP 401 diagnostic after terminal restoration.
That diagnostic may remain visible. Owned form rows must be cleared from the viewport and scrollback.
The second session must preserve the first session's retained History and send no implicit retry.
Both sessions retain strict native-mode, ANSI, exact-byte observer, and process-group cleanup checks.

Each size therefore requires exactly three speech requests across two owned PTY sessions: 401, WAV, then 401.
The setup-help child is a separate read-only command with zero speech requests.
Nine capture checkpoints use the existing 900-ms settling interval, for 8.1 seconds of deliberate capture settling per case.
Allow roughly 20–30 seconds per healthy case before rendering; this estimate is not measured execution evidence.
Existing per-command and cleanup deadlines remain in force; the outer runner must retain its total execution bound.

The capture checkpoints are `failure-initial-form`, `failure-ready`, `failure-form`, `failure-details`,
`failure-returned-form`, `failure-settings-no-retry`, and `failure-recovered-result`.
The second session adds `failure-error-exit-form` and `failure-error-exit-summary`.
Results retain all three synthetic request records and the original audio hash; they do not record the malicious response text.
The fixture uses only fake credentials and fake secrets. It reads no real credential source and calls no remote API.
As with other cases, source preparation alone does not establish these checks passed; execution needs a reserved slot.

Playback uses a private fake FFplay executable.
That helper consumes the real fixture bytes, records their hash, and produces no sound.
Actual FFplay and audible device behavior require separate native evidence.
No microphone, real credential, paid API, or remote account is used.

The harness checks exit 130 for Ctrl+C and exit 143 for SIGTERM.
Busy cancellation waits for a flushed partial response before sending either signal.
It requires one request, client disconnection, no retained partial audio, and joined fixture threads.
Stopped-reader cancellation must finish without releasing terminal backpressure.
The stopped-reader case checks bounded exit, native terminal modes, and observed helper cleanup.
It records direct CLI children through `/bin/ps` before blocking output.
It checks those PIDs disappear after exit, without sending them signals.
Missing helper observations fail coverage; they do not count as cleanup passes.
Blocked output can prevent the CLI's bounded cursor, bracketed-paste, and row cleanup.
The result records those visual limits without claiming full terminal restoration.
Normally draining cases retain strict cursor, bracketed-paste, and cleared-row assertions.
All cases reject alternate-screen use and whole-screen or scrollback clearing by the CLI.
The selected terminal observer checks retained display state; ANSI stripping alone does not prove row cleanup.
Normally draining final screens must contain no active audio-form title.
Each terminal uses one incremental UTF-8 decoder across reads and intermediate capture checkpoints.
Raw captures preserve the original bytes.
Screenshots require captures from the corrected decoder; older per-read captures can contain artificial replacement characters.

The startup case leaves `OPENAI_PICKER_SHELL` unset.
It keeps a native Bash or Zsh parent alive around every CLI process.
It checks native terminal modes and ANSI cleanup before releasing that parent.
It then releases and reaps the parent, preserving the CLI status.
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
The wide comparison does not establish resize behavior; the separate ordinary workflow supplies that evidence.

Keep these evidence limits explicit:

- This recipe is a terminal replay, not graphical Terminal or font acceptance.
- Native Windows/Linux, audible playback, and live API acceptance remain separate evidence.
- Preserve earlier failed captures when rerunning corrected fixture checks.
- A stopped-reader pass does not establish visual reset or row cleanup under permanent output backpressure.
- Source and fixture changes require affected reruns before screenshots or result claims use the new source identity.
