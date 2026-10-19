# Local profiles

Profiles save organization, project, and base URL settings on your computer.
Profile commands make no API requests.
They do not change credentials, project access, or server settings.
Use environment variables or existing request flags for credentials.

## Create and edit

```sh
openai profile create work
openai profile set work --organization org_demo --project proj_demo
openai profile get work
openai profile list
```

`create` makes an empty profile without activating it.
Names are case-sensitive UTF-8 strings without controls or surrounding whitespace.
Names can contain internal spaces when quoted.
The CLI does not normalize names or use them as filenames.

`set` requires at least one explicit edit.
Only its command-local flags persist values.
Root flags and environment values never become saved edits.

```sh
# Save a project with the flag owned by profile set.
openai profile set work --project proj_saved

# The root project flag does not become a saved edit.
# This command saves only the organization.
openai --project proj_request profile set work --organization org_saved
```

An absent field permits lower-priority defaults.
An empty organization or project field suppresses those defaults.
An empty stored base URL explicitly selects the production endpoint when effective.
`get` and `list` retain the stored empty string.

```sh
openai profile set work --project=
openai profile set work --base-url=
openai profile set work --unset project --unset base_url
```

`--unset` accepts `organization`, `project`, or `base_url`.
Repeat the flag to remove multiple fields.
Setting and unsetting the same field fails before the CLI writes anything.
Saved base URLs must use HTTP or HTTPS without user information, query parameters, or fragments.

## Select and switch

```sh
openai profile activate work
openai --profile work auth status
openai --profile work auth switch --to-project proj_other
```

`activate` changes the saved default only.
`auth switch` changes only the selected profile's saved project.
It requires a nonempty `--to-project` value.
It does not activate that profile or remove request overrides.

The CLI selects a profile in this order:

1. Root `--profile NAME`.
2. Present `OPENAI_PROFILE`.
3. The saved default.

An empty root selector or present-empty `OPENAI_PROFILE` disables the saved default.
Folder project defaults remain eligible.
Unknown nonempty selectors and dangling saved defaults fail without fallback.

```sh
# Ignore the saved default for this request.
openai --profile= models list

# Switch a saved profile without activating it.
openai --profile work auth switch --to-project proj_other
```

All `profile` management commands ignore unrelated selectors, including `profile list`.
This permits inspection and repair when `OPENAI_PROFILE` names a missing profile.
`auth status` and `auth switch` resolve selectors.
Completion's command-local `@completion --profile PATH` still identifies a shell profile path.
It does not select an API configuration profile.

## Effective request settings

Project settings use this order:

1. Literal `--header OpenAI-Project:...`.
2. Explicit root `--project`.
3. Present `OPENAI_PROJECT_ID`.
4. Present project field in the selected profile.
5. The nearest folder link.
6. `OPENAI_CUSTOM_HEADERS` fallback.
7. Existing SDK fallback.

Organization settings use the same order without folder links.
Explicit empty headers, flags, environment values, and profile fields retain their meaning.
Endpoint-owned `--project` fields remain API request fields.
They do not select the root project setting.

The saved default profile takes priority over folder links.
Use `openai auth status` to inspect effective values and their sources.
Profile sources appear as `profile:NAME`; folder sources appear as `folder`.
Status reports configuration without verifying identity or access.

Base URLs use this order:

1. Nonempty root `--base-url`.
2. Present `OPENAI_BASE_URL`.
3. Present base URL field in the selected profile.
4. The SDK production default.

An empty root `--base-url` retains fallback behavior.
A present-empty `OPENAI_BASE_URL` retains its explicit empty environment value.
An empty stored base URL selects `https://api.openai.com/v1/` when effective.
Status reports that actual production URL with profile provenance.
Status sanitizes endpoint information; profile inspection shows saved values.

## Remove a profile

```sh
openai profile delete work --deactivate
```

The active profile is the saved default, regardless of environment or request selectors.
Deleting it requires `--deactivate`.
The CLI clears the default and deletes the profile in one transaction.
Deleting another profile does not change the default.
Environment selectors remain unchanged.

## Output and storage

Profile commands and `auth switch` support `--format auto`, `text`, `json`, and `jsonl`.
Automatic output uses readable text, including redirected output.
`--quiet` preserves profile data and mutation receipts.
Unsupported formats, `--transform`, and `--raw-output` fail before mutation.

`get` returns one record:

```json
{"name":"work","active":true,"values":{"project":"proj_demo"}}
```

`list` sorts names by UTF-8 byte order.
JSON returns an array, including `[]` when empty.
JSONL returns one record per line, with no rows when empty.
Absent fields remain omitted; empty fields remain empty strings.

Create, set, activate, and switch receipts contain the operation name and resulting profile:

```json
{"action":"switch","profile":{"name":"work","active":true,"values":{"project":"proj_other"}}}
```

Delete receipts report whether the command cleared the saved default:

```json
{"action":"delete","name":"work","deactivated":true}
```

Each JSONL receipt occupies one complete line.
The CLI emits receipts only after committing storage.
If output fails afterward, the command fails and states that saved profiles changed.
Inspect the saved state before repeating a mutation.

Profiles use `os.UserConfigDir()/openai/profiles.json`.
The location follows the operating system's user configuration directory rules.
The local registry must fit within 1 MiB.
This limit does not affect API responses or streaming events.
The file uses a versioned schema:

```json
{"version":1,"default_profile":null,"profiles":{"work":{"project":"proj_demo"}}}
```

Mutations lock the registry and validate its contents before replacement.
Replacement is atomic on Unix; other platforms do not guarantee atomic replacement.
The CLI rejects malformed schemas, duplicate keys, unknown fields, unsupported versions, and dangling defaults.
Validation failures preserve existing registry bytes.
Store no credentials or credential references in this file.
The profile registry remains separate from `project-links.json`.
