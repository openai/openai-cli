# Shell and file input

Run `openai help piping` for examples.
Use `@-` for one text field or `--file -` for a binary upload.

```sh
printf 'hello\n' | openai responses create --model MODEL --input @-
cat notes.txt | openai files create --file - --purpose user_data
cat speech.wav | openai audio transcriptions create --model whisper-1 --file -
```

The first command sends `hello\n` as the input field.
In literal mode, JSON-looking text also stays text when an ordinary text field explicitly selects stdin.
The CLI does not expand references found inside that text.

Only one effective parameter can consume stdin.
Two consumers fail before the CLI reads stdin or sends a request.
Repeated scalar flags use their last value.
Collection flags retain multiple values, so repeated stdin entries conflict.
Arguments after `--` retain the command's positional-argument rules.
They do not become flags or file references.

## Text and structured operands

Flags with a matching `-structured` flag preserve text in their ordinary form.
This includes Responses input, completion prompts and stop sequences, embedding inputs, and vector store search queries.
Use `openai help --all responses create` to find both forms for that command.
Other flags keep their existing parsers.
Use `-structured` to parse JSON or YAML explicitly:

```sh
openai responses create --model MODEL --input 'Translate: hello'
openai responses create --model MODEL --input-structured '[{"role":"user","content":"hello"}]'
openai responses create --model MODEL --input-structured @input.json
printf '%s' '[{"role":"user","content":"hello"}]' | openai responses create --model MODEL --input-structured @-
```

An explicit structured flag resolves its source once as text, then parses the result.
This ordering also applies in legacy mode and to multipart fields.
Strings inside the parsed result remain data, including `@file`, `@-`, and `\@` strings.
For example, `--input-structured '"@notes.txt"'` sends literal `@notes.txt`.
An explicit `@-` reserves stdin for that operand instead of whole-request parsing.
Do not combine ordinary and structured assignments to the same operand.
The CLI rejects that combination before source reads, regardless of argument order.

### Migrating scripts

**Literal defaults break scripts that relied on implicit JSON/YAML parsing for text-capable operands.**
Use matching `-structured` flags, or select legacy mode for the process.
Scripts that depend on either mode must pin a CLI version that supports it.
Older binaries can ignore the setting.

```sh
OPENAI_CLI_INPUT_MODE=legacy openai responses create --model MODEL --input '[{"role":"user","content":"hello"}]'
OPENAI_CLI_INPUT_MODE=literal openai responses create --model MODEL --input 'Translate: hello'
```

The CLI captures `OPENAI_CLI_INPUT_MODE` before argument parsing.
Accepted values are `literal` and `legacy`; an unset setting selects `literal`.
Invalid values fail.
Legacy mode restores the original parser and its ordinary parser-before-file-expansion ordering.
Explicit `-structured` flags override legacy mode for their operands.
Only processes that inherit the setting use it.
Containers, SSH sessions, and environment-clearing launchers need separate configuration.
An explicit `literal` assignment overrides inherited `legacy` for that process.
The proposed legacy support duration still requires owner acceptance; no lifetime guarantee applies here.

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

`responses create` and `chat completions create` accept one JSON or YAML object through `--request-file`.
Use a filename on any shell. Use `-` to wait until the producer closes stdin.
The CLI rejects an empty, malformed, or multi-document request before sending it.

```sh
openai responses create --request-file request.json
printf '%s' '{"model":"MODEL","input":"hello","store":false}' | openai responses create --request-file -
```

Explicit flags override matching document fields.
A regular request file leaves stdin available for an explicit field such as `--input @-`.
Each command can use stdin once.
Both file and stdin documents honor `OPENAI_UNTRUSTED_STDIN`.

Other generated API commands check briefly for implicit whole-request input.
A producer that starts later can miss this check. In Bash or Zsh, finish writing the document before redirecting it.

```sh
openai embeddings create < request.json
```

Both input modes preserve whole-request JSON/YAML types and the existing trust policy.
Trusted piped values retain existing file-reference handling.
Unknown body fields and explicit nulls retain their existing encoding behavior.
Empty implicit stdin contributes no fields.
Required request fields still apply.

A final Boolean `stream: true` selects stream dispatch for supported JSON and multipart commands.
Explicit stream flags retain precedence.
Strings that resemble Booleans do not select stream dispatch.

Redirected event streams emit each formatted event without waiting for a page of output.
This includes JSON, JSONL, and extraction with raw output.
Terminal paging and finite-list output keep their existing behavior.

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
Successful completion preserves its inode, permissions, and hardlinks.
Linked names therefore continue to expose the saved bytes.

The final output entry must not be a symlink, including a dangling symlink or a chain.
To save through a link, select the target path explicitly instead.
Parent directories remain caller-trusted and retain normal symlink traversal;
this rule does not confine writes against attacker-controlled parents or hardlinks.

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
