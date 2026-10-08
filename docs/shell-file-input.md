# Shell and file input

Explicit stdin references select one request parameter.
Without an explicit reference, the CLI keeps its existing whole-request JSON/YAML behavior.

```sh
printf 'hello\n' | openai responses create --model MODEL --input @-
cat notes.txt | openai files create --file - --purpose user_data
cat speech.wav | openai audio transcriptions create --model whisper-1 --file -
```

The first command sends `hello\n` as the input field.
JSON-looking text also stays text when a field explicitly selects stdin.
The CLI does not expand references found inside that text.

Only one effective parameter can consume stdin.
Two consumers fail before the CLI reads stdin or sends a request.
Repeated scalar flags use their last value.
Collection flags retain multiple values, so repeated stdin entries conflict.
Arguments after `--` retain the command's positional-argument rules.
They do not become flags or file references.

## Text references and binary paths

| Input form | Meaning |
| --- | --- |
| Text field `@prompt.txt` | Read text; encode detected binary content as base64. |
| Text field `@-` | Apply the same text/base64 behavior to stdin. |
| Text field `@file://prompt.txt` | Read the file as a string. |
| Text field `@data://prompt.txt` | Encode the file as base64. |
| Text field `@file://-` or `@data://-` | Apply the selected representation to stdin. |
| Text field `\@literal` | Keep a literal leading `@`. |
| Binary flag `--file recording.wav` | Upload the file's bytes. |
| Binary flag `--file -` | Upload stdin bytes. |
| Binary flag `--file ./-` | Upload the file named `-`. |
| Binary flag `--file @recording.wav` | Upload the file literally named `@recording.wav`. |

The URI forms apply only where existing text-reference handling supports them.
Bare `file://` and `data://` values do not enable global file loading.
Binary file flags use literal paths, including leading `@` characters.

Multipart stdin uploads retain `anonymous_file` and `application/octet-stream` metadata.
A file path retains its basename and extension-derived content type.
Use a file path when an API needs a named file or a specific extension.
Uploads containing readers stream once and do not retry or replay automatically.
Multipart uploads preserve NUL and non-UTF8 bytes.

### Windows shells

Windows PowerShell 5.1 can transform binary data in pipelines and redirection.
PowerShell 7.4 and later preserve native byte pipelines and stdout redirection when stdout and stderr remain separate.
PowerShell object pipelines can have different behavior.
See [Microsoft's byte-stream guidance](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_redirection#example-7-redirecting-binary-data-from-a-native-command).

File paths avoid shell conversion for uploads and downloads:

```powershell
openai files create --file .\sample.wav --purpose user_data
openai files content file-example --output .\copy.bin
```

The CLI preserves the bytes it receives; it cannot restore bytes that the shell already changed.

An empty explicit text input produces an empty string.
An empty binary input produces an empty upload.
The API still determines whether those values are valid.

## Whole-request input

```sh
printf '%s' '{"model":"MODEL","input":"hello"}' | openai responses create
printf 'model: MODEL\ninput: hello\n' | openai responses create
```

Explicit request flags override matching piped fields.
Unknown body fields and explicit nulls retain their existing encoding behavior.
Empty stdin contributes no whole-request fields.
Required request fields still apply.

A final Boolean `stream: true` selects stream dispatch for supported JSON and multipart commands.
Explicit stream flags retain precedence.
Strings that resemble Booleans do not select stream dispatch.

For input from an untrusted producer, set `OPENAI_UNTRUSTED_STDIN=true`.
Piped strings then remain literal instead of authorizing local file reads.
Piped binary file paths fail in this mode.
Explicit file flags remain caller-selected inputs.

## Binary output

```sh
openai files content file-example > copy.bin
openai files content file-example --output - > copy.bin
openai files content file-example --output copy.bin
```

The first two commands stream exact response bytes to stdout.
The shell owns `>` and truncates its destination before the CLI starts.
A failed transfer can leave partial redirected output.
A nonzero exit status remains necessary when checking a transfer.

An explicit file destination stages the complete download before changing an existing ordinary file.
A failed network transfer leaves the existing file unchanged.
Successful completion preserves its inode, permissions, symlinks, and hardlinks.
Linked names therefore continue to expose the saved bytes.

On Windows, new explicit file destinations use protected permissions for the current user, SYSTEM, and Administrators.
Existing files retain their access-control lists.
Private staging files use the same protected Windows permissions.

The final local copy into an existing file is not atomic.
A disk, write, close, or interruption failure during that copy can leave partial contents.
The error identifies that outcome instead of promising rollback.

New destinations publish completed staging files without replacing concurrently created files.
Filesystems without hardlink support use exclusive creation and a final local copy.
That fallback can expose partial bytes during the local copy.
The CLI removes its incomplete destination when it can verify ownership.

Managed SIGINT and SIGTERM interruption removes owned staging files during normal cleanup.
Forced termination and power loss can leave staging files.
Another process with the same user's write authority can modify files during a save.
The CLI detects common destination replacements but does not lock out arbitrary external writers.

Special destinations, such as `/dev/null` and named pipes, retain direct streaming behavior.
Automatic terminal downloads retain their existing destination selection and partial-file behavior.

Successful API download receipts go to stderr, leaving stdout available for data.
Structured error formats and error extraction suppress optional receipts.
Use `--format-error text` to request text diagnostics alongside an explicit response format.
A receipt-write failure returns a nonzero status while retaining the completed file.
The local `@manpages` command retains its existing status messages and completion behavior.

Explicit-path speech SSE saves require a `speech.audio.done` event before success.
Empty, heartbeat-only, and incomplete speech streams fail.
Ordinary empty binary responses remain valid.
Special paths still stream directly and do not gain rollback guarantees.
