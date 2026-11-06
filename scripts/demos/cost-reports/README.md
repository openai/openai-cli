# Project cost report demo

Run the same command before and after this change. The baseline exits with an unknown command or option.
The candidate reads two pages from a local fixture and prints these exact totals:

```text
proj_alpha  usd  12.34
proj_beta   usd  4.56
```

Use macOS with Bash, Python 3, asciinema 3, agg, ffmpeg, ffprobe, and the Menlo font.
Place these tools on your PATH. Run from the candidate checkout.
Set `BASE_REF` to the comparison commit on main. Both builds and all recordings stay outside the repository.

```sh
BASE_REF=origin/main
baseline=$(git rev-parse "$BASE_REF")
candidate=$(git rev-parse HEAD)
artifacts=$(mktemp -d)
mkdir "$artifacts/baseline"
git archive "$baseline" | tar -xf - -C "$artifacts/baseline"
(cd "$artifacts/baseline" && go build -o "$artifacts/before-openai" ./cmd/openai)
go build -o "$artifacts/after-openai" ./cmd/openai
bash scripts/demos/cost-reports/record.sh \
  "$artifacts/before-openai" "$artifacts/after-openai" \
  "$baseline" "$candidate" "$artifacts/media"
```

Record from a clean candidate checkout, or set `DEMO_AFTER_SOURCE_STATE` to describe local changes.
The recorder uses isolated Bash terminals, fake credentials, and a loopback Costs API.
It checks exit codes, pagination, request parameters, and totals.

Inspect `before.png`, `after.png`, and `comparison.gif` in the media directory.
The images show actual CLI output rendered from recorded terminals. They use 110 columns, 22 rows, and Menlo at 18 pixels.
The directory also contains both terminal recordings, transcripts, file hashes, and request logs.

For a lossless two-frame GIF of the screenshots, use Python with Pillow:

```sh
python3 scripts/demos/cost-reports/assemble_snapshot_gif.py \
  "$artifacts/media/before.png" "$artifacts/media/after.png" \
  "$artifacts/media/comparison-snapshots.gif"
```
