# Runnable workflow examples

Use `openai examples` to find built-in recipes.
It shows copyable commands for the three topics and complete help.
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

## Try the offline command

```sh
openai examples
openai examples files
openai --format json examples files
openai examples --help
```

These commands require an available `openai` executable, but no API credentials or input files.
The first command lists the topics. The next two print the Files recipe as text and JSON.
Complete help remains available through `--help`, `-h`, and `openai help examples`.
Printing examples creates no files or remote resources, so it needs no cleanup.

The recipes below require their named inputs only when you run the printed API commands.

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
These are independent alternatives, not a sequence that stops after an earlier command fails.
Use a supported recording within the API's 25 MB limit.
`whisper-1` supports translation and SRT output.
OpenAI has announced its shutdown for February 26, 2027.
This date was verified on October 9, 2026; check the [deprecation notice](https://developers.openai.com/api/docs/deprecations#2026-08-26-transcription-models) before reuse.
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
Bare `openai examples` lists copyable topic commands in automatic or text format.

Other formats, `--transform`, and `--raw-output` are unsupported for the examples command.
Those flags still work in the printed API commands.
Unknown topics, extra arguments, and output failures return nonzero status.
Output failures can leave incomplete text or JSON.
`--quiet` preserves the recipe; `--verbose` follows the existing diagnostic policy.
The examples command does not execute requests or store shorthand commands.
Copied API commands retain their existing retry behavior.

An invalid recipe topic, extra argument, or unsupported known format prints one `Try:` command on stderr.
Rejected extraction flags also include this command.
Unknown CLI flags or format names retain the existing parser guidance.
That command prints local guidance or a recipe. It never executes the recipe or replays rejected values.
For example:

```sh
openai examples files --format yaml   # fails with a local recovery command
openai examples files --format text   # prints the recipe successfully
```

Keep the printed executable path when following recovery guidance from a local build.
Output failures can prevent complete instructions from appearing. Fix the output destination before rerunning.

If you paste the Files recipe into your current shell, a successful upload leaves its ID in `file_id`.
After a later failure, inspect that ID before repeating the upload:

```sh
printf '%s\n' "$file_id"
openai files get "$file_id"
```

A separate shell script does not retain its variables after it exits.
Keep returned IDs with your own workflow records when running recipes that way.
Retry only the failed download when the file already exists remotely.
See [Files recovery](files.md#recover-from-a-failed-command) for partial transfers and local write failures.
