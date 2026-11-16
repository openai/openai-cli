package autocomplete

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuggestionNativeZshRecognizesStandaloneHelp(t *testing.T) {
	probe := `cat >"$SUGGESTION_TEST_DIR/openai" <<'CLI'
#!/bin/sh
printf '%s\n' "${OPENAI_CLI_BARE_INPUT:-plain}" >> "$SUGGESTION_TEST_DIR/grants"
CLI
`
	dir := runSuggestionShellPTY(t, "zsh", "", probe, false, `
foreach line {
  {openai}
  {openai; print CHAIN_CONTINUED}
  {true && openai}
  {openai && openai}
  {openai | cat}
  {openai > /dev/null}
  {openai --help}
  {command openai}
  {( openai )}
  {function call_cli { openai; }; call_cli}
  {eval openai}
  { openai  }
} {
  send -- "$line\r"
  expect -exact "SUGGESTION_TEST> "
}
send -- "exit\r"
expect eof
`)
	grants, err := os.ReadFile(filepath.Join(dir, "grants"))
	require.NoError(t, err)
	// Both sides of "openai && openai" invoke the executable.
	require.Equal(t, "1\n"+strings.Repeat("plain\n", 11)+"1\n", string(grants))
}

func TestSuggestionNativeZshBareHelpDraft(t *testing.T) {
	// This synthetic CLI writes an independent suggestion and returns the draft
	// exactly as recorded. The capture widget reads ZLE without accepting the line.
	// Close Expect's inherited descriptors before testing both free and occupied FD6.
	probe := `exec 5>&- 6>&-
export OPENAI_CLI_HELP_DRAFT_FD=97
precmd() {
  local rc=$?
  if (( rc == 23 )); then print -r -- "$rc" > "$SUGGESTION_TEST_DIR/result"; fi
}
capture_buffer() {
  printf '%s\000' "$BUFFER" >>"$SUGGESTION_TEST_DIR/draft-buffers"
  BUFFER=''
  printf '\nBUFFER_CAPTURED\n'
}
cat >"$SUGGESTION_TEST_DIR/openai" <<'CLI'
#!/bin/sh
dir=$SUGGESTION_TEST_DIR
mode=$(cat "$dir/mode")
printf '%s:%s:%s\n' "$mode" "$OPENAI_CLI_HELP_DRAFT_FD" "$OPENAI_CLI_COMMANDS_FD" >>"$dir/grants"
if [ "$OPENAI_CLI_HELP_DRAFT_FD" = 6 ]; then
  for file in "$TMPDIR"/openai-help-draft.*; do [ ! -e "$file" ] || exit 91; done
  case "$mode" in
    good|failed) printf '%s\000' '  quit 雪 🧑‍💻 $(touch unintended); echo "$HOME"  ' >&6 ;;
    paste) printf 'touch unintended\necho second\n\000' >&6 ;;
    partial) printf 'unfinished' >&6 ;;
    extra) printf 'one\000tail' >&6 ;;
    control) printf 'bad\033draft\000' >&6 ;;
    c1) printf 'bad\302\233draft\000' >&6 ;;
    invalid) printf 'bad\377draft\000' >&6 ;;
    truncated) printf 'bad\341\200\000' >&6 ;;
    nul) printf 'bad\000\000' >&6 ;;
  esac
fi
if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ]; then printf 'openai models list\n' >&9; fi
if [ "$mode" = occupied ]; then printf 'caller keeps fd6\n' >&6; fi
if [ "$mode" = failed ]; then exit 23; fi
CLI
`
	dir := runSuggestionShellPTY(t, "zsh", "", probe, false, `
foreach {mode invocation} {
  good       {openai}
  paste      {openai}
  old        {openai}
  partial    {openai}
  extra      {openai}
  control    {openai}
  c1         {openai}
  invalid    {openai}
  truncated  {openai}
  nul        {openai}
  failed     {openai}
  occupied   {openai 6>caller-fd}
  nonbare    {openai --help}
  redirected {openai > /dev/null}
  input      {openai < /dev/null}
  subshell   {(openai)}
  replaced   {function openai { :; }; __openai_suggestions_run}
} {
  set f [open "$env(SUGGESTION_TEST_DIR)/mode" w]; puts $f $mode; close $f
  send -- "$invocation\r"
  expect -exact "SUGGESTION_TEST> "
  if {$mode eq "good"} {send -- "!"}
  send -- "\030\007"
  expect -re {BUFFER_CAPTURED\r?\n}
}
send -- "exit\r"
expect eof
`)
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		return string(data)
	}
	const draft = `  quit 雪 🧑‍💻 $(touch unintended); echo "$HOME"  !`
	buffers := strings.Split(strings.TrimSuffix(read("draft-buffers"), "\x00"), "\x00")
	require.Len(t, buffers, 17)
	require.Equal(t, draft, buffers[0])
	require.Equal(t, "touch unintended\necho second\n", buffers[1])
	for _, buffer := range buffers[2:] {
		require.Empty(t, buffer)
	}
	require.Equal(t, "good:6:9\npaste:6:9\nold:6:9\npartial:6:9\nextra:6:9\ncontrol:6:9\nc1:6:9\ninvalid:6:9\ntruncated:6:9\nnul:6:9\nfailed:6:9\n"+
		"occupied::9\nnonbare::9\nredirected::9\ninput::9\nsubshell::9\nreplaced::9\n", read("grants"))
	require.Equal(t, "23\n", read("result"))
	require.Equal(t, "caller keeps fd6\n", read("caller-fd"))
	require.NoFileExists(t, filepath.Join(dir, "unintended"))
}

