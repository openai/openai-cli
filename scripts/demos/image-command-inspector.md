# Images command-inspector recording

Build both source revisions before recording. Keep generated media outside the repository.

```sh
scripts/demos/record-image-command-inspector.sh \
  "$PWD" /path/to/before-openai /path/to/after-openai \
  BASE_COMMIT CANDIDATE_COMMIT /path/to/empty-output
```

The recorder requires Python 3, asciinema, agg, ffmpeg, and ffprobe on PATH.
It reuses the shared capture lifecycle and `image-picker-layout/drive_picker.py` for actual CLI output.
It does not run the broad image-picker test harness.

Each scene uses an isolated HOME and the binary's actual model defaults.
The prompt contains synthetic numbered scenes between `FIRST_VIEW` and `LAST_VIEW`.
The scene selects View command, scrolls three rows, presses Tab, then presses Shift+Tab.
The baseline must jump to the command's last page. The candidate must preserve all 16 visible command rows.
Returning to the editor must restore the original rows in both versions.

No API fixture starts. The scene never selects Generate, and update checks are disabled.
A fake credential and loopback-only fallback endpoint prevent access to live API data.
Assertions require no generation status or saved images, exit status 130, and restored terminal settings.
The scene closes its CLI process before removing its temporary HOME.

`comparison.gif` labels both scenes. `before.png` and `after.png` show the command immediately after Tab.
The output retains casts, transcripts, checkpoint reports, source hashes, and binary hashes.
The recording uses private PTYs. It does not establish physical-terminal appearance.
