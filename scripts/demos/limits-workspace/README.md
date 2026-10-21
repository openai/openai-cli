# Limits workspace terminal replay

This recipe drives an immutable CLI binary against a synthetic loopback API.
It makes no live API calls or billing changes.
The recorder uses the shared `capture_and_render.sh` lifecycle and existing PTY harness.
No Go build occurs during recording.

Follow the current local resource policy before source builds or PTY replay.
Read `/Users/vguvvala/code/cli-work/DESIGN.md`, `STATUS.md`, and `PRE_PUSH_REVIEW.md` before presentation changes or publication.
Preserve generated ownership and obtain independent adversarial review before every feature push.

Use existing Python, asciinema, agg, ffmpeg, ffprobe, and Menlo installations.
Use pinned observer dependencies from `scripts/demos/audio-workbench/setup_observer.py`.
Set `DEMO_OBSERVER_SOURCES` to that verified package directory.
Supply a verified binary and its full source commit.
Keep each output directory empty and outside the repository.
Set `DEMO_SOURCE_STATE` when recording an uncommitted candidate based on that source commit.
Binary hashes and source manifests establish recording identity; they do not establish build provenance alone.

```sh
# Run only during the granted PTY reservation.
DEMO_OBSERVER_SOURCES=/absolute/path/to/verified-observer DEMO_WIDTH=80 DEMO_NO_COLOR=1 \
  bash scripts/demos/limits-workspace/record.sh --run-authorized-pty-slot \
  /absolute/path/to/immutable/openai SOURCE_SHA /absolute/path/to/evidence/limits-80

DEMO_OBSERVER_SOURCES=/absolute/path/to/verified-observer DEMO_WIDTH=40 DEMO_NO_COLOR=1 \
  bash scripts/demos/limits-workspace/record.sh --run-authorized-pty-slot \
  /absolute/path/to/immutable/openai SOURCE_SHA /absolute/path/to/evidence/limits-40
```

Width 80 uses a 24-row workspace. Width 40 uses a 12-row workspace.
The outer recording reserves four additional rows for the evidence label and shell prompt.
`DEMO_NO_COLOR=0` permits a separate color run; monochrome remains the default.
The isolated child environment includes only fake credentials and the fixture endpoint.

| Scene | Scope | Confirmed fixture action |
| --- | --- | --- |
| `org-limit` | Organization | Replace the hard limit with USD 125.25 per month |
| `org-limit-delete` | Organization | Remove the hard limit and retain its confirmed receipt after a missing-record refresh |
| `project-limit` | Project | Replace the hard limit with USD 125.25 and preserve seeded shell scrollback |
| `alert-create` | Organization | Correct an invalid amount, retain the draft through help and Back, then create a USD 75.50 alert |
| `alert-print` | Organization | Print the complete reviewed alert command after terminal restoration; perform no mutation |
| `alert-edit` | Project | Change the alert to USD 150.25 and clear its prefix to null |
| `alert-delete` | Organization | Delete the selected synthetic alert |
| `rate-update` | Project | Set requests per minute to 250 and tokens per day to 9007199254740993; preserve other dimensions |
| `uncertain` | Organization | Apply a synthetic write, lose its response, and verify that repetition stays blocked |
| `slow-cancel` | Organization | Cancel a held read with Ctrl+C; perform no mutation |
| `preflight-recover` | Organization | Fail preflight once, refresh the retained draft, then confirm exactly one write |
| `preflight-400` | Organization | Reject the preflight read, retain the draft, explain recovery, and exit with failure without mutation |

