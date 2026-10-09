# Shell and file comparison

The recording uses real CLI binaries and a synthetic loopback API.
The scenes show explicit text stdin and preservation after a truncated download.
No live API calls or uploads occur.
Generated stderr-receipt and manpage updates remain deferred.

Build the fixture:

```sh
GOMAXPROCS=2 go build -p 2 -o /tmp/shell-files-demo ./scripts/demos/shell-files
```

Record a committed candidate against the pinned baseline:

```sh
scripts/demos/shell-files/record.sh \
  /path/to/before/openai /path/to/after/openai \
  BEFORE_SHA AFTER_SHA /path/outside/repository/recording /tmp/shell-files-demo
```

Use an empty output directory.
The shared recorder captures source identities, native environment details, hashes, terminal recordings, GIFs, and screenshots.
The assertions inspect command exit status, captured output, and exact file bytes.
Keep binary media outside Git.
