# Runnable workflow examples

Use `openai examples` to find built-in recipes.
The command prints instructions offline without credentials.
It never runs commands, reads input files, opens a browser, or saves files.

```sh
openai examples files
openai examples audio
openai examples models
```

Recipes use POSIX shell syntax, including bash and zsh.
They expect `openai` on your `PATH`.
They are not PowerShell, cmd.exe, or fish scripts.
Comments identify required local input files.
Replace those paths before copying commands into your shell.
Copied API commands require credentials and can incur charges.
Configure `OPENAI_API_KEY` separately; never paste credentials into a recipe.

## Files

The Files recipe uploads an existing file with explicit `user_data` purpose.
It extracts the returned ID, retrieves metadata, and downloads the contents.
Each command starts only after the previous command succeeds.
The API still validates file types, purposes, and access.

```sh
file_id=$(openai --format json --transform id --raw-output files upload "./upload sample.txt" --purpose user_data) &&
openai files get "$file_id" &&
openai files download "$file_id" --output "./downloaded copy.txt"
```

Upload paths are plain filenames; do not add `@`.
Keep paths and variable expansions quoted.
The download replaces the output file after a successful transfer.
An upload can remain on the server if a later command fails.
Inspect the earlier result before repeating an upload.
Successful upload does not mean processing has completed.
See [Files workflows](files.md) and the [Files API](https://developers.openai.com/api/reference/resources/files/methods/create).

## Audio

The audio recipe uses an existing recording for three separate requests:

- Transcription in the recording's language.
- Translation into English.
- Transcription with SRT subtitle timing.

```sh
openai --format raw audio transcribe --file "./speech sample.wav" --model whisper-1 --response-format text
openai --format raw audio translate --file "./speech sample.wav" --model whisper-1 --response-format text
openai --format raw audio transcribe --file "./speech sample.wav" --model whisper-1 --response-format srt
```

Each command prints its result to stdout.
Run only the command you need.
`whisper-1` supports translation and SRT output.
Explicit raw format preserves the returned text and subtitle bytes.
Model availability and input acceptance depend on the API.
See [audio output](readable-audio.md) and the [speech guide](https://developers.openai.com/api/docs/guides/speech-to-text).

## Model IDs

The models recipe prints available model IDs, one per line.
It disables the item limit and applies extraction to each model.

```sh
openai --format json --transform id --raw-output models list --max-items -1
```

The resulting lines are plain text, without JSON quotes.
The API controls which models your credentials can see.
This command does not run a model.

## Output and failures

Automatic and explicit text output contain the same script bytes.
Pipes, terminal width, and `NO_COLOR` do not change those bytes.
Long command lines can wrap visually; the command does not insert line breaks.

```sh
openai --format json examples files
```

JSON output contains `topic`, `shell`, and `script`.
`shell` is `sh`; `script` contains the complete recipe, including comments and its final newline.
Choose a topic for JSON output.
Bare `openai examples` shows help in automatic or text format.

Other formats, `--transform`, and `--raw-output` are unsupported for the examples command.
Those flags still work in the printed API commands.
Unknown topics, extra arguments, and output failures return nonzero status.
Output failures can leave incomplete text or JSON.
`--quiet` preserves the recipe; `--verbose` follows the existing diagnostic policy.
The examples command does not execute requests or store shorthand commands.
Copied API commands retain their existing retry behavior.
