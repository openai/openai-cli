# Vector-store search demo

This fixture returns a successful search with no matches. It does not inspect indexing or call a live API.

Prerequisites: Python 3, Bash, asciinema, agg, FFmpeg, and two CLI binaries.

```sh
scripts/demos/vector-store-search/record.sh \
  /absolute/path/to/before/openai /absolute/path/to/after/openai \
  BEFORE_FULL_COMMIT AFTER_FULL_COMMIT /absolute/path/to/empty-evidence-directory
```

Use `DEMO_WINDOW_SIZE=40x14` for narrow output. Use `DEMO_THEME=github-light` for the light replay.
The recorder uses the shared capture lifecycle. It checks real exit statuses and captured output.
It removes its temporary files and stops its loopback server on exit. Keep GIFs and screenshots outside Git.
Recordings are terminal replays. They do not establish graphical terminal or live service acceptance.

For authorized live indexing checks, use the [live acceptance recipe](../../../docs/guides/vector-store-search.md#live-acceptance-recipe).
It uploads synthetic text, checks typed attributes and filtered searches, and deletes only resources created during that run.
Live execution and possible charges require separate authorization. The recording fixture does not run that recipe.
