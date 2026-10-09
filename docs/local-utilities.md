# Local tokenizer and Codex helpers

These commands work offline without API credentials.
They do not save your text, edit settings, or install software.

## Explore tokens interactively

```sh
openai tokenizer
```

A capable terminal opens the live editor with the same theme as image generation.
Type or paste text to see its exact token count.
The editor supports up to 1 MiB of UTF-8 input.

- Press Tab to focus the visible View control.
- Press Left or Right on View to switch Text, Token IDs, and Bytes immediately.
- Press Enter on View to open the view chooser.
- Press Down to select the Model row.
- Press Enter on Model to open its chooser.
- Press Up or Down in a chooser to highlight a choice.
- Press Enter to apply that choice.
- Press Escape to cancel a chooser and return to text.
- Press Tab from options to focus the tokens.
- Press Left or Right in the tokens to select a token.
- Press Up from the tokens to return to Model settings.
- Press Enter on a token to inspect its ID, byte offsets, and every byte.
- Press F1 for all controls and script examples.
- Press Ctrl+C to exit.

Enter inserts a newline while editing text.
Down opens options from any column on the last line.
On earlier lines, Down moves the text cursor. Up moves to the previous text line.
The token marked with a dot follows the text cursor without recalculating tokens.
The cursor highlights the character at its position without inserting a gap between letters.
At a token boundary, the marker selects the token on the right. At the end, it selects the last token.
Tokens can split a Unicode character; cursor movement still follows complete graphemes.
Tab and Shift+Tab cycle through text, options, and tokens.
Chooser focus uses the image picker's bold highlight. A checkmark identifies the applied choice.
The main screen shows model families instead of encoding identifiers.
It shows the token count and, for multiple tokens, the selected position.
Colored token backgrounds reveal boundaries and stay consistent across Text, Token IDs, and Bytes.
The Bytes view adds the input byte count; full hexadecimal details remain available through Enter.
Details quotes text to make leading spaces and escaped characters visible.
Scrolling controls appear when the complete details exceed the visible panel.
Text and Token IDs are the primary reading views. Bytes supports exact-byte inspection and debugging.
Ctrl+U removes text before the cursor, matching the image prompt editor.
Paste preserves whitespace, line endings, and Unicode normalization.
The editor escapes control characters for display.
The Bytes view exposes token fragments that split a Unicode character.

Editing clears stale results immediately.
The editor cancels superseded computations and displays only the current revision.
Long unbroken text can take time; editing and quitting remain available during computation.
Terminal output runs independently, so a blocked display does not stop keyboard handling.
If computation fails, edit the text or press `r` while Tokens has focus.
The draft remains available until you exit; the CLI never saves it.

The editor requires terminal input and output, automatic format, and at least 40 columns by 12 rows.
Pipes, CI, `TERM=dumb`, and explicit text format receive command guidance without consuming stdin.
`NO_COLOR` retains keyboard controls and visible focus markers.
It retains the monochrome text cursor while suppressing token colors.
Use `count` or `inspect` for JSON output and scripts.

## Count and inspect tokens

