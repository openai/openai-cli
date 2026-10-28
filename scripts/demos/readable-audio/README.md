# Readable audio demo

Synthetic loopback transcription SSE. No real audio, credentials or paid API call.
`main.go` accepts the fixed fixture and records only its validated synthetic request.
The recorder creates `sample.wav` with marker text, never production audio.

Build the comparison parent and candidate from the recorded commit IDs, then:

```sh
go build -o dist/demos/bin/audio-demo-api ./scripts/demos/readable-audio
PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/readable-audio/record.sh \
  /absolute/before/openai /absolute/after/openai \
  BEFORE_FULL_SHA AFTER_FULL_SHA /absolute/new/evidence-directory
```

The script requires asciinema 3.2.1, agg 1.9.0, ffmpeg, ffprobe and Python 3.
It checks actual command exit codes, original JSONL, matching synthetic requests,
transcript deduplication, metadata and delayed output. The GIF and screenshots
come from actual binaries in real PTYs, replayed with Menlo/Dracula at 90×30.
They do not establish native Apple Terminal or Windows behavior.

The recorder reuses `../capture_and_render.sh` for setup, fixture lifecycle,
capture and rendering. Audio scenes and assertions stay here. Use an empty
output directory outside the repository. The fixture must shut down and close
its request log successfully before validation and comparison assembly.

`readable-audio.tape` is a reusable alternative VHS recipe. It needs the same
loopback server, fake environment, `sample.wav` fixture, and before/after PATH
folders. The verified local renderer is asciinema + agg.

Keep casts, PNGs, GIFs, request logs and hashes outside Git. Inspect the images
and GIF timing after recording. Attach media only when publication is authorized.
