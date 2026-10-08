# Local tokenizer and Codex helpers

These commands work offline without API credentials.
They do not save your text, edit settings, or install software.

## Count and inspect tokens

```sh
openai tokenizer count --text "Hello, world!"
openai tokenizer inspect --text "Hi!" --encoding cl100k_base
openai tokenizer count --file prompt.txt
printf 'Hello, world!' | openai tokenizer count
openai --format json tokenizer inspect --file prompt.txt
openai tokenizer encodings
openai tokenizer licenses
```

Choose `--text` or `--file`.
Either option overrides piped stdin without reading it.
Combining both options is an error.
Without either option, the command reads redirected stdin through EOF.
Use `--file -` to read interactive stdin through EOF.
Plain terminal input without an explicit source returns guidance instead of waiting.

`--text` treats `@`, JSON, YAML, and special-token spellings as ordinary text.
File paths are literal; `--file` does not expand `@` references.
Empty input produces zero tokens.
The tokenizer preserves whitespace, BOM, combining marks, and final newlines.
It performs no Unicode normalization and rejects invalid UTF-8.

The default encoding is `o200k_base`; `cl100k_base` is also available.
Use exact encoding names.
The command does not guess an encoding from a model name.

The input limit is 1 MiB.
Long unbroken text can take minutes; Ctrl+C stops the process.
The tokenizer processes the complete input because arbitrary chunking can change token boundaries.
This local limit does not change API request or response limits.

Default count output:

```text
Encoding: o200k_base
Input bytes: 13
Tokens: 4
```

Inspection includes each token ID and its half-open byte range `[start_byte, end_byte)`.
`bytes_hex` preserves each token's exact bytes.
JSON includes `text` only when the token fragment is valid UTF-8.
A token can split a Unicode character; concatenate its bytes to reconstruct the input.
Readable output quotes fragments and escapes terminal controls.

Counts describe plain text under the selected encoding.
They are not complete request counts, billed usage, prices, or context-limit guarantees.
Messages, tools, images, and files can add tokens.
The existing authenticated command remains available for server request counting:

```sh
openai responses input-tokens count --model YOUR_MODEL --input "Hello"
```

See the [official token-counting guide](https://developers.openai.com/api/docs/guides/token-counting).

## Codex instructions and destinations

```sh
openai codex                         # print installation and configuration instructions
openai --format json codex           # structured instructions
openai codex --destination config    # print a fixed documentation URL
openai codex --destination docs --open # explicitly request a browser launch
```

The OpenAI CLI command `openai codex` prints instructions for the separate Codex CLI executable, `codex`.
It never runs installation commands, launches an agent, logs in, or changes configuration.
Codex CLI and destination services handle their own sign-in and access checks.

| Destination | Purpose |
| --- | --- |
| `docs` | Codex CLI installation and usage documentation. |
| `config` | Codex configuration documentation. |
| `app` | Desktop app documentation and download instructions. |
| `web` | ChatGPT web, where supported cloud workflows are available. |

Default and piped output never open a browser.
`--open` requires an explicit destination and accepts no arbitrary URL.
The command prints the URL before requesting a browser launch.
If opening fails, the URL remains available and the command exits nonzero.

Instructions and destinations were verified on October 8, 2026.
Check [Codex CLI documentation](https://learn.chatgpt.com/docs/cli) for later installation changes.
See [configuration basics](https://learn.chatgpt.com/docs/config-file/config-basic) for configuration details.

## Formats, failures, and dependencies

Both utilities support the existing `--format` flag with `auto`, `text`, and `json`.
`auto` produces readable text, including through pipes.
Unsupported formats, `--transform`, and `--raw-output` return errors.
The existing `--format-error` behavior remains available.
Invalid input, read failures, opening failures, and output failures return nonzero statuses.
Interrupted output can be incomplete.

The tokenizer uses `github.com/tiktoken-go/tokenizer v0.7.0` and embedded OpenAI vocabulary data.
The dependency adds about 11 MB to an unstripped macOS executable.
The CLI never downloads vocabulary data during execution.
Version 0.7.0 matches the checked reference vectors; version 0.8.1 failed control-byte and whitespace cases.
Dependency upgrades must retain these reference checks.
`openai tokenizer licenses` prints the complete bundled notices.
