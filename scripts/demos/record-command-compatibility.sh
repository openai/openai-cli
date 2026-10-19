#!/bin/bash
# Put asciinema, agg, ffmpeg, ffprobe, expect, and python3 on PATH before running.
# Pass two built CLIs, their full source SHAs, and a fresh output directory outside Git.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record-command-compatibility.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
# No API fixture runs. The shell records transcribe arguments without executing them.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
demo_expect="$(command -v expect)"
demo_python="$(command -v python3)"
demo_window_size=100x26
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)

cat > "$demo_runtime/startup.bash" <<'STARTUP'
PS1='$ '; PS2='> '
set +o history
bind 'set bell-style none'
# Source the exact script emitted by this scene's CLI, without rewriting it.
source "$DEMO_EVIDENCE/completion.bash" || exit 91
openai() {
  case "${1-}" in
    __complete)
      printf '%s\0' "$@" >> "$DEMO_EVIDENCE/backend.argv" || return 92
      local status
      if command openai "$@"; then status=0; else status=$?; fi
      printf '%s\n' "$status" >> "$DEMO_EVIDENCE/backend.status" || return 92
      return "$status"
      ;;
    transcribe)
      printf '%s\0' "$@" > "$DEMO_EVIDENCE/accepted.argv" || return 92
      printf '\nAccepted file argument: %s\n' "${2-}"
      printf 'No transcription request was sent.\n'
      ;;
    *) printf 'Unexpected demo command.\n' >&2; return 93 ;;
  esac
}
STARTUP

cat > "$demo_runtime/entry.exp" <<'ENTRY'
set timeout 10
proc fail {message} {
  puts stderr $message
  catch {exec /bin/kill -KILL -- -[exp_pid]}
  catch {close}
  catch {wait}
  exit 94
}
expect_before timeout {fail "Bash completion recording timed out."}
spawn -noecho /bin/bash --noprofile --rcfile $env(DEMO_STARTUP) -i
expect -exact {$ }
send -- {openai transcribe --file='assets/lo'}
after 700
send -- "\t"
# Wait for a real backend call. Readline finishes before it accepts queued Enter.
set completed 0
for {set attempt 0} {$attempt < 100} {incr attempt} {
  if {[file exists "$env(DEMO_EVIDENCE)/backend.status"]} {
    set completed 1
    break
  }
  after 50
}
if {!$completed} {fail "Bash did not call the completion backend."}
after 900
send -- "\r"
expect -exact "\r\nNo transcription request was sent.\r\n"
expect -exact {$ }
after 700
send -- "exit\r"
expect eof
set result [wait]
# Do not open Tcl channels after wait; some system Expect builds reject reuse.
if {[lindex $result 2] != 0 || [lindex $result 3] != 0} {exit 95}
exit 0
ENTRY

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY OPENAI_ADMIN_KEY OPENAI_CUSTOM_HEADERS
cd "$DEMO_WORK"
command openai @completion bash > "$DEMO_EVIDENCE/completion.bash"
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
printf 'Bash Readline: Tab completes a closed quoted file value.\n'
printf 'Synthetic filename only; the shell records arguments without transcribing.\n\n'
"$DEMO_EXPECT" "$DEMO_ENTRY"
printf '\nHelp with a literal --project value (first 7 output lines):\n'
printf '$ openai help --project '\''--help'\'' models list\n\n'
if command openai help --project '--help' models list \
  > "$DEMO_EVIDENCE/help.stdout" 2> "$DEMO_EVIDENCE/help.stderr"; then
  demo_status=0
else
  demo_status=$?
fi
printf '%s\n' "$demo_status" > "$DEMO_EVIDENCE/help.status"
cat "$DEMO_EVIDENCE/help.stdout" "$DEMO_EVIDENCE/help.stderr" | sed -n '1,7p'
command openai help models list > "$DEMO_EVIDENCE/help-reference.stdout" \
  2> "$DEMO_EVIDENCE/help-reference.stderr"
sleep 3
exit "$demo_status"
SCENE

