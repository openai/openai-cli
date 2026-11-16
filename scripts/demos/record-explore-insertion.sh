#!/bin/bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo 'usage: record-explore-insertion.sh CANDIDATE_BINARY CANDIDATE_SHA EMPTY_OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
# This single scene uses the candidate for both recorder inputs.
demo_prepare_capture "$demo_root" "$1" "$1" "$2" "$2" "$3" /usr/bin/true
build_info="$(go version -m "$demo_after")"
grep -Fqx $'\tbuild\tvcs.revision='"$demo_after_sha" <<< "$build_info"
grep -Fqx $'\tbuild\tvcs.modified=false' <<< "$build_info"

mkdir -m 700 "$demo_runtime/archive" "$demo_runtime/home"
for file in install.sh openai-enable-updates.sh; do
  git -C "$demo_root" show "$demo_after_sha:$file" > "$demo_runtime/archive/$file"
done
cp "$demo_after" "$demo_runtime/archive/openai"
# The archive installer requires the GitHub CLI executable on PATH.
gh_dir="$(dirname "$(command -v gh)")"

# These nonprinting probes only observe zsh's editor and submitted commands.
cat > "$demo_runtime/home/.zshrc" <<'PROFILE'
PS1='$ '
capture_buffer() { print -r -- "$BUFFER" >> "$DEMO_BUFFERS"; }
zle -N capture_buffer
bindkey '^]' capture_buffer
preexec() { print -r -- "$1" >> "$DEMO_SUBMITTED"; }
PROFILE

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
unset OPENAI_API_KEY
cd "$DEMO_ARCHIVE"
# The inner zsh PTY handles newline conversion.
stty -opost
exec /usr/bin/expect "$DEMO_DRIVER"
SCENE

cat > "$demo_runtime/drive.exp" <<'DRIVER'
set timeout 15
set stty_init "rows 16 columns 84"
spawn -noecho /bin/zsh -di
expect_before timeout {exit 91} eof {exit 92}
expect -exact {$ }
send -- {source ./openai-enable-updates.sh}
expect -exact {source ./openai-enable-updates.sh}
after 400
send -- "\r"
expect -exact {This shell now uses $HOME/.openai/bin/openai.}
expect -exact {$ }
after 1000
send -- "\014"
expect -exact {$ }
send -- {openai explore}
expect -exact {openai explore}
after 450
send -- "\r"
expect -exact {Explore commands}
after 750
send -- {modles}
expect -exact {modles}
after 320
send -- { list}
expect -exact {accessible model IDs and owners.}
after 1050
send -- "\r"
expect -exact {openai models list}
send -- "\035"
after 850
send -- { --}
expect -exact { --}
after 250
send -- {help}
expect -exact {help}
send -- "\035"
after 1350
close
wait
DRIVER

{
  echo "Candidate: $demo_after_sha"
  printf 'Recipe: %q %q %q %q\n' "$0" "$demo_after" "$demo_after_sha" "$demo_output"
  echo '84 columns by 16 rows. Real extracted-archive install and first Explore in one zsh PTY.'
  echo "GitHub CLI: $gh_dir/gh"
  "$gh_dir/gh" --version | sed -n '1p'
  /bin/zsh --version
  demo_capture_metadata
  shasum -a 256 "$0" "$demo_runtime/archive/install.sh" "$demo_runtime/archive/openai-enable-updates.sh"
} > "$demo_output/metadata.txt"

demo_window_size=84x16
demo_render_options=(--font-family Menlo --font-size 18 --theme dracula --last-frame-duration 2)
demo_capture_scene explore 0 "$gh_dir" 'http://127.0.0.1:1/v1' 'Explore after archive installation' \
  SHELL=/bin/zsh HOME="$demo_runtime/home" ZDOTDIR="$demo_runtime/home" XDG_CONFIG_HOME="$demo_runtime/home/config" \
  COLORTERM=truecolor COLORFGBG='15;0' OPENAI_CLI_NO_UPDATE_CHECK=1 OPENAI_CLI_NO_AUTO_UPDATE=1 \
  DEMO_ARCHIVE="$demo_runtime/archive" DEMO_DRIVER="$demo_runtime/drive.exp" \
  DEMO_BUFFERS="$demo_output/buffers.txt" DEMO_SUBMITTED="$demo_output/submitted.txt"
printf '%s\n' 'openai models list' 'openai models list --help' | cmp - "$demo_output/buffers.txt"
printf '%s\n' 'source ./openai-enable-updates.sh' 'openai explore' | cmp - "$demo_output/submitted.txt"
grep -Fq '$ openai models list --help' "$demo_output/explore.txt"
"$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/explore.png" \
  -vf 'crop=iw:80:0:0' -frames:v 1 "$demo_output/prompt.png"
"$demo_ffprobe" -v error -select_streams v:0 -show_entries stream=width,height,nb_frames,duration \
  -of json "$demo_output/explore.gif" > "$demo_output/media.json"
