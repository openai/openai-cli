# Project rate limits

List the project's model limits with an authorized administrator credential configured for the CLI:

```sh
openai admin projects rate-limits list --project-id proj_example
```

Use the returned rate-limit ID for an approved update:

```sh
openai admin projects rate-limits update --project-id proj_example --rate-limit-id rl_example --max-tokens-per-1-minute 1000
```

The existing `list-rate-limits` and `update-rate-limit` names remain available.
The colon route, `admin organization projects`, and `projects` shortcut support both names.
Readable output labels each rate's units and preserves returned model names and IDs.
Explicit `--format json`, `jsonl`, `yaml`, `raw`, `--transform`, and `--raw-output` keep their existing contracts.
`--limit`, `--after`, `--before`, and `--max-items` retain their pagination behavior.

Supported update fields cover requests/minute, tokens/minute, requests/day, images/minute, audio megabytes/minute, and batch input tokens/day.
Batch input tokens/day is distinct from general tokens/day.
The public update contract does not expose `--max-tokens-per-1-day` or a rate-limit reset operation.
Omitted fields remain unchanged. Do not use zero or null as a reset shortcut.
The list response does not identify which values came from organization inheritance.

Model permissions already support `retrieve`, `update`, and `delete`.
Hosted-tool permissions support `retrieve` and `update`.
Deleting a model policy does not promise unrestricted access or reset rate limits.
Other organization and system restrictions still apply.
Use each command's `--help` to inspect its supported fields before an approved change.

[Rate-limit contract](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/rate_limits/methods/update_rate_limit)

## Synthetic recording

Build the baseline and candidate into separate local executables.
Use the shared asciinema/agg capture tools and keep media outside Git:

```sh
PATH="$HOME/.cache/cli-terminal-replay/bin:$PATH" \
  scripts/demos/project-rate-limits/record.sh \
  /tmp/f32-before/openai /tmp/f32-after/openai \
  BASE_SHA CANDIDATE_SHA /tmp/f32-demo
```

The recorder starts a loopback-only synthetic API and checks process statuses and visible results.
It uses temporary homes and a fake key. It stops its own server and removes temporary state.
`DEMO_SIZE=40x36` records narrow output. `DEMO_THEME=solarized-light` selects a light replay theme.
These recordings do not establish live API acceptance or native graphical-terminal behavior.
