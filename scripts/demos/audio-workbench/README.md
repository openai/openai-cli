Audio workbench integration and replay recipe

Keep each result manifest with its binary hash, source identity, and recorded environment.
A pass applies only to the recorded cases and environment.
Read `CONTRIBUTING.md` and `SECURITY.md` before running this development tooling.

Prerequisites are Python 3.10 or newer, Bash, and asciinema 3.2.1.
Install the pinned observer sources before navigation, failure, or the default all-cases run:

```sh
python3 -I -B scripts/demos/audio-workbench/setup_observer.py
```

Setup downloads only files listed in `observer_sources.json` from immutable upstream commits.
It verifies every source and license hash before publishing the installation directory.
It runs no packaging hooks, changes no Python environment, and installs no CLI dependency.
The default location is `$XDG_CACHE_HOME/openai-cli/audio-observer/MANIFEST_SHA`, using `~/.cache` when unset.
Use `--output /absolute/evidence/observer-sources` to choose another fresh directory.
Use `--source-directory /absolute/path/to/verified-bundle` for an offline copy with the same hash checks.
Pass that output through the checker's `--observer-source-directory` option.

The sources are pyte 0.8.2 at `b106bb7bd23ab31ec7709c5e6788117dcc07203c`
and wcwidth 0.2.13 at `36a625179ed2675287fe6b61c2ad319406449e60`.
Their repository URLs, file hashes, licenses, and source notices remain in the pinned manifest and installed bundle.
pyte includes LGPLv3 and embedded GPL/BSD notices. wcwidth includes MIT and Markus Kuhn notices.
These development dependencies do not enter the CLI binary or release artifacts.

Request a PTY reservation before running either command below.
Keep evidence outside the repository.
Use immutable binaries with recorded complete source identities.

```sh
python3 -I -B scripts/demos/audio-workbench/check.py \
  --binary /absolute/path/to/openai \
  --source CANDIDATE_COMMIT_SHA \
  --repo-root "$PWD" \
  --output /absolute/evidence/audio-workbench-dark \
  --theme dark
```

The default run checks workflow, compact generation, idle and busy cancellation, stopped-reader cancellation, and optional-startup isolation.
It also checks zero-request navigation at 90×34 and 40×12.
The `failure` and `failure-compact` cases cover recovery, explicit retry, and a second session's legacy-model error exit.
Use `--case NAME` to repeat one affected case.
Use `--theme light` or `--theme no-color` for the additional palette checks.

