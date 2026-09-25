# Image-model discovery recordings

`record.sh` compares real CLI binaries using `openai images models --all`
against identical synthetic metadata. The before binary is pinned main, which
does not have this command. The after binary shows exact IDs, visibility,
retirement and dated versions. Separate offline and partial-failure scenes show
unchecked names without a key and useful results after one metadata request
fails. The fixture exposes no image generation route.

This is a real-PTY terminal replay rendered with asciinema 3.2.1 and agg 1.9.0,
then assembled with ffmpeg. It is not native Apple Terminal, PowerShell or
cmd.exe validation. The `.tape` file preserves an alternative VHS recipe;
it is not the renderer used by `record.sh`.

## Reproduce

Build the synthetic server from the repository root:

```sh
mkdir -p dist/demos/bin
go build -o dist/demos/bin/image-model-demo-api ./scripts/demos/image-models/main.go
```

Independently build `openai` at the comparison base and proposed feature commit.
Verify binary provenance before recording: SHA arguments are recorded, but the
runner cannot prove that a binary was built from the supplied commit.
Put `asciinema`, `agg`, `ffmpeg` and `ffprobe` on `PATH`, then run:

```sh
bash scripts/demos/image-models/record.sh \
  /path/to/before/openai /path/to/after/openai \
  "$BEFORE_SHA" "$AFTER_SHA" /path/outside/repository/image-models-media
```

Both commits must be full 40-character IDs. Use main
`c961755b21d579b5b5a2eb87a0e77123038da4a0` for this feature's before build;
PR #238 is a behavior reference, not the comparison baseline.
Use `DEMO_API_BINARY` to select a different build location for the fixture.
The runner deliberately requires media outside the repository. It does not
upload anything. Its temporary symlinks, shell state and local server are
cleaned up on exit.

Each command runs under `env -i` with a temporary PATH exposing the chosen
binary as `openai`, a fake API key, and a loopback URL. The offline scene removes
the key. No user shell hooks, proxy settings or personal CLI environment are
inherited. All four scenes have identical dimensions and styling: 110 columns
by 34 rows, Menlo 22px, Dracula, line height 1.2, and a four-second final pause.

## Fixtures and evidence

`main.go` binds only `127.0.0.1` on an ephemeral port. Its first argument receives
the fixture origin; its second is a new request-log path. Supported base URLs:

- `ORIGIN/normal/v1`: known image IDs return metadata; `chatgpt-image-latest`
  returns 404; DALL-E metadata has the deliberately synthetic retirement date
  `2000-01-01`. These statuses do not represent a live account or API.
- `ORIGIN/partial/v1`: the same data, with one 503 for `gpt-image-1`.
- `ORIGIN/timeout/v1`: the same data, with `gpt-image-1` delayed ten seconds or
  until client cancellation. This optional manual fixture is not in the GIF.

Requests to `/models` and image-generation routes are rejected. The log records
only recognized scenario/model names or a fixed rejection marker; headers,
credentials, arbitrary URLs and bodies are never logged.

`comparison.gif`, `before.png` and `after.png` are the main comparison.
`offline.png` / `offline.gif` and `partial.png` / `partial.gif` are supplemental.
Each scene also has its original `.cast`, a text transcript and a final-frame
GIF. The comparison cast and transcript retain both primary scenes.
`metadata.txt` records supplied commits, binary/recipe hashes, actual statuses,
request counts, OS, shell and tool versions. `media.json` records GIF dimensions
and duration. `requests.txt` retains the synthetic metadata request log.

The runner checks exit statuses (before/partial: 1; after/offline: 0), request
counts (before/offline: zero; after/partial: twelve), and expected output text.
Inspect the resulting PNGs and GIF frames for readable text, clipping, timing
and sensitive data before sharing. Regenerate media after changes to the
demonstrated behavior. Keep binaries and media out of Git.

To use VHS separately, start the fixture, expose before/after directories with
binaries named `openai`, and provide `DEMO_API_URL`, `DEMO_BEFORE_DIR`,
`DEMO_AFTER_DIR` and a synthetic `OPENAI_API_KEY` in an isolated environment.
Run `vhs validate` before rendering the tape from an external output directory.
