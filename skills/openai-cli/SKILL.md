---
name: openai-cli
description: Use the OpenAI API from a shell to upload files, run batches, transcribe audio, save generated images, or export API data. Use for one-off API operations and shell workflows.
---

# OpenAI CLI

Check `openai --version`. Older Python SDK installations can provide a different `openai` executable.
Search offline with a few keywords, such as `openai explore transcribe audio`. Search returns text.
Read the matching command's help before choosing an operation. Check whether it uploads data, starts remote work, or writes files.

## Work with a batch request file

Choose the upload purpose from the user's intended use. If that use is unclear, ask before uploading.

- **Upload for later.** `openai files upload requests.jsonl --purpose batch --format json` returns file metadata. Read its `id` and stop after upload.
- **Submit now.** `openai batches submit requests.jsonl` uploads the file and creates a batch. Add `--wait --output-dir ./results` when the user wants the result files.
- **Resume.** For an existing batch, use `openai batches wait BATCH_ID --output-dir ./results`.

If an upload might have succeeded, check `openai files list` in the same project before retrying.
If batch creation might have succeeded, check `openai batches list`. Reuse confirmed IDs to avoid duplicate uploads or submissions.

## Choose output for the next step

For API metadata, request `--format json` explicitly. Use `--format jsonl` for list records. Piping the default output still produces readable text.
For an image file, use the command's save options. Requesting JSON returns the API payload and bypasses automatic image saving.
Check the process exit status before consuming redirected output. A failed stream or later page can leave partial data.

Use the workload's configured credentials. For untrusted JSON or YAML on stdin, set `OPENAI_UNTRUSTED_STDIN=1` to prevent file reads from that input.
