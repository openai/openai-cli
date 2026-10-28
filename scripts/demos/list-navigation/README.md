# List navigation evidence

Use the current [list pager recipes](../list-pager/README.md) for navigation recordings and public-command checks.
Use the [list table recipe](../list-tables/README.md) for Files, Batches, and Projects width comparisons.
Both reuse the shared capture lifecycle and synthetic loopback fixtures.

Handwritten command assembly activates Files, Batches, and Projects without changing generated declarations.
Models also uses the shared pager for eligible automatic terminal lists.
Short, empty, and final results remain open until `q` restores the original screen.
Space advances through loaded chunks before requesting another API page.
`b` returns to cached chunks without another request.
The pager ignores `i` and `p`; it does not print successful results when quitting.
API errors preserve received records after restoring the terminal.
Explicit formats, extraction, redirected output, CI, and dumb terminals retain their existing paths.

The current navigation checker dispatches the list pager observer.
Run it from the repository root with a candidate binary and its comparison baseline:

```sh
python3 -I -B scripts/check-list-navigation.py \
  /absolute/path/to/openai-after /absolute/path/to/new-checks \
  --baseline /absolute/path/to/openai-before \
  --asciinema /absolute/path/to/asciinema
```

The output directory must not exist.
For focused Models checks, append `--case models-short-final --case models-loaded-chunks`.
Omit `--case` to run all current observer cases.
The observer preserves binary hashes, raw output, casts, keys, requests, statuses, and terminal-restoration evidence.

The older fixture, driver, validator, and `scripts/demos/record-list-navigation.sh` remain historical recording tools.
Their eager-output baseline and former navigation contract do not match the current pager-only behavior.
Do not use that recorder to establish current candidate behavior.
Historical recordings retain their original source and binary identities.

Keep recordings outside the repository.
Use pinned binaries, fake credentials, isolated configuration, and private PTYs.
Inspect screenshots and GIFs before sharing them.
These recordings establish terminal replay, not native graphical terminal appearance or clipboard behavior.
