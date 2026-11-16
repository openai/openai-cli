# Fine-tuning removal demo

Build CLI binaries from the comparison base and candidate commits.
Use the shared replay tools described in [the demo guide](../README.md).

```bash
bash scripts/demos/fine-tuning-removal/record.sh \
  /path/to/before/openai /path/to/after/openai \
  "$BASE_SHA" "$CANDIDATE_SHA" /path/to/empty/evidence
```

Both scenes execute `openai fine-tuning --help` in isolated Bash PTYs.
The baseline lists commands and exits 0. The candidate rejects the help topic and exits 3.
The script uses no API fixture or live credentials.
It captures a terminal replay, not a native graphical terminal.
Inspect both PNGs and the comparison GIF before publication.
