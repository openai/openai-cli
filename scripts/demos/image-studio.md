# Images studio recording

Use the shared capture lifecycle with two previously built CLI binaries.
Keep generated media outside the repository.

```sh
scripts/demos/record-image-studio.sh \
  "$PWD" /path/to/before-openai /path/to/after-openai \
  BASE_COMMIT CANDIDATE_COMMIT /path/to/empty-output
```

The recorder requires Python 3, asciinema, agg, ffmpeg, and ffprobe on PATH.
It reuses `image-picker-layout/drive_picker.py`, the recovery fixture, and the existing synthetic PNG recipe.
The scene starts `openai images generate` with isolated settings and a fake credential.
Only the loopback fixture receives requests. No image viewer or font registration runs.

Both scenes show the same prompt, settings, and three synthetic saved images.
The candidate additionally switches panes and selects the second image.
Assertions verify the request, saved bytes, pane navigation, exit status, and terminal restoration.

`comparison.gif` contains both scenes. `before.png` and `after.png` show their actual result checkpoints.
The output also retains casts, transcripts, binary hashes, checkpoint reports, and capture metadata.
This is a color-block terminal replay. It does not establish native graphical-terminal appearance.
