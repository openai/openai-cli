# Data controls and external storage

Readable output explains configured retention and external-storage validation states.
Existing commands and request bodies remain available.
Explicit `--format json`, `jsonl`, `yaml`, `raw`, and extraction retain their existing behavior.

## Inspect configuration

Use authorized Admin access for organization and project data controls.
These commands inspect saved configuration without running storage validation:

```sh
openai admin:organization:data-retention retrieve
openai admin:organization:projects:data-retention retrieve --project-id proj_demo
openai admin:organization:external-storage list --project-id proj_demo
openai admin:organization:external-storage retrieve --external-storage-id ext_returned
openai --format json admin:organization:external-storage retrieve --external-storage-id ext_returned
```

Replace synthetic IDs with IDs from your authorized organization.
`organization_default` means the project inherits its organization setting.
The project response does not resolve the effective setting.
Missing values remain missing; unknown values remain visible.
The CLI does not infer endpoint-specific retention from this response.

Organization and project retention already support `update` with `--retention-type`.
Updates change policy and require separately authorized resources.
Organization settings accept `zero_data_retention`, `modified_abuse_monitoring`, `enhanced_zero_data_retention`, and `enhanced_modified_abuse_monitoring`.
Project settings also accept `organization_default` and `none`.
`none` does not mean that every endpoint retains no data.

## Storage validation and lifecycle

`pending` means validation is incomplete.
`validated` records a successful check; it does not prove continuous storage health.
`unhealthy` records a detected storage or configuration problem.
Readable output preserves returned failure details and actual storage IDs.
An HTTP success does not establish completed validation.

The existing `validate` command writes cloud test objects and activates customer-managed retention after successful validation.
Run it only with authorized test resources and the correct storage ID:

```sh
# Mutation: this performs cloud validation and can change project retention.
openai admin:organization:external-storage validate --external-storage-id ext_returned
```

Storage setup requires an approved organization, Admin permissions, compatible cloud storage, and matching residency.
The CLI does not automatically validate during inspection.

The existing lifecycle exposes `create`, `retrieve`, `list`, `validate`, and `delete`.
No inspected contract provides storage `update` or atomic replacement.
Confirm replacement ordering and overlap requirements before planning a migration.
The CLI does not automatically delete or recreate configurations.
Deleting the final configuration restores organization-default retention when customer-managed retention was active.
Deletion leaves cloud storage unchanged.

See the [Private Safety Processing guide](https://developers.openai.com/api/docs/guides/private-safety-processing) for setup and validation effects.
See the [delete reference](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/external_storage/methods/delete) for removal effects.

## Provider request contracts

The pinned CLI contract supports these create inputs.
Each create request also requires `project_id`.
These synthetic objects illustrate required fields; they do not prove provider access.

```json
{"type":"aws","bucket":"synthetic-demo-bucket","role_arn":"arn:aws:iam::000000000000:role/synthetic-demo"}
```

```json
{"type":"azure","tenant_id":"synthetic-tenant","subscription_id":"synthetic-subscription","resource_group":"synthetic-group","account_name":"syntheticaccount","container":"synthetic-container"}
```

```json
{"type":"gcp","bucket":"synthetic-demo-bucket","workload_identity_project_number":"000000000000","workload_identity_pool_id":"synthetic-pool","workload_identity_provider_id":"synthetic-provider"}
```

Pass the provider object through `--provider` or supported JSON/YAML body input.
AWS response fields include `account_id`, `region`, and `external_id`; create does not require them.
Azure adds `region` in responses.
GCP adds `region` and `audience` in responses.
The CLI preserves unfamiliar fields and provider types.

## Record the synthetic comparison

The recorder uses the shared `scripts/demos/capture_and_render.sh` lifecycle.
It starts a loopback fixture with synthetic credentials in an isolated environment.
It does not contact OpenAI or a cloud provider.
It compares actual binaries using identical response bytes.

Provide Bash, Python 3, Go, asciinema, agg, ffmpeg, ffprobe, and shasum on `PATH`.
Provide verified baseline and candidate binaries with their full source commit IDs.
The recorder verifies source revisions through `go version -m` and records binary hashes.
The candidate binary must include Go VCS metadata.
The baseline normally requires clean Go VCS metadata.
For an archived baseline build, set `DEMO_BEFORE_BUILD_MANIFEST` to its reviewed build manifest.
That manifest must record matching `source_sha`, `binary_path`, and `binary_sha256` values.
It must also record `exit_code: 0` and `result: "pass"`.
The recorder verifies those fields and copies the manifest into the output directory.
The copied manifest retains archive provenance and build commands supplied by the caller.
This alternative does not establish baseline VCS metadata or live behavior.
For dirty candidate builds, set `DEMO_AFTER_SOURCE_STATE` to the reviewed source manifest identity.
Choose an empty output directory outside the repository.

```sh
scripts/demos/data-controls/record.sh \
  /tmp/f42-before/openai /tmp/f42-after/openai \
  "$BEFORE_SHA" "$AFTER_SHA" /tmp/f42-data-controls-demo
```

`DEMO_WINDOW_SIZE` accepts `120x32` (default), `80x40`, or `40x60`.
`DEMO_THEME` accepts `dracula` (default) or `github-light`.
All captures disable CLI color through `NO_COLOR=1` and `FORCE_COLOR=0`.
Metadata records the actual window size and theme.

```sh
DEMO_WINDOW_SIZE=80x40 DEMO_THEME=dracula \
  scripts/demos/data-controls/record.sh \
  /tmp/f42-before/openai /tmp/f42-after/openai \
  "$BEFORE_SHA" "$AFTER_SHA" /tmp/f42-data-controls-dark

DEMO_WINDOW_SIZE=40x60 DEMO_THEME=github-light \
  scripts/demos/data-controls/record.sh \
  /tmp/f42-before/openai /tmp/f42-after/openai \
  "$BEFORE_SHA" "$AFTER_SHA" /tmp/f42-data-controls-light
```

The recorder captures before/after retention, pending validation, and validated inspection scenes.
The pending fixture receives `ext_requested` but returns `ext_returned`.
Every scene verifies explicit JSON against its complete response object.
The retention scene also displays the JSON command and output.
Each CLI process has a 15-second deadline.
The fixture bounds socket reads and writes to three seconds.
Owned fixture and capture processes stop when the recorder exits.
Use the retained `.cast`, `.txt`, `.json`, metadata, and request logs for review.
The recorder produces `comparison.gif` and six scene screenshots outside Git.

```sh
python3 -I -B scripts/demos/data-controls/validate.py /tmp/f42-data-controls-demo
```

Replay evidence does not establish native terminal appearance or real-provider acceptance.
Live entitlement, credentials, residency, effective retention, and provider validation remain separate checkpoints.