func TestSuggestionNativeZshResumesInterruptedPasteFrames(t *testing.T) {
	probe := `exec 5>&- 6>&-
precmd() {
  local rc=$? state=unchanged
  [[ -f $SUGGESTION_TEST_DIR/check-status ]] || return 0
  (( ${precmd_functions[(I)__openai_arm_help_frame]} )) && state=queued
  print -r -- "$rc $state" >> "$SUGGESTION_TEST_DIR/queued"
  command rm -f -- "$SUGGESTION_TEST_DIR/check-status"
}
original_init() { print -r -- called >> "$SUGGESTION_TEST_DIR/init-calls"; }
reinstall_init() { zle -N zle-line-init original_init; }
autoload -Uz add-zsh-hook
add-zsh-hook precmd reinstall_init
reinstall_init
original_report() { LBUFFER+='user-report'; }
zle -N original_report
bindkey -M main $'\e[1;1R' original_report
capture_buffer() {
  printf '%s\000' "$BUFFER" >>"$SUGGESTION_TEST_DIR/draft-buffers"
  print -r -- "${widgets[zle-line-init]-}" >>"$SUGGESTION_TEST_DIR/after"
  BUFFER=''
  printf '\nBUFFER_CAPTURED\n'
}
cat >"$SUGGESTION_TEST_DIR/openai" <<'CLI'
#!/bin/sh
cat "$SUGGESTION_TEST_DIR/frame" >&6
exit "$(cat "$SUGGESTION_TEST_DIR/exit")"
CLI
`
	dir := runSuggestionShellPTY(t, "zsh", "", probe, false, `
set opener "\033\133200~"
set closer "\033\133201~"
set body {print -r -- accidental > "$SUGGESTION_TEST_DIR/executed"}
append body "\rline two"
foreach {tag status} {
  o1 130 o2 143 o3 130 o4 143 o5 130
  p0 143 p1 130 p2 143 p3 130 p4 143 p5 130
  o2r 143 p0r 143 p3r 130 p5r 143
  r 143 r 130 r 0 successr 0
  p0 0 p1 1 o0 130 p6 143 extra 130 short 143 text 143 successr 143 invalidr 0 trailingr 0
} {
  set f [open "$env(SUGGESTION_TEST_DIR)/frame" w]; fconfigure $f -translation binary
  switch $tag {
    successr {puts -nonewline $f "git status\000r\000"}
    invalidr {puts -nonewline $f "bad\033text\000r\000"}
    trailingr {puts -nonewline $f "bad\000r\000x"}
    extra {puts -nonewline $f "\000p0\000x"}
    short {puts -nonewline $f "\000p0"}
    text {puts -nonewline $f "print unsafe\r\000"}
    default {puts -nonewline $f "\000$tag\000"}
  }
  close $f
  set f [open "$env(SUGGESTION_TEST_DIR)/exit" w]; puts $f $status; close $f
  set f [open "$env(SUGGESTION_TEST_DIR)/check-status" w]; close $f
  send -- "openai\r"
  expect -exact "SUGGESTION_TEST> "
  if {[regexp {^o([1-5])r?$} $tag _ n] && $status in {130 143}} {
    send -- "[string range $opener $n end]$body$closer"
  } elseif {[regexp {^p([0-5])r?$} $tag _ n] && $status in {130 143}} {
    send -- "[string range $closer $n end]next"
  } elseif {$tag eq "r" || ($tag eq "successr" && $status == 0)} {
    send -- "-edit"
  } else {
    send -- "unchanged"
  }
  if {$tag in {o2r p0r p3r p5r r} || ($tag eq "successr" && $status == 0)} {send -- "user;1R\033\1331;1Rtail"}
  send -- "\030\007"
  expect -re {BUFFER_CAPTURED\r?\n}
}
send -- {bindkey -l > "$SUGGESTION_TEST_DIR/maps"; bindkey -M main $'\e[1;1R' > "$SUGGESTION_TEST_DIR/binding"}
send -- "\r"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		return string(data)
	}
	const pasted = "print -r -- accidental > \"$SUGGESTION_TEST_DIR/executed\"\nline two\x00"
	require.Equal(t, strings.Repeat(pasted, 5)+strings.Repeat("next\x00", 6)+
		strings.TrimSuffix(pasted, "\x00")+"user;1Rtail\x00"+strings.Repeat("nextuser;1Rtail\x00", 3)+
		strings.Repeat("-edituser;1Rtail\x00", 3)+"git status-edituser;1Rtail\x00"+
		strings.Repeat("unchanged\x00", 10), read("draft-buffers"))
	require.Equal(t, strings.Repeat("user:original_init\n", 29), read("after"))
	require.Len(t, strings.Fields(read("init-calls")), 31)
	queued := strings.Split(strings.TrimSpace(read("queued")), "\n")
	require.Len(t, queued, 29)
	for i, status := range []int{130, 143, 130, 143, 130, 143, 130, 143, 130, 143, 130, 143, 143, 130, 143, 143, 130, 0, 0, 0, 1, 130, 143, 130, 143, 143, 143, 0, 0} {
		widget := "unchanged"
		if i < 19 {
			widget = "queued"
		}
		require.Equal(t, strconv.Itoa(status)+" "+widget, queued[i])
	}
	require.NotContains(t, read("maps"), "__openai_help_reply_map")
	require.Contains(t, read("binding"), "original_report")
	require.NoFileExists(t, filepath.Join(dir, "executed"))
}
