# Vector-store workspace terminal checks

This recipe drives the real CLI through an owned PTY.
It uses a loopback HTTP fixture and synthetic files.
It inherits no credentials, proxy settings, or user configuration.
It performs no live API requests.

Prerequisites: Python 3.10 or newer and a previously built CLI binary.
The recipe uses Python's standard library and the repository's shared PTY harness.
It installs nothing and does not build the CLI.
The checker rejects Python's `-O` and `-OO` modes before creating resources.

Keep evidence outside the repository.
Use an immutable binary and record its complete source commit.
Use `--source-state dirty` for an uncommitted candidate.
The report also records hashes for the binary, harness, and vector-store sources.

```sh
python3 -I -B scripts/demos/vector-store-workspace/check.py \
  --binary /absolute/path/to/openai \
  --source "$(git rev-parse HEAD)" \
  --source-state clean \
  --output /absolute/path/to/new-evidence-directory
```

Use `--theme dark` or `--theme light` to capture color rendering.
The default `--theme mono` checks output without color.
Theme captures use synthetic terminal replies, not a graphical terminal application.

Use `--case NAME` to rerun one affected case:

- `group-help`: the bare command prints help and exits without keyboard input or API requests.
- `workflow`: store selection, store creation, file browser, upload, indexing, file list, search, passage, and back.
- `search` and `search-narrow`: type, search once, select a second passage, edit the query, and retry an empty result at 90×34 and 40×12.
- `upload-failure`: failed upload, explicit retry, indexing, and search.
- `attachment-failure`: retained uploaded file, remote-state check, explicit attachment retry, indexing, and search.
- `tiny`: mutation blocking below 40 columns or 12 rows, followed by resize and cancellation.
- `ctrl-c`: keyboard cancellation while idle.
- `sigterm`: process cancellation while idle.
- `busy-ctrl-c`: keyboard cancellation during a held HTTP response.
- `busy-sigterm`: process cancellation during a held HTTP response.
- `management`: store filtering, renaming, expiration, metadata, existing files, custom chunking, batches, content, attributes, and confirmed removal.
- `management-failure`: failed renaming retains store state, sends no automatic retry, and exits with failure.

All workspace scenarios start with `openai vector-stores manage`.
The fixture checks project, organization, custom headers, upload purpose, exact file bytes, and search input.
Mutation cases send repeated Enter keys.
Request counts must show one attempt per user action.
Upload failure cases permit one explicit retry.
Attachment retries must retain the uploaded file ID.
The attachment failure returns 404 during the manual retry's remote-state check.
The next read verifies successful indexing after attachment.
Management checks verify expiration clearing, exact deletion confirmations, source-file preservation, and retained completed batch counts.
They also reopen the latest batch without requiring another ID entry.

The shared harness verifies exit codes, terminal input restoration, cursor restoration, and bracketed-paste restoration.
The owned PTY has no controlling-session attachment, preserving Darwin's post-exit terminal attribute checks.
It rejects alternate-screen use and screen or scrollback clearing.
The recipe also checks process-group cleanup and server-thread cleanup.
Each case writes a `.raw` capture and an asciinema `.cast` recording.
The final `report.json` records requests, source hashes, results, and cleanup outcomes.

These checks establish native process behavior against a synthetic API.
They do not establish live API acceptance or graphical terminal appearance.
Text assertions inspect newly emitted bytes, not an emulator's current viewport.
Use separate rendered evidence for layout and font review.

## Isolated local preview API

`preview_api.py` provides session-local stores, files, batches, and seeded sample text.
It binds an ephemeral loopback port and makes no outbound requests.
Search uses word overlap. Uploads accept UTF-8 text up to 1 MiB each and 8 MiB per session.
These preview limits do not change the CLI or public API contract.
Data remains in memory. The fixture logs no requests or uploaded content.

Import `create_server()` and own its serving thread, shutdown, and close.
Use the explicitly fake `PREVIEW_KEY` when a client needs an API key.
Standalone use requires `--parent-fd` with an inherited pipe; EOF or a signal stops the fixture.

Run the focused fixture checks locally:

```sh
python3 -B scripts/demos/vector-store-workspace/preview_api_test.py
```
