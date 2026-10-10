# Transcript timestamp terminal replay

The recipe uses the existing `scripts/demos/capture_and_render.sh` lifecycle.
It supplies a Python fixture instead of building a Go fixture.
The fixture validates a fake audio marker, model, response format, chunking strategy, and fake authorization.
It listens only on loopback.
The fixture records validated synthetic fields and response hashes.
It closes the request log before a successful SIGTERM exit.
Invalid requests or evidence-write failures make fixture shutdown fail.

Both scenes use the same finite response and command.
The response contains two speaker segments and the complete aggregate transcript.
Segment IDs, types, duration, and task exercise retained details.
The after scene requires both timestamped sentences exactly once.
The recipe records actual command exits through the shared lifecycle.
It verifies source and binary hashes after capture.
It preserves requests, scene source, metadata, and validation output outside Git.

The baseline source is `e68939820415144d769ed02de6aa72d5b7d32948`.
The caller must supply the actual candidate SHA and binaries.
Use `DEMO_AFTER_SOURCE_STATE` to identify any uncommitted source precisely.
Do not label such a recording as a committed-candidate recording.

Run the recipe with existing before and after binaries:

```sh
bash scripts/demos/transcript-output/record.sh \
  /absolute/before/openai /absolute/after/openai \
  e68939820415144d769ed02de6aa72d5b7d32948 AFTER_FULL_SHA \
  /absolute/new/evidence-directory
```

When running a copied recipe outside the repository, set `DEMO_REPO_ROOT` to the repository path.
Keep `server.py` executable because the shared lifecycle invokes it directly.
The recorder accepts exactly five arguments, matching the existing readable-audio recorder.
It requires Python 3, asciinema 3.2.1, agg 1.9.0, ffmpeg, and ffprobe.
It renders 90×30 terminal replays with NO_COLOR=1.
The recipe does not install tools or build the CLI.

Inspect the GIF and screenshots after capture.
The recordings prove synthetic CLI behavior on the execution host.
They do not prove native Terminal app appearance, other platforms, or live API acceptance.
No microphone, real audio, paid API call, installation, or publication occurs.
Keep binary media outside Git.
