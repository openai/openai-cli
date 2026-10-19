# Limits workspace terminal replay

This recipe drives an immutable CLI binary against a synthetic loopback API.
It makes no live API calls or billing changes.
The recorder uses the shared `capture_and_render.sh` lifecycle and existing PTY harness.
No Go build occurs during recording.

Obtain the source-build and PTY reservations through the current coordinator.
Read `/Users/vguvvala/code/cli-work/DESIGN.md`, `STATUS.md`, and `PRE_PUSH_REVIEW.md` before presentation changes or publication.
Preserve generated ownership and obtain independent adversarial review before every feature push.

Use existing Python, asciinema, agg, ffmpeg, ffprobe, and Menlo installations.
Supply a verified binary and its full source commit.
Keep each output directory empty and outside the repository.
Set `DEMO_SOURCE_STATE` when recording an uncommitted candidate based on that source commit.
Binary hashes and source manifests establish recording identity; they do not establish build provenance alone.

```sh
# Run only during the granted PTY reservation.
DEMO_WIDTH=80 DEMO_NO_COLOR=1 \
  bash scripts/demos/limits-workspace/record.sh --run-authorized-pty-slot \
  /absolute/path/to/immutable/openai SOURCE_SHA /absolute/path/to/evidence/limits-80

DEMO_WIDTH=40 DEMO_NO_COLOR=1 \
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
| `project-limit` | Project | Replace the hard limit with USD 125.25 per month |
| `alert-create` | Organization | Create a USD 75.50 monthly alert with synthetic email settings |
| `alert-edit` | Project | Change the alert to USD 150.25 and clear its prefix to null |
| `alert-delete` | Organization | Delete the selected synthetic alert |
| `rate-update` | Project | Change requests per minute to 250; preserve all other dimensions |
| `uncertain` | Organization | Apply a synthetic write, lose its response, and verify that repetition stays blocked |
| `slow-cancel` | Organization | Cancel a held read with Ctrl+C; perform no mutation |

Each mutation scene opens a form or deletion review.
The driver reads to the review's end before selecting Apply.
It checks that the fixture receives no mutation before confirmation.
The uncertain scene checks one mutation, retained failure guidance, and exit status 1.
The cancellation scene waits for the fixture's request marker and requires exit status 130.
Successful scenes require exit status 0 and one mutation each.

The fixture stores only synthetic request bodies, response hashes, and method/path/status records.
It never logs request headers or credentials.
Shutdown releases held work and joins the server worker.
The scene driver restores terminal output processing and closes every owned child PTY.

The recorder preserves raw child bytes, child casts, outer casts, request logs, hashes, and actual process statuses.
The validator checks byte identity across all three capture forms.
It checks exact mutation bodies and guards against automatic retry after an uncertain write.
Terminal captures merge stdout and stderr. They do not prove separate stream routing.

Planned screenshot evidence comes from recorded checkpoints:

- Overview: scope, reported current-month Costs, spend units, returned enforcement, and keyboard controls.
- Form: entered values, retained optional-field semantics, and visible Review action.
- Review top: selected resource and original state.
- Review Apply: explicit final action after scrolling through the complete review.
- Saved state: refreshed resource and confirmed outcome.
- Uncertain state: retained draft and blocked retry guidance.
- Loading state: cancellation remains available while a read is pending.
- Final scene frame: restored shell, actual result, and no stale confirmation question.

Inspect every full-terminal PNG and relevant GIF frame at both sizes.
Check clipping, stale rows, focus markers, controls, and final state after exit.
Generated artifacts are terminal replays, not native terminal appearance evidence.
Do not commit binary media or claim these planned scenes ran before execution.
The earlier spend-threshold recorder remains the separate before/after comparison.
