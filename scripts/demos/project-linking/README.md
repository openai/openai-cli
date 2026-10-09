# Folder project linking recording

This recorder compares two real CLI binaries using one synthetic loopback Files API.
It uses the shared `scripts/demos/capture_and_render.sh` lifecycle.
It creates temporary `openai` PATH entries and separate configuration homes.
It does not change the installed CLI.

The baseline attempts `openai link --project proj_work` and must return status 3.
The candidate links `work-chatbot`, inspects the link, lists remote files, and removes the link.
The synthetic API returns `file_remote_proj_work` only when it receives the expected project header.
These results represent remote OpenAI files, not files in `work-chatbot`.
Project selection does not change the API key or grant project access.

Wait for the coordinator's PTY reservation before recording.
Provide independently built binaries and their full source commit IDs.
Put `python3`, `asciinema`, `agg`, `ffmpeg`, and `ffprobe` on PATH.
Use an empty output directory outside the repository.

```sh
bash scripts/demos/project-linking/record.sh \
  /absolute/evidence/baseline-openai \
  /absolute/evidence/candidate-openai \
  BASE_COMMIT CANDIDATE_COMMIT \
  /absolute/evidence/project-linking-media
```

Set `DEMO_SOURCE_MANIFEST` when the candidate requires an additional source manifest.
The recorder preserves both commit IDs, binary hashes, script hashes, request evidence, and real command statuses.
The fixture stores only fixed synthetic request paths, project IDs, and file IDs.
It never logs credentials or other request headers.
SIGTERM starts fixture shutdown on a separate thread.
The shared lifecycle waits for shutdown and treats fixture failure as recording failure.

The capture uses Bash at 100 columns and 26 rows.
The replay uses Menlo at 18 pixels with the asciinema theme.
Each scene identifies its platform and synthetic remote Files API.
macOS recordings are terminal replays, not native graphical terminal validation.
The fixture does not establish live API access or backend authorization behavior.

Inspect `before.png`, `after.png`, and `comparison.gif` before sharing.
Check complete command output, file IDs, and terminal clipping.
Keep GIFs, screenshots, casts, and binaries outside Git.