```sh
openai tokenizer count --text "Hello, world!"
openai tokenizer inspect --text "Hi!" --encoding cl100k_base
openai tokenizer count --text "    return value" --encoding p50k_base
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
These input rules apply to `count` and `inspect`, not the live editor.

`--text` treats `@`, JSON, YAML, and special-token spellings as ordinary text.
File paths are literal; `--file` does not expand `@` references.
Empty input produces zero tokens.
The tokenizer preserves whitespace, BOM, combining marks, and final newlines.
It performs no Unicode normalization and rejects invalid UTF-8.

The default encoding remains `o200k_base`.
The tokenizer also supports `cl100k_base`, `r50k_base`, and `p50k_base` offline.
`cl100k_base` supports the GPT-4 and GPT-3.5 families.
The legacy `r50k_base` encoding supports GPT-3 models such as `davinci`.
The legacy `p50k_base` encoding supports original Codex models and `text-davinci-002`/`text-davinci-003`.
These model families follow the [OpenAI tokenizer reference](https://developers.openai.com/cookbook/examples/how_to_count_tokens_with_tiktoken).
The original Codex models differ from the current Codex CLI.
Use exact encoding names.
The command does not guess an encoding from a model name.
Token IDs follow OpenAI tiktoken 0.14.0 with fixed Unicode 16 character rules.
Compiler upgrades do not change those rules.
Legacy encodings preserve their case-sensitive contractions and distinct whitespace tokens.

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
Automatic terminal output groups installation, startup, configuration, and official links in the CLI theme.
Explicit text and JSON formats retain their plain output contracts.
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
The system browser launcher receives only named desktop, profile, and locale settings.
Linux retains the `BROWSER` fallback supported by `xdg-open`.
If opening fails, the URL remains available and the command exits nonzero.

Instructions and destinations were verified on October 8, 2026.
Check [Codex CLI documentation](https://learn.chatgpt.com/docs/cli) for later installation changes.
See [configuration basics](https://learn.chatgpt.com/docs/config-file/config-basic) for configuration details.

## Formats, failures, and dependencies

Tokenizer subcommands and Codex support `--format auto`, `text`, and `json`.
Bare `openai tokenizer` requires `count` or `inspect` for JSON output.
`auto` produces readable text through pipes.
Unsupported formats, `--transform`, and `--raw-output` return errors.
The existing `--format-error` behavior remains available.
Invalid input, read failures, opening failures, and output failures return nonzero statuses.
Interrupted output can be incomplete.

## Recover from a failure

Check the exit status before using output in a script.
Do not treat partial JSON or a printed Codex URL as proof that the command succeeded.
Script commands never retry, change your input, install a browser, or choose a different encoding automatically.
The live editor replaces tokenization work when you edit text or select another encoding.

| Failure | What remains | What to do next |
| --- | --- | --- |
| Live preview fails | Your draft remains; stale results disappear. | Edit the text or press `r` with results selected. Use `tokenizer inspect` for plain output. |
| Invalid or oversized paste | The editor keeps the complete previous draft. | Paste valid UTF-8 within the 1 MiB total limit. The editor never truncates accepted input. |
| Terminal becomes too small | The draft and current results remain in memory. | Resize to at least 40 columns by 12 rows, or press Ctrl+C. |
| Editor startup or display fails | The CLI attempts terminal restoration and exits nonzero. | Use `tokenizer count` or `tokenizer inspect` with an explicit source. |
| Terminal stops accepting output | Cancellation still restores native terminal modes. Final display cleanup may not reach the terminal. | Press Ctrl+C to exit, then restore terminal output before continuing. |
| Editor exits or receives a termination signal | The draft is discarded; no files or settings change. | Keep important source text in your own file before opening the editor. |
| No input in a terminal | No token result. | Supply `--text`, `--file`, or a pipe. Use `--file -` for interactive stdin. |
| Conflicting or repeated input options | No token result; input stays unread. | Supply each option once and choose either `--text` or `--file`. |
| Unknown encoding or option | No token result. | Run `openai tokenizer encodings` or command `--help`. Use an encoding name, not a model name. |
| Invalid UTF-8 | No token result; source bytes remain unchanged. | Convert a copy from its known source encoding to UTF-8. Do not discard invalid bytes silently. |
| Input exceeds 1 MiB | No token result; source bytes remain unchanged. | Choose a smaller complete input. Counts from arbitrary chunks may differ from the complete text. |
| File cannot open or read | No token result. | Check the supplied path, file type, permissions, and producer. The error does not print private paths. |
| Input producer has not closed stdin | The command waits for EOF. | Finish the producer or send EOF. Press Ctrl+C to stop. |
| Producer fails after writing a valid prefix | The tokenizer can successfully count that prefix. | Check the producer's exit status too. Bash and zsh can use `set -o pipefail`. |
| Unsupported output format or transformation | No result. | Use `--format text` or `--format json`; remove `--transform` and `--raw-output`. |
| Output file or pipe fails | Previously written bytes can remain incomplete. | Fix the output destination and rerun. Discard partial output; do not append a retry to it. |
| Tokenization is interrupted | No complete result is promised. | Rerun only if you still need the result. Long unbroken input can remain slow. |
| Missing or unknown Codex destination | No browser launches. | Choose `docs`, `config`, `app`, or `web`. `--open` requires an explicit destination. |
| Browser launcher is missing, fails, or exceeds ten seconds | The printed URL remains; the command exits nonzero. | Check whether the page opened. Otherwise, open the printed URL manually. |

An output failure prevents `--open` from launching a browser.
A browser launch can partially succeed before its launcher reports an error.
Cancellation terminates the launcher; it does not close an already launched browser.
The CLI cannot verify page loading, network access, or destination sign-in.
If stderr also fails, an explanatory message might be unavailable; the exit status still indicates failure.
The tokenizer cannot distinguish normal EOF from a producer that failed after writing data.
Without `pipefail`, a shell pipeline can hide the producer's failure behind the tokenizer's successful exit.

For file output, write to a temporary file and replace the final file only after success:

```sh
if [ -d tokens.json ]; then
  printf '%s\n' 'tokens.json is a directory; choose an output file.' >&2
  exit 1
fi
token_result=$(mktemp './tokens.json.XXXXXX') || exit 1
trap 'rm -f "$token_result"' EXIT
if openai --format json tokenizer inspect --file prompt.txt >"$token_result"; then
  mv "$token_result" tokens.json
else
  token_status=$?
  printf '%s\n' 'Tokenization failed; existing tokens.json was kept.' >&2
  exit "$token_status"
fi
```

Shell redirection alone can truncate an existing file before the CLI starts.
The temporary-file example preserves an existing result when tokenization fails.

## Tokenizer dependencies

The tokenizer reads embedded OpenAI vocabularies through `github.com/tiktoken-go/tokenizer v0.7.0`.
It uses `github.com/pkoukk/tiktoken-go v0.1.8` for ordinary BPE encoding with fixed Unicode 16 rules.
Each encoding initializes once, and both counting and inspection validate every returned byte against the input.
The CLI never downloads vocabulary data during execution.
Dependency and Unicode updates must retain reference checks across supported Go versions.
`openai tokenizer licenses` prints the complete bundled notices.
