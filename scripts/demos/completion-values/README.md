# Completion value evidence

This demo invokes each binary's actual Bash completion callback inside a PTY.
The terminal replay shows candidate values for four command lines.
It uses no API requests or personal shell configuration.

Record two committed binaries:

```sh
bash scripts/demos/completion-values/record.sh \
  /absolute/before/openai /absolute/after/openai \
  BEFORE_SHA AFTER_SHA /absolute/empty-output-directory
```

The recorder reuses `scripts/demos/capture_and_render.sh`.
It asserts candidate values and preserves transcripts, source hashes, GIFs, and screenshots.
Keep media outside Git.

Check actual Tab insertion separately:

```sh
python3 scripts/demos/completion-values/native_check.py \
  /absolute/after/openai /absolute/native-results
```

The native check opens isolated Bash and Zsh PTYs.
It captures the command buffer after Tab without executing that command.
Machine protocol tests separately cover Bash, Zsh, Fish, and PowerShell payloads.
Native shell execution on macOS does not establish native Windows behavior.

Check driver cleanup without native processes, signals, or PTYs:

```sh
python3 -B scripts/demos/completion-values/native_check_test.py
```