Each mutation scene opens a form or deletion review.
The driver reads to the review's end before selecting Apply.
It checks that the fixture receives no mutation before confirmation.
The create-alert scene checks visible validation errors and complete help text.
Repeated help preserves the draft, prefix mode, and selected Review action.
Escape from review returns to the draft. Help from review clears Apply selection.
The alert scene resizes help below minimum size, then returns through both supported sizes.
Resize clears Apply and requires rereading. The scene inspects the retained amount before a fresh review.
The final request verifies the retained amount, recipient, and prefix.
The uncertain scene refreshes after failure and checks that writes remain locked.
It requires one mutation, retained failure guidance, and exit status 1.
The cancellation scene waits for the fixture's request marker and requires exit status 130.
Successful mutation scenes require exit status 0 and one mutation each.
The print scene uses Ctrl+P and requires exit status 0 with no mutation.
It compares the complete printed command bytes after terminal restoration, including the final newline.
Wrapped inspection rows do not establish command copyability.
The preflight scene loads the overview before returning HTTP 400 from the confirmation read.
It retains the draft, blocks review before refresh, and checks F1 recovery at both widths.
Escape from help returns to the retained draft before another Escape opens the overview.
Normal exit preserves the failure and hides private response data. No mutation occurs.
Final recovery tells users to reopen the workspace and preserve the original request scope.
The recorder rejects workspace keyboard actions in failure diagnostics after terminal restoration.

The fixture stores only synthetic request bodies, response hashes, and method/path/status records.
It never logs request headers or credentials.
Shutdown releases held work and joins the server worker.
The scene driver restores terminal output processing and closes every owned child PTY.

The recorder preserves raw child bytes, child casts, outer casts, request logs, hashes, and actual process statuses.
The validator checks byte identity across all three capture forms.
The child cast ends with an empty output event at the actual observation-end timestamp.
This event preserves observed quiet time without adding terminal bytes or changing checkpoint times.
The validator requires finite, monotonic event times and checkpoints within that observation interval.
It independently replays child bytes and resize events through pinned pyte and wcwidth sources.
The observer answers cursor queries at their actual positions, including fragmented and repeated queries.
The recorder probes query fragmentation, keyboard controls, cursor bounds, and history before recording.
The project scene checks retained scrollback markers in emulator history.
Limits supplies a resizable observer because the shared observer supports fixed audio sizes only.
The driver retains shared PTY allocation and cleanup.
The observer clips during resize; it does not model native terminal reflow.
Checkpoint renderers receive no stdin. The recorder logs their exact names, timestamps, and output filenames.
A second validation pass compares that inventory with source checkpoints and all PNG and frame-GIF filenames.
It rejects missing, duplicate, altered, or unexpected checkpoint artifacts.
It checks exact mutation bodies and guards against automatic retry after an uncertain write.
Terminal captures merge stdout and stderr. They do not prove separate stream routing.

Planned screenshot evidence comes from recorded checkpoints:

- Overview: scope, reported current-month Costs, spend units, returned enforcement, and keyboard controls.
- Form: entered values, retained optional-field semantics, and visible Review action.
- Review top: selected resource and original state.
- Review Apply: explicit final action after scrolling through the complete review.
- Review Print: the Ctrl+P action and the inspection-only command label.
- Printed command: the complete command after terminal restoration, with no mutation.
- Saved state: refreshed resource and confirmed outcome.
- Uncertain state: retained draft and blocked retry guidance.
- Preflight recovery: retained draft, required refresh, HTTP status, and field/scope guidance.
- Loading state: cancellation remains available while a read is pending.
- Final scene frame: restored shell, actual result, and no stale confirmation question.

Inspect every full-terminal PNG and relevant GIF frame at both sizes.
Check clipping, stale rows, focus markers, controls, and final state after exit.
Generated artifacts are terminal replays, not native terminal appearance evidence.
Do not commit binary media or claim these planned scenes ran before execution.
The earlier spend-threshold recorder remains the separate before/after comparison.

The workspace uses an inline panel. The project scene verifies prior shell scrollback.
Selection keeps fixed geometry and font weight.
The panel uses at most 76 columns and 18 rows.
The recorder requires inline readiness, cursor restoration, and restored terminal modes.
Paste disable, keyboard reset, and keyboard-stack restoration must precede printed commands and final diagnostics.
Historical full-screen recordings do not validate this revision.
The rate scene exercises the seventh field and exact tokens-per-day request bytes.
