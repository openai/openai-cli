package autocomplete

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests drive a real zsh editor. Their FIFO writer replaces only the
// terminal adapter; it never writes command text into the terminal input stream.
func runSuggestionClickPTY(t *testing.T, extra, interaction string) string {
	t.Helper()
	script, err := autoCompleteFS.ReadFile("shellscripts/zsh_command_insertion_click.zsh")
	require.NoError(t, err)
	probe := strings.ReplaceAll(string(script), "__APPNAME__", "openai") + `
print -r -- "$__openai_suggestions_click_directory" >"$SUGGESTION_TEST_DIR/endpoint"
bindkey '^X^R' openai_suggestion_restore
click_test_clear() { BUFFER=''; CURSOR=0; }
zle -N click_test_clear
bindkey '^Xq' click_test_clear
` + extra
	setup := suggestionClickTerminalSetup(t)
	return runSuggestionShellPTY(t, "zsh", setup, probe, false, `
expect_after {
  eof {puts stderr "unexpected shell exit: [wait]"; exit 1}
  timeout {puts stderr "click insertion probe timed out"; exit 1}
}
set endpoint_file [open "$env(SUGGESTION_TEST_DIR)/endpoint" r]
set endpoint [string trim [read $endpoint_file]]
close $endpoint_file
if {$endpoint eq ""} {error "click endpoint missing"}
proc ready {} {
  global endpoint
  for {set attempt 0} {$attempt < 100 && ![file exists "$endpoint/ready"]} {incr attempt} {after 20}
  set file [open "$endpoint/ready" r]
  set value [string trim [read $file]]
  close $file
  return $value
}
proc request {record} {
  global endpoint
  set file [open "$endpoint/requests" w]
  fconfigure $file -translation binary -buffering none
  puts -nonewline $file $record
  close $file
}
proc click {row} {
  set state [split [ready] " "]
  request "[lindex $state 0] [lindex $state 1] $row\n"
}
`+interaction)
}

func suggestionClickTerminalSetup(t *testing.T) string {
	t.Helper()
	terminfo := t.TempDir()
	source := filepath.Join(terminfo, "fixture.terminfo")
	require.NoError(t, os.WriteFile(source, []byte("xterm-kitty|Synthetic Kitty capability fixture,\n\tuse=xterm-256color,\n"), 0o600))
	tic, err := exec.LookPath("tic")
	if err != nil {
		if strings.Contains(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), "zsh") {
			t.Fatal("tic is required for native click tests")
		}
		t.Skip("tic is required for native click tests")
	}
	output, err := exec.Command(tic, "-x", "-o", terminfo, source).CombinedOutput()
	require.NoError(t, err, string(output))
	t.Setenv("TERMINFO", terminfo)
	return "export TERMINFO='" + strings.ReplaceAll(terminfo, "'", "'\\''") + "'\nKITTY_WINDOW_ID=73\nKITTY_PID=7173\nTERM=xterm-kitty"
}

