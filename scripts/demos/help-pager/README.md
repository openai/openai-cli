# Startup help comparison

Bare `openai` opens the complete help at the banner in interactive terminals.
Use arrows, Page Up/Down, Space, Home/End, or the mouse wheel to scroll.
Press `q`, Escape, or Enter to return to the shell. Ctrl+C exits with status 130.
`openai --help`, subcommands, pipes, CI, and redirected streams retain ordinary output.
Windows requires a native terminal; unsupported terminal inputs retain ordinary output.
Classic Windows consoles without TERM or WT_SESSION also retain ordinary output.
Automatic update notices appear after the viewer closes successfully.

Build the baseline and proposed CLI separately. Build the existing rejecting fixture:

```sh
go build -o /tmp/help-demo-api ./scripts/demos/image-models
```

Record with the existing asciinema, agg, ffmpeg, and Python tools on PATH:

```sh
DEMO_API_BINARY=/tmp/help-demo-api bash scripts/demos/help-pager/record.sh \
  /absolute/before/openai /absolute/after/openai \
  BEFORE_COMMIT AFTER_COMMIT /absolute/new-output-directory
```

The script reuses the shared capture lifecycle and isolates configuration and credentials.
It records actual CLI output, asserts zero API requests, and generates before/after images and a GIF.
The images show terminal replay output. They do not establish native graphical-terminal or Windows behavior.
Keep generated media outside the repository.
