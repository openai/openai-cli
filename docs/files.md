# Upload, inspect, and download files

Upload a local file with an explicit purpose:

```sh
openai files upload "upload space.txt" --purpose user_data
```

Use the returned file ID to inspect its metadata:

```sh
openai files get file-example
```

Download its contents to a local path:

```sh
openai files download file-example --output "downloaded copy.txt"
```

You can also redirect the contents:

```sh
openai files download file-example > copy.txt
```

Replace `file-example` with the full ID from your upload response.

## Find commands and complete paths

Use `openai files --help` to see upload, get, and download in workflow order.
Each command's `--help` explains its inputs and shows an example.
Help also works after a path or file ID without sending an API request.

The existing Bash, Zsh, fish, and PowerShell completion adapters support the first upload path.
[Reload an already loaded completion adapter](shell-completion.md) after updating the CLI.
The adapter completes literal filenames, including a leading `@`.
In PowerShell, quote a leading `@` or use `./@name` to avoid shell splatting.

## Upload paths and purpose

Upload takes a plain local path. Quote paths that contain spaces or shell characters.
The existing `--file` flag remains available:

```sh
openai files upload --file "upload space.txt" --purpose user_data
openai files create --file "upload space.txt" --purpose user_data
```

Provide one positional path or `--file`, not both.
Upload does not add or remove a leading `@`.
The following command uploads a file whose actual name starts with `@`:

```sh
openai files upload "@upload space.txt" --purpose user_data
```

For `upload space.txt`, use `"upload space.txt"`, without an added `@`.
The binary upload path does not use the JSON input expansion syntax.

Prefix a path starting with `-` with `./`:

```sh
openai files upload "./-notes.txt" --purpose user_data
```

Alternatively, place options before `--`. The CLI treats the following path as literal input:

```sh
openai files upload --purpose user_data -- "-notes.txt"
```

Use `./-` for a file named `-`. The existing `--file -` sentinel requests stdin.
Current piped input can be consumed first as JSON/YAML and rejected before the upload starts.
Use a local file for binary uploads until the shared stdin handling changes.

