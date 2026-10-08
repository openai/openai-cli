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

Use `./-` for a file named `-`. The existing `--file -` behavior reads file contents from stdin.

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
A successful upload does not mean that downstream processing has finished.

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
See [reading command results](readable-output.md) for output formats.

Inspect command help without making an API request:

```sh
openai files upload --help
openai files get --help
openai files download --help
```
