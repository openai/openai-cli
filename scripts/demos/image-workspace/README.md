# Images workspace comparison

This recorder captures actual CLI output through the existing asciinema workflow.
It uses synthetic loopback responses and a deterministic geometric PNG.
It never calls the image service or reads personal credentials.

The comparison uses macOS PTYs at 110 columns and 38 rows.
Both binaries receive the same prompt, model, image size, quality, and count.
The baseline generates one batch. The candidate also scrolls images and generates another batch.
The second batch must retain the previous images during generation.

Build immutable baseline and candidate binaries before recording.
Use the project's resource coordination for the bounded PTY capture.
Add the existing asciinema and agg installation to PATH.
Then run:

```sh
PATH="$HOME/.cache/cli-terminal-replay/bin:$PATH" bash scripts/demos/record-image-workspace.sh \
  /path/to/openai-cli /path/to/before/openai /path/to/after/openai \
  BASE_COMMIT CANDIDATE_COMMIT /path/to/new-empty-media-directory
```

The script requires Bash, Python 3, asciinema, agg, ffmpeg, and ffprobe.
The macOS render uses Menlo with Apple Symbols for the braille spinner.
Keep binary media outside Git.
Commit the recorder and this recipe alongside the existing demo helpers.

`comparison.gif` contains labeled before and after scenes.
`before.png` and `after.png` show the filled form before generation.
Both form screenshots use the same crop, removing only empty terminal rows below the controls.
Additional images show results, gallery scrolling, and retained images during generation.
The checkpoint casts preserve exact PTY bytes without rewriting the displayed interface.
Metadata records commits, binary hashes, tool versions, dimensions, and fixture hashes.
Each scene report records request counts, saved files, checkpoints, and exit status.

The GIF uses generic terminal color previews.
It demonstrates layout and interaction, not native Apple Terminal font-backed image quality.
Native terminal image claims require separate native evidence.
The recorder's short title cards identify synthetic fixtures. They are outside the CLI output.