func TestSuggestionClickNativeInsertionRestoreAndSeparateEnter(t *testing.T) {
	dir := runSuggestionClickPTY(t, "", `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "draft tail\002\002\002\002"
after 50
click 2
expect -exact "Command 2/3. Enter runs the command."
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
click 2
after 50
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030\022\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
click 1
expect -exact "Command 1/3. Enter runs the command."
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\r"
expect -exact "EXECUTED_SELECTED_COMMAND\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
	buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
	require.NoError(t, err)
	require.Equal(t, "<openai files list>|17\n<openai files list>|17\n<draft tail>|6\n<openai models list>|18\n", string(buffers))
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	require.Equal(t, "<examples>\n<models>\n<list>\n", string(calls))
	assertSuggestionClickCleanup(t, dir)
}

func TestSuggestionClickNativeRejectsMalformedAndStaleRequests(t *testing.T) {
	dir := runSuggestionClickPTY(t, "", `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set state [split [ready] " "]
set epoch [lindex $state 0]
set generation [lindex $state 1]
send -- "draft"
after 50
foreach bad [list "wrong $generation 1\n" "$epoch wrong 1\n" "$epoch $generation 0\n" "$epoch $generation 129\n" "$epoch $generation 2 extra\n" "$epoch $generation 01\n" "$epoch $generation \[1\]\n"] {request $bad; after 30}
request "$epoch $generation "
send -- "x\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
request "9\n"
after 50
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025openai wait\r"
expect -exact "WAIT_READY\r\n"
if {[file exists "$endpoint/ready"]} {error "busy shell published readiness"}
request "$epoch $generation 1\n"
send -- "keyboard-only\r"
expect -exact "SUGGESTION_TEST> "
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
if {[file exists "$endpoint/ready"]} {error "silent command retained stale clicks"}
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
request "$epoch $generation 1\n"
after 50
send -- "safe\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025exit\r"
expect eof
`)
	buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
	require.NoError(t, err)
	require.Equal(t, "<draftx>|6\n<draftx>|6\n<>|0\n<safe>|4\n", string(buffers))
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	require.Equal(t, "<examples>\n<wait>\n<examples>\n", string(calls))
	assertSuggestionClickCleanup(t, dir)
}

func TestSuggestionClickNativeUndoUnicodeAndMultilineDraft(t *testing.T) {
	dir := runSuggestionClickPTY(t, `
LC_ALL=en_US.UTF-8
click_test_draft() { BUFFER=$'雪 draft\nnext\n'; CURSOR=3; print DRAFT_SET; }
zle -N click_test_draft
bindkey '^Xx' click_test_draft
bindkey '^_' undo
`, `
send -- "openai literal; print CLICK_LITERAL_FINISHED\r"
expect -exact "\r\nCLICK_LITERAL_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set state [ready]
send -- "\030x"
expect -re {DRAFT_SET\r?\n}
after 50
click 1
expect -exact "Command 1/1. Enter runs the command."
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\037\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
click 1
expect -exact "Command 1/1. Enter runs the command."
send -- "\030\022\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030qexit\r"
expect eof
`)
	buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
	require.NoError(t, err)
	literal := "openai responses create --input '雪 $HOME $(touch unsafe); end'"
	require.Equal(t, "<"+literal+">|62\n<雪 draft\nnext\n>|3\n<雪 draft\nnext\n>|3\n", string(buffers))
	_, err = os.Stat(filepath.Join(dir, "unsafe"))
	require.True(t, os.IsNotExist(err))
	assertSuggestionClickCleanup(t, dir)
}

func TestSuggestionClickNativeRejectsOtherEditorContexts(t *testing.T) {
	for _, context := range []string{"recursive", "vared"} {
		t.Run(context, func(t *testing.T) {
			extra := `
click_test_context() { print CONTEXT_START; zle recursive-edit; print CONTEXT_DONE; }
zle -N click_test_context
bindkey '^Xe' click_test_context
`
			if context == "vared" {
				extra = ""
			}
			enterContext := `send -- "\030e"
expect -re {CONTEXT_START\r?\n}`
			leaveContext := `send -- "\r"
expect -re {CONTEXT_DONE\r?\n}`
			if context == "vared" {
				enterContext = `send -- "value='inside vared'; vared value; print VARED_FINISHED\r"
expect -exact "inside vared"`
				leaveContext = `send -- "\r"
expect -exact "\r\nVARED_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "`
			}
			dir := runSuggestionClickPTY(t, extra, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set state [split [ready] " "]
`+enterContext+`
after 100
request "[lindex $state 0] [lindex $state 1] 1\n"
after 50
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
`+leaveContext+`
send -- "exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			if context == "recursive" {
				require.Equal(t, "<>|0\n", string(buffers))
			} else {
				require.Equal(t, "<inside vared>|12\n", string(buffers))
			}
			assertSuggestionClickCleanup(t, dir)
		})
	}
}

func TestSuggestionClickNativePipelineDirectoryAndDisable(t *testing.T) {
	dir := runSuggestionClickPTY(t, `
click_test_chdir() { cd /; }
zle -N click_test_chdir
bindkey '^X^D' click_test_chdir
`, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set state [split [ready] " "]
send -- "openai examples | cat\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
after 100
if {[file exists "$endpoint/ready"]} {error "pipeline retained readiness"}
request "[lindex $state 0] [lindex $state 1] 1\n"
after 50
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set state [split [ready] " "]
send -- "draft\030\004"
after 100
if {[file exists "$endpoint/ready"]} {error "directory change retained readiness"}
request "[lindex $state 0] [lindex $state 1] 1\n"
after 50
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025openai_command_insertion_disable\r"
expect -exact "SUGGESTION_TEST> "
after 100
if {[file exists $endpoint]} {error "disable retained click endpoint"}
send -- "exit\r"
expect eof
`)
	buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
	require.NoError(t, err)
	require.Equal(t, "<>|0\n<draft>|5\n", string(buffers))
	assertSuggestionClickCleanup(t, dir)
}

