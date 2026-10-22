# Webhook workflow terminal replays

This recipe compares published webhook test output with recovery guidance in the expanded workflow.
Use published commit `5351ddb021263896da3a6871b621e19d38e8ffe9` for the before binary.
Both binaries receive identical synthetic test results: API HTTP 200, `success: true`, and receiver HTTP 500.
Both binaries report the same delivery failure and exit zero.
The candidate prints the submitted endpoint and event before the result.
It identifies the project when available, or explains that the API key determines the project.
The candidate adds receiver advice, an inspection command, and a retry command on stderr.

Two additional candidate scenes show event discovery and guided creation.
The catalog contains four synthetic event names across three groups.
The guided scene edits the name, URL, and events in one visible form, then opens final review.
It searches for response events and selects both matches with Ctrl+A.
It checks an empty search, then restores the search and both selections.
It returns from review to event selection with Shift+Tab, then opens review again.
The review initially selects No.
The compact panel retains terminal scrollback and keeps Continue to confirmation inside the form.
Tall terminals do not stretch the panel.
The driver chooses Yes before pressing Enter.
The fixture verifies both subscriptions and rejects creation before confirmation or duplicate creation.
The response contains a clearly fake signing secret.
The fixture never contacts the receiver URL.

## Reproduce

Use existing Python, asciinema, agg, ffmpeg, and ffprobe installations.
Build the candidate independently.
Verify each binary's source identity before recording.
The recipe records supplied commits and hashes but cannot establish their relationship.
Set `DEMO_AFTER_SOURCE_STATE` when the candidate contains uncommitted changes.

Run each width with a fresh output directory outside the repository:

```sh
DEMO_WIDTH=80 PATH=/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH \
  bash scripts/demos/webhook-test-results/record.sh \
  /path/to/before/openai /path/to/after/openai \
  5351ddb021263896da3a6871b621e19d38e8ffe9 AFTER_SHA \
  /path/outside/repository/webhooks-80

DEMO_WIDTH=40 PATH=/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH \
  bash scripts/demos/webhook-test-results/record.sh \
  /path/to/before/openai /path/to/after/openai \
  5351ddb021263896da3a6871b621e19d38e8ffe9 AFTER_SHA \
  /path/outside/repository/webhooks-40
```

`AFTER_SHA` must be the full candidate commit ID.
The shared `../capture_and_render.sh` lifecycle owns recording, rendering, fixture cleanup, and temporary files.
The guided driver reuses `../../image_picker_harness.py` for PTY control and terminal restoration checks.
It relays the child's exact terminal bytes into the outer recording.
Both captures must retain identical bytes.
After the CLI exits, the recorder repeats `Terminal replay | synthetic data` before the final prompt.
This footer labels the recording; it is separate from the preserved child output and its hash.
The driver restores output processing and reaps its child on failure or interruption.

The Python fixture binds an ephemeral loopback port.
It validates routes, request bodies, synthetic credentials, and confirmation state.
It limits this fixed recipe to eight requests.
Its signal handler stops the server and waits for request threads.
Fixture failures and shutdown failures fail the recording.

## Inspect the evidence

Each command uses a temporary home, fake credentials, and an explicit environment.
No personal CLI configuration, proxy settings, or shell hooks enter the command environment.
Both widths use `NO_COLOR=1`.
Outer recordings use 40 rows to retain labels and results.
The guided child uses 24 rows.

Test and catalog probes use `--format text` with separate stdout and stderr capture.
Terminal scenes use default readable output and combine stdout with stderr.
The recipe verifies exact test stdout, baseline silence on stderr, candidate guidance, and zero exit statuses.
Rendered form checks require one title and no stale unfiltered event rows after each transition.
It also verifies complete catalog fields, fixture equality, request counts, and unchanged source and binary hashes.
The runtime manifest includes Go source and module files, excluding test files.
Long advice wraps at 40 columns.
Transcript checks require complete text after wrapping but do not establish visual quality.

`comparison.gif` includes the before test, after test, event catalog, and guided creation.
`before.png` and `after.png` show the receiver500 comparison.
`discovery.png` shows grouped events.
`guided.png` shows the created endpoint and next steps.
Its footer keeps the synthetic-data label visible when the result scrolls the initial heading.
`guided-name.png`, `guided-url.png`, and `guided-events.png` show focus within the combined form.
`guided-review.png` shows final review before creation.
`guided-confirm.png` shows the explicit Yes selection before submission.
Additional frames show empty search results, restored selections, and back navigation.
Each frame retains the terminal replay and synthetic data labels.

Each scene includes its cast, transcript, and exit status.
Noninteractive probes retain separate stdout, stderr, and exit status files.
`guided-evidence.*` retains child bytes, child timing, and checkpoint times.
`metadata.txt` records commit inputs, environment, tools, binary hashes, and capture statuses.
`requests.jsonl` records only synthetic scenario names, operations, response hashes, and API statuses.

Inspect all PNGs and GIF frames for clipping, spacing, timing, and sensitive data before sharing.
These artifacts show terminal replays with synthetic data.
They do not establish native terminal appearance or live API behavior.
Keep media and binaries outside Git.

## Colored form and endpoint browser

Record the current dark palette and endpoint browser with the same isolated capture lifecycle:

```sh
bash scripts/demos/webhook-test-results/record-workflow.sh \
  /path/to/after/openai AFTER_SHA /path/outside/repository/webhooks-workflow
```

This scene uses an 80×28 outer terminal and an 80×24 child terminal.
It shows category selection, search, Space selection, and Ctrl+A within the selected category.
It opens Existing webhooks, inspects one endpoint, and returns to the retained draft.
The confirmation starts at No. The driver explicitly selects Yes before creation.
The fixture accepts exactly the expected subscriptions and rejects creation before the confirmation marker.
Validation requires three successful requests: catalog, endpoint list, and endpoint creation.
No request reaches the displayed receiver URL.

`workflow.gif` contains the walkthrough. Named PNGs show each checkpoint, including `events.png`, `endpoints.png`, and `endpoint-details.png`.
`confirmation.png` shows the initial No selection. `draft-return.png` shows retained settings and subscriptions.
The driver retains exact child output, timing, request evidence, and source hashes.
Colors use the existing CLI dark palette. Rendering uses Menlo and Dracula through agg.
These recordings establish synthetic terminal behavior, not native graphical appearance or live API behavior.
