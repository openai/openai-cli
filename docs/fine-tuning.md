# Monitor fine-tuning jobs

Use existing job IDs to inspect progress and failures. These commands do not start training.

```sh
openai fine-tuning jobs list
openai fine-tuning jobs retrieve --fine-tuning-job-id ftjob_example
openai fine-tuning jobs list-events --fine-tuning-job-id ftjob_example
openai fine-tuning jobs checkpoints list --fine-tuning-job-id ftjob_example
```

Replace `ftjob_example` with a returned job ID. Events are a snapshot, not a continuous watch.
Inspect the job's `status` and `error` fields before choosing a recovery action.
Use `--format json` for full API fields, including unfamiliar statuses and checkpoint metadata.

## Available commands and dashboard limits

These command paths follow `openai fine-tuning`:

| Purpose | Existing command paths |
| --- | --- |
| Inspect jobs | `jobs list`, `jobs retrieve`, `jobs list-events` |
| Manage supported training jobs | `jobs create`, `jobs cancel`, `jobs pause`, `jobs resume` |
| Inspect checkpoint metadata | `jobs checkpoints list` |
| Manage checkpoint sharing | `checkpoints permissions list`, `checkpoints permissions retrieve`, `checkpoints permissions create`, `checkpoints permissions delete` |
| Validate or run a reinforcement grader | `alpha graders validate`, `alpha graders run` |

Job creation accepts supervised, DPO, and reinforcement method settings through `--method` or a JSON request body.
These inputs do not bypass training eligibility. Inspect `jobs create --help` before submitting a request.
Job-level Weights & Biases settings use `jobs create --integration`.
That option does not store organization integration credentials.

The CLI does not provide dashboard charts, automatic event polling, or a model-comparison interface.
Use job, event, and checkpoint responses to inspect their underlying data.
Job listing supports the public API's `--after`, `--limit`, and `--metadata` filters.
Dashboard-specific status, model, and user filters have no matching public list flags.
Organization integration credential configuration and checkpoint-weight export have no documented public CLI workflow.
Dashboard controls can depend on organization eligibility and internal feature gates.
Their appearance does not establish a public API contract.

## Empty results and access errors

A successful empty jobs request prints:

```text
No fine-tuning jobs returned.
Training eligibility was not checked.
```

This describes the current request. It does not establish that the organization has no jobs or cannot train.
Check the selected project, credentials, metadata filters, and `--after` cursor when an expected job is missing.
Authentication, permission, network, and pagination errors remain failures with nonzero exit statuses.
An error does not become an empty result.

Automatic and explicit text output use this message, including pipes.
Explicit JSON, JSONL, YAML, raw, pretty, explore, extraction, and raw-output behavior stays unchanged.
Use `--format jsonl` for scripts that consume one complete record per line.

```sh
openai fine-tuning jobs list --format jsonl --max-items -1
openai fine-tuning jobs list --transform id --raw-output
openai fine-tuning jobs list --after ftjob_example --limit 10
```

`--max-items -1` retains unlimited automatic pagination. `--limit` controls each API page.
`--max-items 0` prints no records. The command still makes the initial request and preserves its errors.
`--format raw` returns one API page envelope instead of traversing all pages.
Persistent request options, including `--project`, `--organization`, `--base-url`, and `--header`, retain their existing behavior.
Use `openai help setup` for credential configuration. Keep secret values out of shell history.

## Pause and resume

The existing commands request state changes. Confirm eligibility and the intended job before running them.
Resuming continues training and can incur charges.

```sh
openai fine-tuning jobs pause --fine-tuning-job-id ftjob_example
openai fine-tuning jobs resume --fine-tuning-job-id ftjob_example
```

The API decides whether a job supports each action in its current state.
Inspect the returned status and subsequent events. HTTP success does not mean training completed.
The [reinforcement fine-tuning guide](https://developers.openai.com/api/docs/guides/reinforcement-fine-tuning#pausing-and-resuming-jobs) explains supported pause/resume behavior.

## Checkpoint access

Job checkpoint listing returns metadata. Its `fine_tuned_model_checkpoint` field identifies the model used by permission commands.
That field differs from the checkpoint object's `id`.

```sh
openai fine-tuning checkpoints permissions list \
  --fine-tuned-model-checkpoint 'ft:model:organization:suffix:checkpoint'
```

Permission operations require an admin API key through the existing `OPENAI_ADMIN_KEY` configuration.
Organization owners can grant or remove access across projects within the same organization.
See the [permission API](https://developers.openai.com/api/reference/resources/fine_tuning/subresources/checkpoints/subresources/permissions/methods/create).

Sharing permissions do not export model weights. This CLI has no checkpoint-export command or promised download format.
A checkpoint-export workflow needs a supported public contract and an agreed customer migration requirement.

## Availability

Checked October 18, 2026 against the [official availability timeline](https://developers.openai.com/api/docs/deprecations#update-to-openais-self-serve-fine-tuning):

- Since May 7, 2026, organizations without previous fine-tuning cannot create jobs or train.
- Since July 2, 2026, organizations without fine-tuned inference during the preceding 60 days cannot create jobs.
- On January 6, 2027, active existing customers lose access to creating new jobs.
- Fine-tuned inference continues until the corresponding base model is deprecated.

January 6 is not a general inference shutdown date. An empty list does not check these eligibility conditions.
Check the linked timeline for updates before planning training or migration.

The CLI retains supported commands for existing customers and scripts during this transition.
The API enforces eligibility and job-state restrictions. A command's presence does not grant training access.

Local fixture tests verify CLI behavior. They do not establish account eligibility or live service acceptance.