{
  echo 'feature: quoted Bash file completion and literal values during help routing'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'capture: native Bash Readline in an isolated PTY, with actual emitted completion and CLI backend'
  echo 'scope: Bash PTY replay, not native graphical terminal validation'
  echo 'fixture: empty assets/logo.wav; no audio, credentials, API requests, or profile installation'
  echo 'transcribe: wrapper records accepted argv; it never invokes the API command'
  echo 'help: first seven output lines displayed; complete stdout, stderr, and status retained'
  echo 'startup: isolated HOME and INPUTRC; CI disables optional first-run shell setup'
  echo "terminal dimensions: $demo_window_size"
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-command-compatibility.sh" "$demo_expect" "$demo_python"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_runtime/startup.bash" "$demo_runtime/entry.exp" "$demo_output/"

for demo_scene in before after; do
  demo_status=3
  demo_label='Before: quoted completion duplicates --file; help loses a literal value'
  if [ "$demo_scene" = after ]; then
    demo_status=0
    demo_label='After: complete the filename; preserve the literal help value'
  fi
  demo_evidence="$demo_output/$demo_scene-evidence"
  demo_work="$demo_runtime/$demo_scene-work"
  mkdir -p "$demo_evidence" "$demo_work/assets" "$demo_work/home"
  : > "$demo_work/assets/logo.wav"
  demo_capture_scene "$demo_scene" "$demo_status" "$demo_runtime/$demo_scene" http://127.0.0.1:1 \
    "$demo_label" "DEMO_WORK=$demo_work" "DEMO_EVIDENCE=$demo_evidence" \
    "DEMO_EXPECT=$demo_expect" "DEMO_ENTRY=$demo_runtime/entry.exp" \
    "DEMO_STARTUP=$demo_runtime/startup.bash" "HOME=$demo_work/home" \
    "XDG_CONFIG_HOME=$demo_work/home" "XDG_CACHE_HOME=$demo_work/home" \
    INPUTRC=/dev/null HISTFILE=/dev/null BASH_ENV=/dev/null ENV=/dev/null \
    BASH_SILENCE_DEPRECATION_WARNING=1 CI=1 NO_COLOR=1 \
    HTTP_PROXY=http://127.0.0.1:1 HTTPS_PROXY=http://127.0.0.1:1 NO_PROXY=127.0.0.1
done

"$demo_python" -I -B - "$demo_output" <<'VALIDATE' > "$demo_output/validation.txt"
import pathlib, sys

output = pathlib.Path(sys.argv[1])
def argv(path):
    data = path.read_bytes()
    assert data.endswith(b'\0'), path
    return data[:-1].split(b'\0')

for scene, argument, status in (
    ('before', b'--file=--file=assets/logo.wav', '3'),
    ('after', b'--file=assets/logo.wav', '0'),
):
    evidence = output / (scene + '-evidence')
    assert argv(evidence / 'accepted.argv') == [b'transcribe', argument]
    assert argv(evidence / 'backend.argv') == [b'__complete', b'--', b'transcribe', b'--file=assets/lo']
    assert (evidence / 'backend.status').read_text() == '10\n'
    assert (evidence / 'help.status').read_text() == status + '\n'
    assert not (evidence / 'help-reference.stderr').read_bytes()
    stdout = (evidence / 'help.stdout').read_bytes()
    stderr = (evidence / 'help.stderr').read_bytes()
    canonical = (evidence / 'help-reference.stdout').read_bytes()
    assert b'openai models list' in canonical
    if scene == 'before':
        assert not stdout and stderr
    else:
        assert not stderr and stdout == canonical
    transcript = (output / (scene + '.txt')).read_text()
    assert 'Accepted file argument: ' + argument.decode() in transcript
    assert 'No transcription request was sent.' in transcript
    assert "$ openai help --project '--help' models list" in transcript
print('PASS: real Bash Tab completion preserves the expected argv before and after.')
print('PASS: literal help value recovers exact canonical help; complete streams and statuses retained.')
VALIDATE
shasum -a 256 "$demo_output/"*-evidence/completion.bash >> "$demo_output/metadata.txt"
demo_assemble_capture 300 before after
printf 'Recorded command compatibility comparison in %s\n' "$demo_output"
