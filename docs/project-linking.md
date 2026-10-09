# Folder project links

Save an OpenAI project for the current folder:

```sh
cd work-chatbot
openai link --project proj_work
openai files list
```

`files list` lists remote OpenAI files in that project. It does not list local files.
Linking does not authenticate, create a remote project, or grant project access.
Your API key must already have access to the selected project.

Inspect the saved link and its effective project:

```sh
openai link
openai link --format json
```

Remove the current folder's link:

```sh
openai unlink
```

All three operations run locally without credentials or network requests.
They leave repository files unchanged.
Use `link --project` again to replace a saved link.
An environment variable alone never saves a link.

## Project selection

API requests select a project in this order:

1. The explicit root `--project` flag.
2. `OPENAI_PROJECT_ID`.
3. The nearest linked folder, including the current folder.
4. The existing SDK default when no project is selected.

An explicit empty flag or empty environment variable disables folder selection.
For example, `openai --project= files list` sends an empty project header.
Root request flags work before or after commands, subject to existing endpoint flag ownership.
An endpoint's own `--project` request field does not change request-header selection.
Explicit custom `OpenAI-Project` headers retain their existing override behavior.

`link --project` saves a future folder default.
An existing `OPENAI_PROJECT_ID` still overrides that default on later API commands.
Link output identifies this override.

## Folders and storage

Subdirectories inherit the nearest linked ancestor.
A link in a subdirectory overrides its parent link.
`unlink` removes only the current folder's own link.
Removing a child link can expose a parent link again.
Run `unlink` in the linked parent folder to remove that parent link.

The CLI resolves symlinks before saving or looking up a directory.
The real directory and its symlink therefore share one link.
Links follow canonical paths, not folder names or filesystem object identities.
Moving a folder does not move its link.
Deleting a folder leaves its saved entry unchanged.
Recreating the same path reuses the saved entry.
Unlink before moving or deleting a folder when you do not want this behavior.

The registry contains only paths and project IDs.
The CLI stores it in the operating system's user configuration directory:

- macOS: `~/Library/Application Support/openai/project-links.json`
- Linux: `$XDG_CONFIG_HOME/openai/project-links.json`, or `~/.config/openai/project-links.json`
- Windows: `%APPDATA%\openai\project-links.json`

The CLI never reads project defaults from repository configuration.
The user configuration directory must be an absolute path.
On Unix, the application directory and registry must have private permissions.
New directories use mode `0700`; new registry files use mode `0600`.
Windows uses the user configuration directory's inherited access controls.

Concurrent CLI writers hold a shared registry lock through validation and replacement.
Each write syncs a temporary file before renaming it over the registry.
Unix rename keeps readers from seeing partial JSON.
An interrupted writer releases its kernel lock when the process exits.
The sibling lock file remains intentionally.
External editors must not change the registry while CLI writers run.
Native Windows replacement and locking behavior remain unverified.

The local registry supports up to 1 MiB of JSON.
This limit does not apply to API payloads.
Malformed, duplicate, insecure, or nonregular registry files cause an error.
The CLI preserves invalid settings instead of overwriting them.
An explicit project flag or environment variable bypasses registry loading for API requests.
An explicit `OpenAI-Project` custom header also bypasses registry loading.

## Output and recovery

`link` and `unlink` support `--format auto`, `text`, and `json`.
They reject positional arguments, `--transform`, and `--raw-output` before changing settings.
`--quiet` suppresses readable save/remove confirmations and preserves inspection and JSON output.
JSON reports the action, current directory, linked directory, saved project, inheritance, effective project, and source.
An empty effective project means the request uses an empty override or the existing default.
The `source` field distinguishes these cases.
Inspection hides environment values that do not match the project ID format.
JSON marks these values with `effective_project_redacted: true`.
This prevents inspection from printing accidentally pasted credentials.
API requests preserve the existing environment behavior.

If output fails after saving, inspect the link before retrying.
If the registry cannot be read, check its file type, permissions, and JSON syntax.
Move an invalid registry aside only when you intend to discard its saved links.
To remove a stale entry for a deleted directory, edit that entry while no CLI process writes the registry.
