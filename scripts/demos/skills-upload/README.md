# Skills upload repair

Existing commands accept a ZIP or a directory:

```sh
openai skills create --files ./demo-skill
openai skills versions create --skill-id skill_example --files ./demo-skill.zip --default false
openai --format json skills create --files ./demo-skill.zip
```

A directory upload includes every regular file recursively, including hidden files.
Use a dedicated skill directory. The CLI does not apply ignore files.
The ZIP contains one top-level folder named after the selected directory.
Nested relative paths and executable permission bits survive packaging.
Entry order and timestamps are stable. Symlinks and special files cause an error.
Keep source files unchanged while the CLI packages them.

The CLI stages directory ZIPs in private temporary files and removes them after the command.
Packaging uses bounded copy buffers. Temporary disk space must accommodate the ZIP.
The API validates manifests, archive contents and service limits. The CLI adds no upload-size limit.
Existing ZIPs retain their exact bytes and filename, including malformed archives that the API can reject.

Repeated `--files` values upload individual multipart files.
Relative paths retain their directory components. Absolute paths use their common parent folder as the upload root.
Use a directory or ZIP to preserve deeper layout and executable metadata reliably.
Plain paths remain literal: a leading `@` belongs to the filename.
Use `--files -` for one ZIP from stdin; its multipart filename is `skill.zip`.
Trusted piped JSON/YAML inputs remain available.

Upload streams do not retry automatically. Check remote state before repeating an interrupted request.
Local ZIP and individual-file read errors identify `--files`. Cleanup guidance remains visible beside preparation failures.
Existing readable results, explicit formats, extraction, lifecycle commands and content downloads remain available.

## Local reproduction

Build baseline and candidate binaries in separate output locations.
Use the exact pinned baseline and candidate commits in the recording arguments.

```sh
bash scripts/demos/skills-upload/record.sh /path/to/before/openai /path/to/after/openai BEFORE_SHA AFTER_SHA /tmp/skills-demo
```

The recorder uses the shared asciinema/agg capture lifecycle and a synthetic localhost API.
It creates a small skill with nested binary data and an executable helper.
The fixture compares supplied ZIP bytes and inspects packaged entries before returning success.
All files, request logs, captures and screenshots stay outside Git.
The recording proves local wire behavior. It does not prove live API acceptance or stored-content fidelity.

The default replay uses 100 columns and 40 rows with an inherited monochrome CLI palette.
Set `SKILL_DEMO_COLUMNS=40 SKILL_DEMO_ROWS=60` for a narrow terminal.
Set `SKILL_DEMO_THEME=github-light` for a light-background replay.
Use a fresh output directory for each recording.
