# Local profiles

Save organization and project defaults for commands you run across shells.

```sh
openai profile create work
openai profile set work --organization org_example --project proj_example
openai profile activate work
openai auth status
```

A new profile starts empty. Activation selects the default for future commands.
Use `profile get NAME` or `profile list` to inspect saved settings.

## Select another saved project

```sh
openai profile create staging
openai profile set staging --organization org_example --project proj_staging
openai profile activate staging
openai auth status
```

Use `profile activate work` to return to your previous settings.
Use `profile set staging --project proj_next` to change the ID saved under that name.
Selecting a profile keeps your existing credentials. Platform checks access when you make a request.

## Override one request

```sh
openai --profile work models list
openai --project proj_once models list
openai --profile= models list
```

The CLI selects the profile from `--profile`, then `OPENAI_PROFILE`, then the saved default.
An empty selector disables saved defaults. An unknown name stops the command before dispatch.

Each context field follows this order.

1. An explicit `--header` for `OpenAI-Organization` or `OpenAI-Project`.
2. The corresponding `--organization` or `--project` root flag.
3. `OPENAI_ORG_ID` or `OPENAI_PROJECT_ID`.
4. The selected profile's saved field.
5. `OPENAI_CUSTOM_HEADERS` and the SDK defaults.

Saving an organization or project trims surrounding spaces. An explicit empty field overrides lower defaults.
Remove a field with `openai profile set work --unset project`.
Delete an active profile with `openai profile delete work --deactivate`.

## Storage

The CLI stores profiles in `openai/profiles.json` under the operating system's user configuration directory.
Profile names are case-sensitive. Profile commands run locally. They support text, JSON and JSONL.
Only options passed to `profile set` edit saved organization and project IDs.
Profile commands reject configured credentials and recognizable OpenAI keys in new names and IDs.
If an old file contains one, the CLI hides it in profile output and blocks requests using that ID.
Use `profile set NAME --unset project` or `--unset organization` to remove the saved ID.
CLI writes use a shared lock. Close other editors before changing the file manually.

Earlier preview versions allowed saved base URLs. A selected profile containing a saved base URL blocks requests.
Remove that setting with `openai profile set work --unset base_url`.
Supply the intended endpoint with `--base-url` or `OPENAI_BASE_URL`.