```sh
DEMO_REPO_ROOT="$PWD" \
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
New drafts contain the editable Instructions example `Speak warmly and clearly.`
The workflow replaces that example with multiline instructions containing literal `@` and backslash-`@` values.
It opens model, voice, and format choices, then changes speed to 1.25.
Choices and typing must make no request.
Ctrl+G starts one explicit synthetic generation.
The harness checks request fields, measured duration, varying peaks, and exact saved audio bytes.
It exercises Play and Save controls, History, 40×12, too-small blocking, explicit audition, restoration, and Clear.
History, voice Preview, and Clear use the More actions menu.
The ordinary form contains no command-preview row or keyboard footer.
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
They check forward and reverse Tab, settings boundaries, chooser navigation, and the More actions menu.
Command inspection opens a separate scrollable modal through More actions.
A long synthetic draft proves command scrolling at both terminal sizes.
Help opens locally without submitting speech.
The Result section exposes separate Play and Save actions.
A private synthetic History entry supplies Result navigation without generation.
Both cases require zero accepted or rejected fixture requests and strict cancellation cleanup.
Settings End selects More actions. Clear is the third More action.
Clear empties Text and Instructions without reseeding the constructor example.
The navigation inventory contains 185 current-viewport checkpoints at each size.
The navigation cases test Alt+Enter without submitting editor text.
They leave Enter generation and the existing repeat-submit guard unchanged.

Navigation and failure cases use the repository-owned `observer.py` by default.
The default all-cases run includes both groups and never silently skips them.
The checker verifies the adapter hash before loading it.
Observer construction verifies the manifest, dependency hashes, import inventory, and loaded module origins.
Missing sources stop execution before a PTY starts and identify the setup command.
The optional `--navigation-observer` override preserves the previously reviewed external adapter's exact hash.
It is unnecessary for a fresh checkout and cannot bypass dependency integrity checks.

Use a binary built from the current reviewed source with a fresh evidence directory.
Record the checker commit separately if it differs from the binary runtime commit.
Prior results retain their original source identities and do not establish acceptance of changed code.

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
Use the installed observer sources and a different fresh output directory for each case.
These cases do not resize, rewrite cast headers, or alter captured bytes.

The first explicit Ctrl+G sends synthetic Text with the actual seeded Instructions example.
The fixture returns HTTP 401 with valid Request ID `req_audio_demo_401` and malicious synthetic message canaries.
The fixture supplies the exact error code `invalid_api_key`.
The recovery panel therefore uses `The API did not accept this key.`
A generic HTTP 401 without that code retains `The API could not verify your access.`
Its selectable actions are API setup, Error details, and Back to form.
The message does not identify the credential source or claim expiration.
Raw HTTP status, Request ID, setup commands, and error shortcuts must not appear on the ordinary form.

API setup opens the real scrollable canonical guide inside the form.
The guide explains that users must close the form before setup and reopen audio speak afterward.
The checker reads the guide through its final scroll position.
It verifies useful setup content against the same binary's read-only `help setup` output.
The guide and verification command must add zero requests and leave synthetic HOME unchanged.
They must not expose any malicious canary or fake credential value.
The checker does not execute credential setup steps or change live configuration.
Escape returns from API setup to recovery.

Error details opens a separate panel with `HTTP status: 401` and the safe Request ID.
Escape returns from details to recovery.
Back to form returns to the More actions setting without quitting or sending another request.
A model chooser round trip and More actions' Last error entry must not repeat generation.
The failed response must not enter History.

Only another explicit Ctrl+G receives the synthetic WAV response.
The second request must preserve the original Text, default Instructions, and settings exactly.
Successful recovery must remove the active error and the Last error action.
The checker verifies two-second measurement, one retained result, and byte-identical WAV export.
It rejects API-message canaries across the complete raw capture, including erased frames.
The successful session finishes through Ctrl+C with exit 130, a blank viewport, and empty owned scrollback.

Each case then launches a second owned session with a new default Instructions draft.
This session selects `tts-1` and the supported Alloy voice.
Instructions become disabled; changing back to a supported model restores the unchanged draft.
The session reselects `tts-1` before one explicit request.
That request must omit the `instructions` field entirely.
The fixture rejects legacy requests that contain Instructions or use another voice.

The second session receives one synthetic HTTP 401.
Escape first returns from recovery to the ordinary More actions setting.
A second Escape exits with status 1 and the original safe diagnostic after terminal restoration.
The final diagnostic may remain visible. Owned form and recovery rows must be cleared.
The session preserves earlier History and sends no implicit retry.
Both sessions retain strict native-mode, ANSI, exact-byte observer, and process-group cleanup checks.

Each size requires three speech requests across two owned sessions: 401, WAV, then 401.
The setup verification command is a separate read-only process with zero speech requests.
Twelve capture checkpoints retain the existing 900-ms settling interval.
They require 10.8 seconds of deliberate capture settling per case.
Existing operation and cleanup deadlines remain unchanged. The outer runner must enforce its total execution bound.

First-session checkpoints are `failure-initial-form`, `failure-ready`, `failure-recovery`, `failure-api-setup`, and `failure-details`.
They continue with `failure-returned-form`, `failure-settings-no-retry`, and `failure-recovered-result`.
Second-session checkpoints are `failure-legacy-ready`, `failure-error-exit-recovery`, `failure-error-exit-form`, and `failure-error-exit-summary`.
Results retain three request records and the original audio hash. They omit the malicious response text.
The fixture uses fake credentials and fake secrets. It reads no real credentials and calls no remote API.
Source preparation does not establish passing behavior. Execution requires a reserved slot.

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

Print command lifecycle

Use `--case print-command` with a fresh evidence directory and the exact candidate binary.
This focused case needs Bash and the existing private-PTY reservation.
It does not need asciinema, a navigation observer, or native desktop input.
Use the existing bounded supervisor with a 120-second outer limit.
The case allows 60 seconds for two CLI sessions. Existing ownership cleanup retains its separate bounded waits.

Both sessions use 40×12, fake credentials, loopback, and a private HOME.
The checker selects the actual binary through an isolated `openai` symlink.
It enters 60 ASCII `A` characters and opens Show command.
Enter must preserve the form and make no request.
The first session uses Ctrl+P. The second uses Ctrl+C.
The checker requires exit 0 and 130 respectively.

The Bash parent stays alive after the CLI exits.
The checker compares native terminal modes before releasing that parent.
It requires final cursor and bracketed-paste cleanup before the printed command.
It compares complete command bytes against an independent synthetic request specification.
Only the terminal's original newline translation is accounted for.
The checker preserves raw output, casts, emitted bytes, process statuses, and owned-process cleanup records.
Both sessions must make zero accepted or rejected requests and create no audio History.
A forced harness cleanup fails the case.

This case verifies public process behavior and terminal restoration.
It does not verify actual graphical-terminal selection, clipboard bytes, fonts, or audible playback.
The Go Bash/zsh recorder tests separately execute printed command bytes through both shells.
Keep unavailable platforms and deferred restore/close fault injection explicit.


Checker structure and focused checks

`check.py` parses options and selects direct scenario functions from a small dispatch map.
`scenarios.py` contains the workflow, navigation, recovery, startup, printing, and cancellation scenarios.
`case_context.py` owns each case's result, private fixture, terminals, and capture operations.
The run-level harness retains signal handling and final cleanup across all registered cases.
Terminal construction receives its case explicitly; it does not read a surrounding loop's result variable.
Fixture ownership is registered before thread construction or startup.
Terminal ownership is registered before observer setup and before returning the terminal to its caller.

Run these focused checks without a PTY or network connection:

```sh
python3 -I -B scripts/demos/audio-workbench/test_case_context.py
python3 -I -B scripts/demos/audio-workbench/test_observer.py
```

These tests cover partial startup, deferred interruption, continued cleanup, source integrity, and scrollback saturation.
They do not replace the full checker run against the exact candidate binary.
