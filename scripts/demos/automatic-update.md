# Automatic update confirmation

`record-automatic-update.sh` uses the shared capture lifecycle and existing rejecting API fixture.
It records real baseline and candidate binaries in isolated Bash PTYs.
The candidate uses a synthetic activation receipt matching its exact executable digest.
The recording does not claim a real download, installation, or release upgrade.

Build the baseline from the comparison commit. Build the candidate with GoReleaser's release metadata flags and its exact source commit.
Both binaries must report the same application version for the unchanged-help comparison.
Keep all binaries and captured media outside the repository.

```sh
DEMO_API_BINARY=/absolute/path/image-model-demo-api \
  bash scripts/demos/record-automatic-update.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/automatic-update-demo
```

The recorder requires existing `asciinema`, `agg`, `ffmpeg`, `ffprobe`, and `python3` tools.
Use a new output directory for each recording. The shared lifecycle cleans up its owned processes and temporary homes.

The checks compare complete help transcripts, verify one confirmation, verify silent repetition, and require zero API requests.
The output contains source identities, binary hashes, casts, transcripts, a GIF, screenshots, and validation results.
These artifacts show terminal replay behavior. They do not establish native graphical terminal or font appearance.
