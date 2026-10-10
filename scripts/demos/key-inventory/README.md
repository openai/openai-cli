# Key and service-account inventory

Existing commands show readable inventory with the selected project scope:

```sh
openai admin admin-api-keys list
openai admin projects api-keys list --project-id proj_example
openai admin projects api-keys list --project-id proj_example --owner-project-access any
openai admin projects service-accounts list --project-id proj_example
openai projects api-keys retrieve key_example --project-id proj_example
```

No new commands or flags are added. Colon routes and organization routes remain supported.
The Admin-key command reads `/organization/admin_api_keys` for the selected organization.
Its public documentation has conflicting scope wording. This inventory does not promise an account-wide census of credentials.
Project inventory uses `--project-id`, independently of the root `--project` request header.
Project-key visibility retains the API default. `--owner-project-access any` requests all enabled project keys.
A successful empty list means that the request returned no records with those inputs.

Readable results keep complete IDs, names, owners, roles, and returned metadata.
Timestamp fields contain Unix seconds. Missing fields remain absent; explicit null remains visible.
Project-key `expires_at: null` means no expiration. Missing expiry does not establish that meaning.
A null last-use value does not establish that a key was never used.
Owner project access describes the owner's access. It does not establish key validity.
A service-account role does not establish credential validity.
Unfamiliar fields remain visible. Readable inventory masks known one-time secret slots.
Readable inventory rejects malformed JSON and non-object records without echoing their contents.

Explicit output modes retain API data, including unfamiliar fields:

```sh
openai admin projects api-keys list --project-id proj_example --format json
openai admin projects api-keys list --project-id proj_example --format jsonl
openai admin projects api-keys list --project-id proj_example --format raw
openai admin projects api-keys list --project-id proj_example --transform id --raw-output
```

Pagination, filters, `--max-items -1`, custom headers, credentials, and context options retain their existing behavior.
Creation commands retain their original one-time secret responses. Store real credentials securely when using those existing commands.
This feature does not add personal-key APIs, Admin-key updates, or policy controls.
Synthetic verification does not establish live expiry enforcement or credential ownership in a test organization.

## Synthetic demo

The fixture accepts GET requests only. It creates no keys or service accounts.
Use separate baseline and candidate binaries built from recorded source revisions.
The recorder uses the shared `scripts/demos/capture_and_render.sh` lifecycle.
Store recordings outside the repository. Recordings are terminal replays, not native graphical-terminal validation.

```sh
scripts/demos/key-inventory/record.sh --run-authorized-pty-slot \
  /absolute/path/before/openai /absolute/path/after/openai \
  BEFORE_COMMIT AFTER_COMMIT /absolute/path/recording
```

The scene shows an empty Admin-key list and one populated project-key list.
The synthetic project key has an inactive owner, null last-use, and an expiry timestamp.
Expected output preserves those meanings without inventing a disabled or never-used status.
The recorder stops its own fixture and removes its temporary runtime directory.
