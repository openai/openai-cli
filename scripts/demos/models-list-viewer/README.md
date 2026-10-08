# Models list demo

This recipe compares an ID-only list with exact ID and OWNER columns.
Both binaries run `openai models list` against the same 48 synthetic records.
The fixture contains short IDs, three owners, and descending response order.
Both viewers sort the IDs and keep output within the terminal.
No private model IDs or live credentials enter the recording.

At 110 columns, the candidate shows ID and OWNER columns.
At 40 columns, it uses complete `ID:` and `Owned by:` labels without shortening either value.
Both comparison scenes receive Space, b, then q.
The scene checks exact ID/owner pairing, field preservation, navigation, print-all count, and terminal restoration.
Every command must exit successfully and make one HTTP request.
The recorder verifies identical raw, child-cast, and outer-cast bytes.
It also checks unchanged source and binary hashes during recording.
The p action retains complete labels; this scene does not press p.

The 110-column run adds one candidate loading scene.
Its loopback fixture holds the response until `Loading models` appears.
The scene retains that pending state for 1.2 seconds before releasing the same response body.
Assertions require loading feedback before model output and its removal from the loaded viewer.
That scene quits with q after verifying the result.

Use existing asciinema, agg, ffmpeg, ffprobe, and Python installations.
Keep output outside the repository in a new directory.
Use full source commit IDs.
Each source SHA must identify its corresponding binary.
Set `DEMO_AFTER_SOURCE_STATE` explicitly when recording an uncommitted candidate from the supplied source commit.
Metadata records that state, binary hashes, and current runtime source hashes.

```sh
DEMO_WIDTH=110 PATH=/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH \
  bash scripts/demos/models-list-viewer/record.sh \
  /path/to/before-binary /path/to/after-binary \
  BEFORE_SHA AFTER_SHA /path/outside/repository/demo-110

DEMO_WIDTH=40 PATH=/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH \
  bash scripts/demos/models-list-viewer/record.sh \
  /path/to/before-binary /path/to/after-binary \
  BEFORE_SHA AFTER_SHA /path/outside/repository/demo-40
```

The selected width applies to the child and outer terminal.
Child terminals use 26 rows; outer recordings use 36 rows for labels and the final prompt.
Temporary homes and an explicit environment isolate credentials and shell configuration.

The renderer uses agg swash with Menlo, 18px text, and the Dracula theme.
Outputs include complete-terminal PNGs, individual GIFs, and a comparison GIF.
Each `*-viewer.png` shows the loaded viewer before q restores the shell.
The wide run also includes `loading-pending.png`, selected before the fixture releases its response.
Evidence includes binary/tool/source hashes, fixture metadata, request logs, and exact child output.
Terminal captures merge stdout and stderr; they do not establish separate stream routing.
Inspect every delivered PNG and GIF frame before claiming visual success.
These artifacts show terminal replay, not native terminal appearance.
Keep rendered media outside Git.