Supply `--purpose` for each upload. The CLI does not select a purpose for you.
The example uses `user_data`; select the purpose required by your API workflow.
The API applies purpose, file type, and size requirements.
See the [Files upload reference](https://developers.openai.com/api/reference/resources/files/methods/create) for current requirements.

## Upload receipt and metadata

An interactive upload shows a short receipt after the API accepts the upload:

```text
Uploaded upload space.txt (13 B)
ID: file-example
Purpose: user_data

Download it: openai files download file-example --output 'upload space.txt'
```

The receipt uses the returned filename, size, ID, and purpose.
The CLI omits the size when the API does not supply it.
The download suggestion appears for supported shells and unambiguous returned filenames.
A successful upload does not mean that downstream processing has finished.
When the returned status is `error`, the receipt suggests inspecting the metadata.
An upload can return success while its status reports a processing error.

`files get` shows metadata, including the full ID and timestamps in UTC.
For example, `created_at: 1700000000` becomes:

```text
Created at: 2023-11-14T22:13:20Z
```

The `Z` suffix means UTC. The displayed fields depend on the API response.
`files get` retrieves metadata. Use `files download` for the file contents.

The receipt and readable timestamps appear with automatic or text output when stdout and stderr are terminals.
Pipes, redirected stderr, and the existing `create` and `retrieve` commands keep their existing output.

## Download destinations

`--output PATH` writes the exact downloaded bytes to that path.
Its parent directory must already exist. An existing destination file is overwritten.
The `-o` alias remains available:

```sh
openai files download file-example -o "downloaded copy.bin"
```

Redirected stdout receives the exact contents, including binary bytes.
Upload receipts and metadata do not appear in downloaded contents.

With terminal stdout and no destination, download keeps the existing content behavior.
The CLI displays text safely and saves binary contents to a generated local file.
Use `--output -` to force contents to stdout.

## Scripts and existing commands

`create`, `retrieve`, and `content` keep their existing names and flags:

```sh
openai files create --file "upload space.txt" --purpose user_data
openai files retrieve --file-id file-example
openai files content --file-id file-example --output "downloaded copy.txt"
```

Choose JSON explicitly when a script parses metadata:

```sh
openai files get file-example --format json > metadata.json
openai files retrieve --file-id file-example --format json > metadata.json
```

JSON retains the original timestamp fields and other API data.
Extraction and raw string output remain available:

```sh
openai files upload "upload space.txt" --purpose user_data --transform id --raw-output
openai files get file-example --transform filename --raw-output
```

Interactive receipts do not appear in JSON, extraction, or piped upload output.
`--quiet` suppresses upload receipts and retains the selected metadata output.
The receipt's suggested command preserves the executable used for the upload.
See [reading command results](readable-output.md) for output formats.

Inspect command help without making an API request:

```sh
openai files upload --help
openai files get --help
openai files download --help
```

## Recover from a failed command

A command can fail after the API receives an upload.
A missing receipt does not prove that the upload failed.
Do not repeat an upload only to obtain its receipt or more error details.
Malformed HTTP 200 metadata can currently return exit status zero.
Treat unexpected output as unconfirmed, even when the process succeeds.

| Failure | Next action |
| --- | --- |
| Missing path, extra paths, or a positional path combined with `--file` | Provide one quoted path. Use `openai files upload "upload space.txt" --purpose user_data`. |
| Missing `--purpose` | Supply the purpose required by your workflow. The CLI does not choose it. |
| Missing local file or a directory instead of a file | Check the path relative to your current directory. Pass a readable file. Preserve a real leading `@`. |
| Permission denied | Check access to the source file or destination directory. Choose a location you can access. |
| HTTP 400 or 422 | Correct the rejected parameter, purpose, or file contents before another upload. Consult the Files upload requirements. |
| HTTP 401 or 403 | Use `openai help setup` for credential instructions. Check the selected project and its resource permissions. |
| HTTP 404 | Check the complete file ID and selected project. A deleted, expired, or inaccessible file might be unavailable. |
| HTTP 429 rate limit | Wait before another request. Billing or quota errors require correcting the stated limit. |
| HTTP 409, timeout, connection loss, or HTTP 5xx | Inspect the file before repeating an upload. The API might have received the request. |
| Processing status `error` | Inspect the metadata and status details. Correct the underlying input or workflow before another upload. |
| Missing destination directory | Create the parent directory or choose an existing directory. Then download to an unused filename. |
| Interrupted download, disk full, or write failure | Treat the destination as incomplete. Correct the cause before downloading to an unused filename. |
| Missing or malformed upload result, cancellation, or receipt write failure | Check for an accepted upload before repeating it. An absent receipt is not an upload rollback. |

If you have the file ID, inspect it without uploading again:

```sh
openai files get file-example --format json
```

If you do not have the ID, inspect recent files in the same project:

```sh
openai files list --purpose user_data --order desc --limit 20 --max-items 20 --format json
```

Use the purpose from your original upload.
Compare the filename, byte count, and creation time.
Filenames are not unique. This short list cannot prove that no upload exists.
Continue through older files when necessary.

For API details from a read request, use:

```sh
openai files get file-example --format-error json
```

Do not repeat an uncertain upload just to add `--format-error json`.
An upload can succeed even when the terminal or pipe cannot display its result.
The CLI does not undo an accepted upload when output fails.

Downloads can leave partial files after failure or interruption.
An existing `--output` destination can be truncated when downloading begins.
Shell redirection can truncate its destination before the CLI starts, even when the API request fails.
Choose an unused destination when recovering:

```sh
openai files download file-example --output "downloaded retry.bin"
```

The parent directory must already exist.
Check the command's exit status before using the download.
If you have an expected byte count or checksum, compare it with the downloaded file.
The command does not resume a partial download.

Read requests can retry transient failures. The CLI does not automatically replay streamed file uploads.
Cancellation stops local work; it does not prove that the API discarded the upload.
A valid File response confirms upload acceptance. Its processing status describes later processing.

Recovery for API errors follows the [official error guidance](https://developers.openai.com/api/docs/guides/error-codes).
