# Models list demo

This recipe compares metadata output with sorted model IDs in a bounded viewer.
Both binaries run `openai models list` against the same 120 synthetic records.
The baseline has no model viewer hook.
The candidate displays sorted model IDs in a bounded viewer.
The fixture reverses model order and alternates shutdown dates.
No private model IDs or live credentials enter the recording.

The baseline prints all 120 IDs and their metadata, then exits without keyboard input.
The scene asserts response order, metadata counts, terminal overflow, and absence of viewer controls.
The candidate shows exact ascending IDs and the hint `p: print all 120 records, quit`.
Only the candidate receives Space, b, then q.
The scene checks complete expected ID lines before and after navigation, bounded output, and terminal restoration.
Both commands must exit successfully and make one HTTP request.
The recorder verifies identical raw, child-cast, and outer-cast bytes.
It also checks unchanged source and binary hashes during recording.
The p action retains complete labels; this scene does not press p.

Use existing asciinema, agg, ffmpeg, ffprobe, and Python installations.
Keep output outside the repository in a new directory.
Use full source commit IDs.
Each source SHA must identify its corresponding binary.
Set `DEMO_AFTER_SOURCE_STATE` explicitly when recording an uncommitted candidate from the supplied source commit.
Metadata records that state, binary hashes, and current runtime source hashes.

```sh
PATH=/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH \
  bash scripts/demos/models-list-viewer/record.sh \
  /path/to/before-binary /path/to/after-binary \
  BEFORE_SHA AFTER_SHA /path/outside/repository/demo
```

Both child terminals use 110 columns and 27 rows.
The outer recording includes a synthetic-data label and uses 110 columns and 36 rows.
Temporary homes and an explicit environment isolate credentials and shell configuration.

The renderer uses agg swash with Menlo, 18px text, and the Dracula theme.
Outputs include complete-terminal PNGs, individual GIFs, and a comparison GIF.
The additional `before-output.png` shows the baseline's final terminal contents after all metadata records print.
The additional `after-viewer.png` shows the loaded candidate viewer before q restores the shell.
The baseline can scroll the initial demo label out of view; its raw output remains intact.
Evidence includes binary/tool/source hashes, fixture metadata, request logs, and exact child output.
Terminal captures merge stdout and stderr; they do not establish separate stream routing.
Inspect every delivered PNG and GIF frame before claiming visual success.
These artifacts show terminal replay, not native terminal appearance.
Keep rendered media outside Git.