func TestSuggestionClickNativeUnsupportedEnvironmentKeepsKeyboard(t *testing.T) {
	for name, environment := range map[string]string{
		"missing-window": "unset KITTY_WINDOW_ID", "missing-process": "unset KITTY_PID",
		"zero-window": "KITTY_WINDOW_ID=0", "zero-process": "KITTY_PID=0",
		"leading-zero-window": "KITTY_WINDOW_ID=073", "leading-zero-process": "KITTY_PID=07173",
		"other-terminal": "TERM=xterm-256color", "ssh": "SSH_CONNECTION=synthetic",
		"ssh-tty": "SSH_TTY=synthetic", "tmux": "TMUX=synthetic", "screen": "STY=synthetic",
	} {
		t.Run(name, func(t *testing.T) {
			script, err := autoCompleteFS.ReadFile("shellscripts/zsh_command_insertion_click.zsh")
			require.NoError(t, err)
			probe := strings.ReplaceAll(string(script), "__APPNAME__", "openai") + `
print -r -- "${__openai_suggestions_click_directory-}" >"$SUGGESTION_TEST_DIR/endpoint"
`
			dir := runSuggestionShellPTY(t, "zsh", suggestionClickTerminalSetup(t)+"\n"+environment, probe, false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025exit\r"
expect eof
`)
			endpoint, err := os.ReadFile(filepath.Join(dir, "endpoint"))
			require.NoError(t, err)
			require.Equal(t, "\n", string(endpoint))
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai models list>|18\n", string(buffers))
		})
	}
}

func TestSuggestionClickNativeRecoveryAndSubshellCleanup(t *testing.T) {
	dir := runSuggestionClickPTY(t, "", `
send -- "openai recovery >/dev/null; print RECOVERY_STATUS:\$?\r"
expect -exact "RECOVERY_ERROR\r\n"
expect -exact "RECOVERY_STATUS:17\r\n"
expect -exact "SUGGESTION_TEST> "
click 1
expect -exact "Command 1/1. Enter runs the command."
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025(openai_command_insertion_disable); print SUBSHELL_FINISHED\r"
expect -exact "SUBSHELL_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
if {![file isdirectory $endpoint]} {error "subshell removed parent endpoint"}
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
click 3
expect -exact "Command 3/3. Enter runs the command."
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025source \"\$SUGGESTION_TEST_DIR/hook\"; print RECONFIGURED\r"
expect -exact "RECONFIGURED\r\n"
expect -exact "SUGGESTION_TEST> "
if {[file exists $endpoint]} {error "keyboard-only reconfiguration retained endpoint"}
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025exit\r"
expect eof
`)
	buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
	require.NoError(t, err)
	require.Equal(t, "<openai models list>|18\n<openai responses create --model demo --input 'Hi'>|49\n<openai models list>|18\n", string(buffers))
	assertSuggestionClickCleanup(t, dir)
}

func TestSuggestionClickNativeQueuedKeysAndDescriptorFailure(t *testing.T) {
	dir := runSuggestionClickPTY(t, `
click_test_queued() {
  print -r -u "$__openai_suggestions_click_fd" -- "$__openai_suggestions_click_epoch $__openai_suggestions_click_generation 1"
  zle -U x
  __openai_suggestions_click_request
}
click_test_failure() {
  exec {__openai_suggestions_click_fd}>&-
  __openai_suggestions_click_request
}
zle -N click_test_queued
zle -N click_test_failure
bindkey '^Xk' click_test_queued
bindkey '^Xf' click_test_failure
`, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set state [ready]
send -- "draft\030k"
after 50
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030f"
after 50
if {[file exists $endpoint]} {error "failed descriptor retained endpoint"}
send -- "\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030qexit\r"
expect eof
`)
	buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
	require.NoError(t, err)
	require.Equal(t, "<draftx>|6\n<openai models list>|18\n", string(buffers))
	assertSuggestionClickCleanup(t, dir)
}

func TestSuggestionClickNativeChangedTerminalIdentity(t *testing.T) {
	for _, change := range []string{"KITTY_PID=7174", "KITTY_WINDOW_ID=74"} {
		t.Run(change, func(t *testing.T) {
			dir := runSuggestionClickPTY(t, "click_test_identity() { "+change+`; }
zle -N click_test_identity
bindkey '^Xo' click_test_identity
`, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set state [split [ready] " "]
send -- "draft\030o"
after 50
request "[lindex $state 0] [lindex $state 1] 1\n"
after 50
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030qexit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<draft>|5\n", string(buffers))
			assertSuggestionClickCleanup(t, dir)
		})
	}
}

func assertSuggestionClickCleanup(t *testing.T, dir string) {
	t.Helper()
	endpoint, err := os.ReadFile(filepath.Join(dir, "endpoint"))
	require.NoError(t, err)
	_, err = os.Stat(strings.TrimSpace(string(endpoint)))
	require.True(t, os.IsNotExist(err), "normal shell exit must remove its private click endpoint")
}
