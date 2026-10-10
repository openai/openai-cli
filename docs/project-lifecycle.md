# Create, rename, and archive projects

These commands manage projects in your organization through the Admin API.
Use an admin API key with permission for the operation.
Configure `OPENAI_ADMIN_KEY` through your existing secure environment or credential setup.
Use `openai help setup` for credential guidance without sending an API request.

## Create and inspect a project

Create a project with a name:

```sh
openai admin projects create --name "Example application"
```

Use the returned project ID to inspect its current state:

```sh
openai admin projects retrieve --project-id proj_example
```

Replace `proj_example` with the complete returned ID in subsequent examples.
Use returned IDs, names, and status values when checking the result.
A command's requested name or target ID does not substitute for the returned project state.

## Choose residency when creating a project

Request an available residency configuration explicitly:

```sh
openai admin projects create --name "Example US application" --residency US_STORAGE_PROCESSING
```

Your organization must have access to the requested configuration.
The accepted configurations appear in `openai admin projects create --help`.
Omitting `--residency` leaves the selection to the API. Do not assume a region from omission.
Inspect the returned `residency` when the API supplies it.

Regional storage and regional processing have different support requirements.
Consult [data residency controls](https://developers.openai.com/api/docs/guides/your-data#data-residency-controls) before choosing a configuration.
Command help lists accepted values; it does not establish your organization's access.

The existing `--geography` option is deprecated and remains available for compatibility.
Use `--residency` for new project configuration. Do not supply both options when creating a project.
The project update API does not expose `residency` as an update field.
See the [create](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/methods/create)
and [update](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/methods/update) references for supported fields.

## Rename a project

Update the name using its project ID:

```sh
openai admin projects update --project-id proj_example --name "Renamed application"
```

Check the returned name and status.
Use `retrieve` when you need a later read of the current project state.

## Archive a project

Archive a project when you intend to stop using it:

```sh
openai admin projects archive --project-id proj_example
```

Archived projects cannot be used or updated.
Archive does not delete the project. Do not treat archive as confirmation that project data was erased.
See the [archive reference](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/methods/archive).

List projects, including archived projects:

```sh
openai admin projects list --include-archived=true
```

The default list excludes archived projects.
`--limit` controls each API page; its default is 20 and its supported range is 1–100.
`--max-items` controls the total number of displayed items. The default is unlimited; `-1` selects unlimited explicitly.
`--after` sets the initial pagination cursor.

## Preserve request context

`--project-id` selects the project resource for retrieve, update, and archive.
The global `--project` option sets request context. It does not replace `--project-id`.
`--organization` sets organization request context.
These settings do not prove the authenticated organization's identity.
Project responses contain a project ID, but no organization ID.

For example, inspect a target project with an explicit organization setting:

```sh
openai --organization org_example admin projects retrieve --project-id proj_example
```

Keep the same credentials, organization, base URL, and custom headers during follow-up reads.
Explicit `--admin-api-key`, `--organization`, `--project`, `--base-url`, and `--header` options remain available.
Prefer environment-based credentials to avoid exposing secrets in process arguments or shell history.
`--admin-api-key` overrides `OPENAI_ADMIN_KEY`; `--organization` and `--project` override their corresponding environment settings.
Custom headers can override matching request headers. Review [request header behavior](../README.md#request-headers) when using overrides.

## Use explicit output formats in scripts

Select JSON when a script consumes project metadata:

```sh
openai admin projects retrieve --project-id proj_example --format json > project.json
```

Select one field with the existing transform option:

```sh
openai admin projects retrieve --project-id proj_example --transform id --raw-output
```

Readable output remains the default, including in pipes.
Explicit JSON, JSONL, YAML, raw output, and transforms retain their selected output behavior.
Machine formats preserve API field values, including numeric timestamps.
See [reading command results](readable-output.md) for format and extraction details.

The existing command routes remain available:

```sh
openai admin:organization:projects retrieve --project-id proj_example
openai admin organization projects retrieve --project-id proj_example
openai projects retrieve --project-id proj_example
```

## Recover when the mutation outcome is uncertain

A timeout, connection loss, cancellation, or output failure can occur after the API receives a mutation.
A missing receipt does not prove the mutation failed. Local cancellation does not undo an accepted change.
Malformed or unexpected output also leaves the outcome unconfirmed, even if the process exits successfully.
Check the returned state before repeating create, update, or archive.

For an existing project ID, read the project without changing it:

```sh
openai admin projects retrieve --project-id proj_example --format json
```

If create returned no usable ID, inspect project listings in the same organization:

```sh
openai admin projects list --include-archived=true --max-items -1 --format jsonl
```

Compare the name and creation time. Project names are not unique.
Missing or inaccessible results do not prove that the API rejected the mutation.
An error during pagination can leave a partial listing. Check the command's exit status.
Resolve uncertainty before creating another project or repeating the mutation.

For details about a failed read, select structured errors:

```sh
openai admin projects retrieve --project-id proj_example --format-error json
```

Do not repeat an uncertain mutation merely to obtain structured error details.
For authentication or permission failures, inspect `openai help setup` and the selected credential's access.
Preserve the original error details when escalating an unresolved failure.

## Other project settings

Residency support does not imply support for project key policy or other dashboard settings.
Project create/update expose only their documented fields.
The CLI does not provide a project key-policy command through this lifecycle workflow.
Do not add undocumented policy fields to a project update body.
