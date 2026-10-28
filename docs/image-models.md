# Finding image model names

`openai images models` lists exact image model IDs known to this CLI's SDK and
checks each model's metadata with your configured credentials and endpoint.
It does not generate images or download the full account model list.

```sh
openai images models                         # check known aliases
openai images models --all                   # also check dated versions; show every result
openai images models --offline               # known aliases, no key or network required
openai images models --offline --all         # all known IDs, unchecked
openai --format json images models           # structured report, including hidden rows
openai --transform 'models.0.id' --raw-output images models --offline
```

Use the complete ID with the existing generation command:

```sh
openai images generate --prompt "A tiny orange robot" --model gpt-image-2.5-flare
```

This command does not set a CLI default. The catalog comes from the SDK version
bundled with the CLI and may not contain newly released models. It is not an
exhaustive inventory of the models available to your account.

Checks run at most three requests at a time, with a five-second deadline per
request, a 15-second overall deadline, and no retries. Authentication rejection
or rate limiting stops new checks; already running checks can finish. Ctrl-C
cancels outstanding requests and retains completed results.

Readable output hides retired and not-visible models unless `--all` is set.
Failed checks remain visible. Structured formats always include every checked
catalog entry; `--all` adds the known dated versions to that catalog. Existing
format and extraction flags work on the report.

The report has `source` (`live` or `offline`), `complete`, and `models`. Each
model has its exact `id`, `snapshot` flag, and `status`:

- `visible`: model metadata was accessible. Generation permissions, quota and
  supported image options can still differ.
- `not_visible`: the metadata endpoint returned 404 for this key. This does not
  establish whether the model is retired or available to another account.
- `retired`: the metadata shutdown date is today or earlier in UTC.
- `unknown`: a check failed or could not run. `failure` gives a safe category,
  such as `authentication`, `rate_limit`, `timeout`, or `canceled`.
- `not_checked`: offline catalog entry; no visibility claim is made.

A non-null valid shutdown date appears as `shutdown_date`, including future
retirement dates. No raw API error text or account metadata is put in this report.

Complete live checks exit zero, even when every model is retired or not visible.
Incomplete live checks print partial results, explain the failure on stderr, and
exit nonzero. Offline discovery exits zero with `complete: false`. A failed
output write also exits nonzero. The existing `models list` command is unchanged.
