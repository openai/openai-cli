# Flag value completion

Shell completion suggests known values without an API key or network connection.
Load the existing [completion script](shell-completion.md) for your shell.

Type a prefix, then press Tab:

```text
openai --format j<Tab>
json  jsonl

openai files upload sample.jsonl --purpose b<Tab>
batch
```

Suggestions cover these existing flags:

- `--format` and `--format-error` suggest the CLI's supported output formats.
- Local Codex, tokenizer, image preview, and image preference commands suggest their supported `--format` values.
- `files upload --purpose` and `files create --purpose` suggest supported upload purposes.
- `files list --purpose` also suggests known output purposes, such as `batch_output`.

Upload purposes are `assistants`, `batch`, `evals`, `fine-tune`, `user_data`, and `vision`.
The [Files upload contract](https://developers.openai.com/api/reference/resources/files/methods/create) defines these values.
The [Files list contract](https://developers.openai.com/api/reference/resources/files/methods/list) accepts a free-form purpose filter.
Suggestions do not restrict request values or choose a default purpose.

Both `--format json` and `--format=json` support completion.
Root flags also complete after nested commands, such as `openai responses create --format j`.
Existing file completion, command aliases, and free-form flags keep their behavior.
No new command, flag, shell setup, or request validation is added.
