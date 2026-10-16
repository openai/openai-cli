# Local profile recording

This scene compares an accepted A1 binary against the A2 candidate.
The baseline lacks profile commands and returns status 3 for `profile list`.
The candidate creates, edits, activates, switches, and inspects a synthetic profile.
`auth status` shows the resulting effective project and profile source.
All scene commands run offline.

Use the existing [`capture_and_render.sh`](../capture_and_render.sh) lifecycle.
Use the loopback fixture from [`project-linking/server.py`](../project-linking/server.py) as an unexpected-request trap.
Reuse the recorder structure from [`project-linking/record-context-review.sh`](../project-linking/record-context-review.sh).
Do not add another recorder lifecycle or commit binary media.

Prepare an external recorder with these settings:

- Supply independently built baseline and candidate binaries, with full commit IDs.
- Copy this `scene.sh` to the shared lifecycle's temporary runtime directory.
- Use isolated homes for `HOME`, `USERPROFILE`, `APPDATA`, and `XDG_CONFIG_HOME`.
- Supply `DEMO_SCENE`, `DEMO_SCENE_LABEL`, `DEMO_PLATFORM`, `DEMO_WORK_ROOT`, and `DEMO_STATUS_LOG` for each scene.
- Use `env -i` through `demo_capture_scene` to exclude personal settings and credentials.
- Set `NO_COLOR=1`, `FORCE_COLOR=0`, `CI=1`, `PAGER=cat`, and `GOMAXPROCS=2`.
- Capture Bash at 120 columns and 40 rows.
- Render Menlo at 18 pixels with the asciinema theme.
- Keep all captures, screenshots, binaries, and manifests outside the repository.

Use the current bounded PTY resource claim before recording.
Native foreground access requires its separate user window.
This recipe does not establish native graphical-terminal behavior.

The external recorder must assert these exact command statuses:

```text
before list 3
after create 0
after set 0
after activate 0
after switch 0
after status 0
after inspect 0
```

The scene writes tab-separated status rows to `DEMO_STATUS_LOG`.
Assert that the trap's request log contains no requests.
Check the candidate transcript for `proj_other`, `profile:work`, and the saved-default label.
Check that create returns `active:false` and activate returns `active:true`.
Check that no credential value appears in either transcript.
Record binary hashes, source identities, script hashes, actual statuses, and environment information.
Preserve a source manifest when the candidate includes uncommitted changes.

Inspect `before.png`, `after.png`, and `comparison.gif` before sharing.
Check complete output and clipping at the recorded dimensions.
Keep rendering evidence separate from native interaction and platform validation.
