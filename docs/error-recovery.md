# Recovering from command errors

Command errors go to stderr and keep their failing exit status.
The CLI suggests a correction but never runs it.

```text
$ openai modles list
Unknown command. Did you mean: openai models list?

$ openai help modles list
Unknown help topic. Did you mean: openai help models list?

$ openai models list --file missing.txt
The --file option is not available for this command.
Options and examples: openai models list --help

$ openai files upload missing.txt --purpose user_data
Could not open the file for --file. Check the path and permissions.

$ openai uploads parts create --upload-id upload_demo --data missing.bin
Could not open the file for --data. Check the path and permissions.
```

A suggestion can retain declared subcommands after the corrected command.
Suggestions omit options and their values.
They stop before any remaining argument that is not a declared subcommand.
It never repeats option values, private paths, credentials, or request contents.

If an option belongs to another public command, the error identifies that option.
It links to help for the current command without guessing a different command.
Existing suggestions for similar options in the current command remain available.

File errors identify the declared option that supplied the failed file input.
This includes file references in request options and piped JSON or YAML.
Unknown fields keep general file guidance.
Read and cleanup failures retain their underlying causes.

Existing flag guidance still identifies missing values, expected types, and required options.
Use the suggested help command to inspect options in the correct scope.

Explicit error formats remain available:

```sh
openai --format-error json modles list
openai --format-error jsonl files upload missing.txt --purpose user_data
openai --format-error json --transform-error message modles list
```

The first command emits a JSON object with a `message` field.
The final command extracts that field.
These local errors do not invent an HTTP status or request ID.
