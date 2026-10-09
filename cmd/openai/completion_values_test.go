package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainCompletionValuesProtocols(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			name   string
			args   []string
			values []string
		}{
			{"all formats", []string{"--format", ""}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"format prefix", []string{"--format", "j"}, []string{"json", "jsonl"}},
			{"error format prefix", []string{"--format-error", "j"}, []string{"json", "jsonl"}},
			{"assigned format", []string{"--format=j"}, []string{"json", "jsonl"}},
			{"empty assigned format", []string{"--format="}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"assigned error format", []string{"--format-error=r"}, []string{"raw"}},
			{"nested root format", []string{"responses", "create", "--format", "j"}, []string{"json", "jsonl"}},
			{"group root format", []string{"responses", "--format", "j"}, []string{"json", "jsonl"}},
			{"earlier root flags", []string{"--format", "json", "--organization=org-synthetic", "files", "upload", "--purpose", "b"}, []string{"batch"}},
			{"interspersed root flag", []string{"files", "--format", "json", "upload", "--purpose", "v"}, []string{"vision"}},
			{"command alias", []string{"audio:transcriptions", "create", "--format", "j"}, []string{"json", "jsonl"}},
			{"shortcut", []string{"transcribe", "--format-error", "t"}, []string{"text"}},
			{"codex formats", []string{"codex", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer editor formats", []string{"tokenizer", "--format", ""}, []string{"auto", "text"}},
			{"tokenizer count formats", []string{"tokenizer", "count", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer inspect formats", []string{"tokenizer", "inspect", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer encodings formats", []string{"tokenizer", "encodings", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer licenses formats", []string{"tokenizer", "licenses", "--format", ""}, []string{"auto", "text", "json"}},
			{"image preview formats", []string{"images", "preview", "--format", ""}, []string{"auto", "text"}},
			{"image inline on formats", []string{"images", "inline", "on", "--format", ""}, []string{"auto", "text"}},
			{"image inline off formats", []string{"images", "inline", "off", "--format", ""}, []string{"auto", "text"}},
			{"image preview error formats", []string{"images", "preview", "--format-error=j"}, []string{"json", "jsonl"}},
			{"codex assigned format", []string{"codex", "--format=j"}, []string{"json"}},
			{"tokenizer assigned format", []string{"tokenizer", "count", "--format=j"}, []string{"json"}},
			{"codex error formats", []string{"codex", "--format-error", ""}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"tokenizer editor error formats", []string{"tokenizer", "--format-error", ""}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"tokenizer child error format", []string{"tokenizer", "count", "--format-error=j"}, []string{"json", "jsonl"}},
			{"upload purposes", []string{"files", "upload", "--purpose", ""}, []string{"assistants", "batch", "evals", "fine-tune", "user_data", "vision"}},
			{"legacy create purposes", []string{"files", "create", "--purpose", ""}, []string{"assistants", "batch", "evals", "fine-tune", "user_data", "vision"}},
			{"assigned purpose", []string{"files", "upload", "--purpose=u"}, []string{"user_data"}},
			{"empty assigned purpose", []string{"files", "upload", "--purpose="}, []string{"assistants", "batch", "evals", "fine-tune", "user_data", "vision"}},
			{"list purposes", []string{"files", "list", "--purpose", ""}, []string{"assistants", "assistants_output", "batch", "batch_output", "evals", "fine-tune", "fine-tune-results", "user_data", "vision"}},
			{"list output purpose", []string{"files", "list", "--purpose=batch_"}, []string{"batch_output"}},
			// This case models a shell-decoded preceding filename.
			// Current-token quotes have separate coverage below.
			{"quoted filename precedes value", []string{"files", "upload", "upload space.txt", "--purpose", "u"}, []string{"user_data"}},
		} {
			t.Run(style+"/"+tc.name, func(t *testing.T) {
				prefix := ""
				if name, _, assigned := strings.Cut(tc.args[len(tc.args)-1], "="); assigned && style != "bash" {
					prefix = name + "="
				}
				want := ""
				for _, value := range tc.values {
					want += prefix + value + "\n"
				}
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != (mainDispatchResult{stdout: want}) {
					t.Fatalf("completion got %+v; want stdout %q and status 0", got, want)
				}
			})
		}
	}
}

func TestMainCompletionValuesPreserveOtherInputs(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			args []string
			want mainDispatchResult
		}{
			{[]string{"--format", "unknown"}, mainDispatchResult{code: 11}},
			{[]string{"--format=unknown"}, mainDispatchResult{code: 11}},
			{[]string{"codex", "--format=jsonl"}, mainDispatchResult{code: 11}},
			{[]string{"tokenizer", "--format", "j"}, mainDispatchResult{code: 11}},
			{[]string{"tokenizer", "inspect", "--format", "y"}, mainDispatchResult{code: 11}},
			{[]string{"images", "preview", "--format=j"}, mainDispatchResult{code: 11}},
			{[]string{"images", "inline", "on", "--format", "j"}, mainDispatchResult{code: 11}},
			{[]string{"images", "inline", "off", "--format", "y"}, mainDispatchResult{code: 11}},
			{[]string{"files", "upload", "--purpose", "batch_output"}, mainDispatchResult{code: 11}},
			{[]string{"files", "create", "--purpose=fine-tune-results"}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", "--model", "j"}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", "--model=j"}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", "--", "--format=j"}, mainDispatchResult{}},
			{[]string{"--", "--format=j"}, mainDispatchResult{}},
			{[]string{"files", "upload", "--purpose", "batch", "--file", "j"}, mainDispatchResult{code: 10}},
			{[]string{"files", "upload", "--purpose", "batch", "--file=j"}, mainDispatchResult{code: 10, stdout: "--file=\n"}},
			{[]string{"files", "upload", "--purpose", "batch", "j"}, mainDispatchResult{code: 10}},
			{[]string{"files", "upload", "--purpose", "batch", "--", "--purpose=j"}, mainDispatchResult{code: 10}},
		} {
			t.Run(style+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1", "OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != tc.want {
					t.Fatalf("completion got %+v; want %+v", got, tc.want)
				}
			})
		}
	}
}

func TestMainCompletionValuesAdapterCompatibility(t *testing.T) {
	for _, marker := range []struct {
		name string
		env  []string
	}{
		{"missing", nil},
		{"empty", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES="}},
		{"zero", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=0"}},
		{"true", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=true"}},
		{"two", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=2"}},
	} {
		for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
			for _, tc := range []struct {
				args []string
				want string
			}{
				{[]string{"--format", "j"}, "json\njsonl\n"},
				{[]string{"--format=j"}, "--format=json\n--format=jsonl\n"},
				{[]string{"--format-error=y"}, "--format-error=yaml\n"},
				{[]string{"files", "upload", "--purpose=u"}, "--purpose=user_data\n"},
			} {
				t.Run(marker.name+"/"+style+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
					want := mainDispatchResult{stdout: tc.want}
					if style == "bash" || style == "zsh" {
						want = mainDispatchResult{code: 11}
					}
					got := runMainDispatchWithEnv(t, style, marker.env, mainCompletionArgs(style, tc.args...)...)
					if got != want {
						t.Fatalf("adapter marker changed completion: got %+v; want %+v", got, want)
					}
				})
			}
			t.Run(marker.name+"/"+style+"/command", func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, marker.env, mainCompletionArgs(style, "models", "li")...)
				name, _, _ := strings.Cut(got.stdout, ":")
				name, _, _ = strings.Cut(name, "\t")
				if got.code != 0 || got.stderr != "" || strings.TrimSuffix(name, "\n") != "list" {
					t.Fatalf("static marker changed command completion: %+v", got)
				}
			})
			t.Run(marker.name+"/"+style+"/file", func(t *testing.T) {
				env := append([]string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1"}, marker.env...)
				got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "files", "upload", "--file", "fixture")...)
				if got != (mainDispatchResult{code: 10}) {
					t.Fatalf("static marker changed file completion: %+v", got)
				}
			})
		}
	}
}

func TestMainCompletionValuesDirectoryCollisions(t *testing.T) {
	for _, tc := range []struct {
		name, directory, replacementPrefix string
		args                               []string
		values                             string
		suppress                           bool
	}{
		{"separated", "yaml", "", []string{"--format", "y"}, "yaml\n", true},
		{"assigned", "yaml", "", []string{"--format=y"}, "yaml\n", true},
		{"whole set", "json", "", []string{"--format", "j"}, "json\njsonl\n", true},
		{"restored assignment", "--format=yaml", "--format=", []string{"--format=y"}, "yaml\n", true},
		{"unrestored assignment", "--format=yaml", "", []string{"--format=y"}, "yaml\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.Mkdir(tc.directory, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
				t.Run(style, func(t *testing.T) {
					env := []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1", "OPENAI_CLI_COMPLETION_BASH_VALUE_PREFIX=" + tc.replacementPrefix}
					want := mainDispatchResult{stdout: tc.values}
					if style == "bash" && tc.suppress {
						want = mainDispatchResult{code: 11}
					} else if style != "bash" && strings.Contains(tc.args[len(tc.args)-1], "=") {
						want.stdout = "--format=" + strings.ReplaceAll(strings.TrimSuffix(tc.values, "\n"), "\n", "\n--format=") + "\n"
					}
					got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, tc.args...)...)
					if got != want {
						t.Fatalf("directory collision got %+v; want %+v", got, want)
					}
				})
			}
		})
	}
	t.Run("replacement prefix does not bypass old adapter gate", func(t *testing.T) {
		t.Chdir(t.TempDir())
		for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
			want := mainDispatchResult{stdout: "--format=text\n"}
			if style == "bash" || style == "zsh" {
				want = mainDispatchResult{code: 11}
			}
			got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_BASH_VALUE_PREFIX=--format="}, mainCompletionArgs(style, "--format=t")...)
			if got != want {
				t.Fatalf("%s replacement prefix bypassed adapter gate: got %+v; want %+v", style, got, want)
			}
		}
	})
}

func TestMainCompletionValuesDirectoryScope(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			name  string
			args  []string
			names []string
			code  int
		}{
			{"flags", []string{"--forma"}, []string{"--format", "--format-error"}, 0},
			{"commands", []string{"models", "li"}, []string{"list"}, 0},
			{"flags after flag-like data", []string{"--organization", "--format", "--forma"}, []string{"--format", "--format-error"}, 0},
			{"commands after flag-like data", []string{"--organization", "--format", "models", "li"}, []string{"list"}, 0},
			{"files", []string{"files", "upload", "--file", "y"}, nil, 10},
		} {
			t.Run(style+"/"+tc.name, func(t *testing.T) {
				t.Chdir(t.TempDir())
				env := []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1", "OPENAI_CLI_COMPLETION_FILE_VALUES=1"}
				args := mainCompletionArgs(style, tc.args...)
				before := runMainDispatchWithEnv(t, style, env, args...)
				if before.code != tc.code || before.stderr != "" {
					t.Fatalf("completion control failed: %+v", before)
				}
				names := make(map[string]bool)
				if before.stdout != "" {
					for _, line := range strings.Split(strings.TrimSuffix(before.stdout, "\n"), "\n") {
						name, _, _ := strings.Cut(line, ":")
						name, _, _ = strings.Cut(name, "\t")
						names[name] = true
					}
				}
				if len(names) != len(tc.names) {
					t.Fatalf("unexpected control candidates: %+v", before)
				}
				for _, name := range tc.names {
					if !names[name] {
						t.Fatalf("control lacks candidate %q: %+v", name, before)
					}
				}
				for _, directory := range []string{"yaml", "json", "list", "--format"} {
					if err := os.Mkdir(directory, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if got := runMainDispatchWithEnv(t, style, env, args...); got != before {
					t.Fatalf("directories changed non-static completion: got %+v; want %+v", got, before)
				}
			})
		}
	}
}

func TestMainCompletionValuesCallerDirectory(t *testing.T) {
	for _, replacementPrefix := range []string{"", "--format="} {
		for _, collisionLocation := range []string{"caller", "backend"} {
			t.Run(collisionLocation+"/"+replacementPrefix, func(t *testing.T) {
				root := t.TempDir()
				caller, backend := filepath.Join(root, "caller"), filepath.Join(root, "backend")
				for _, directory := range []string{caller, backend} {
					if err := os.Mkdir(directory, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(filepath.Join(root, collisionLocation, replacementPrefix+"yaml"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Chdir(backend)
				env := []string{
					"OPENAI_CLI_COMPLETION_STATIC_VALUES=1",
					"OPENAI_CLI_COMPLETION_BASH_VALUE_PREFIX=" + replacementPrefix,
					"OPENAI_CLI_COMPLETION_BASH_CWD=" + caller,
				}
				want := mainDispatchResult{stdout: "yaml\n"}
				if collisionLocation == "caller" {
					want = mainDispatchResult{code: 11}
				}
				got := runMainDispatchWithEnv(t, "bash", env, mainCompletionArgs("bash", "--format=y")...)
				if got != want {
					t.Fatalf("completion used the wrong directory: got %+v; want %+v", got, want)
				}
			})
		}
	}
}

func TestMainCompletionValuesBashCallerDirectoryCapture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("This Bash callback test requires Unix executable wrappers and newline directory names.")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adapter := runMainDispatch(t, "bash", "openai", "@completion", "bash")
	if adapter.code != 0 || adapter.stderr != "" || adapter.stdout == "" {
		t.Fatalf("adapter generation failed: %+v", adapter)
	}
	for _, tc := range []struct {
		name, collision, candidates string
		failedPwd, command          bool
	}{
		{"caller-only newline", "caller", "", false, false},
		{"backend-only newline", "backend", "yaml\n", false, false},
		{"failed pwd static", "", "", true, false},
		{"failed pwd command", "", "list\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			caller, backend, bindir := filepath.Join(root, "caller\n"), filepath.Join(root, "backend"), filepath.Join(root, "bin")
			for _, directory := range []string{caller, backend, bindir} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.collision != "" {
				directory := backend
				if tc.collision == "caller" {
					directory = caller
				}
				if err := os.Mkdir(filepath.Join(directory, "yaml"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			adapterPath := filepath.Join(root, "adapter.bash")
			if err := os.WriteFile(adapterPath, []byte(adapter.stdout), 0o600); err != nil {
				t.Fatal(err)
			}
			const wrapper = `#!/bin/sh
printf '%s' "$OPENAI_CLI_COMPLETION_BASH_CWD" > "$COMPLETION_TEST_CWD"
printf '%s' "$OPENAI_CLI_COMPLETION_STATIC_VALUES" > "$COMPLETION_TEST_MARKER"
cd "$COMPLETION_TEST_BACKEND" || exit 91
exec "$COMPLETION_TEST_BINARY" -test.run='^TestMainDispatchProcess$' -- openai "$@"
`
			if err := os.WriteFile(filepath.Join(bindir, "openai"), []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			const probe = `
source "$COMPLETION_TEST_ADAPTER" || exit 92
if [[ "$COMPLETION_TEST_FAILED_PWD" == 1 ]]; then pwd() { return 1; }; fi
if [[ "$COMPLETION_TEST_COMMAND" == 1 ]]; then
  COMP_LINE='openai models li'
  COMP_WORDS=(openai models li)
else
  COMP_LINE='openai --format y'
  COMP_WORDS=(openai --format y)
fi
COMP_CWORD=2
COMP_POINT=${#COMP_LINE}
__openai_bash_autocomplete openai "${COMP_WORDS[2]}" "${COMP_WORDS[1]}"
for candidate in "${COMPREPLY[@]}"; do printf '%s\n' "$candidate"; done
`
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", probe)
			process.Dir = caller
			process.WaitDelay = 2 * time.Second
			process.Env = []string{
				"PATH=" + bindir + ":/usr/bin:/bin", "HOME=" + root, "LC_ALL=C", "GOMAXPROCS=2",
				"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_BASE_URL=invalid-completion-url",
				"COMPLETION_TEST_BINARY=" + binary, "COMPLETION_TEST_BACKEND=" + backend,
				"COMPLETION_TEST_ADAPTER=" + adapterPath,
				"COMPLETION_TEST_CWD=" + filepath.Join(root, "cwd"),
				"COMPLETION_TEST_MARKER=" + filepath.Join(root, "marker"),
			}
			if tc.failedPwd {
				process.Env = append(process.Env, "COMPLETION_TEST_FAILED_PWD=1")
			}
			if tc.command {
				process.Env = append(process.Env, "COMPLETION_TEST_COMMAND=1")
			}
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &stderr
			if err := process.Run(); err != nil || ctx.Err() != nil || stderr.Len() != 0 || stdout.String() != tc.candidates {
				t.Fatalf("Bash callback failed: error=%v context=%v stdout=%q stderr=%q", err, ctx.Err(), stdout.String(), stderr.String())
			}
			wantDirectory, err := filepath.EvalSymlinks(caller)
			if err != nil {
				t.Fatal(err)
			}
			wantMarker := "1"
			if tc.failedPwd {
				wantDirectory, wantMarker = "", "0"
			}
			for name, want := range map[string]string{"cwd": wantDirectory, "marker": wantMarker} {
				got, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(got) != want {
					t.Fatalf("callback %s bytes changed: got %q error=%v; want %q", name, got, err, want)
				}
			}
		})
	}
}

func TestMainCompletionValuesQuotedArguments(t *testing.T) {
	for _, style := range []string{"zsh", "fish"} {
		for _, quote := range []string{"'", `"`} {
			for _, closing := range []string{"", quote} {
				for _, tc := range []struct {
					path        []string
					flag, value string
					completion  string
				}{
					{nil, "--format", "j", "json\njsonl\n"},
					{nil, "--format", "", "auto\ntext\nexplore\njson\njsonl\npretty\nraw\nyaml\n"},
					{[]string{"files", "upload"}, "--purpose", "u", "user_data\n"},
					{[]string{"files", "upload"}, "--purpose", "", "assistants\nbatch\nevals\nfine-tune\nuser_data\nvision\n"},
				} {
					for _, form := range []string{"separated", "assigned", "whole assignment"} {
						args := append([]string(nil), tc.path...)
						want := tc.completion
						switch form {
						case "separated":
							args = append(args, tc.flag, quote+tc.value+closing)
						case "assigned":
							args = append(args, tc.flag+"="+quote+tc.value+closing)
						case "whole assignment":
							args = append(args, quote+tc.flag+"="+tc.value+closing)
						}
						if form != "separated" {
							want = tc.flag + "=" + strings.ReplaceAll(strings.TrimSuffix(want, "\n"), "\n", "\n"+tc.flag+"=") + "\n"
						}
						t.Run(style+"/"+form+"/"+strings.Join(args, " "), func(t *testing.T) {
							got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, args...)...)
							if got != (mainDispatchResult{stdout: want}) {
								t.Fatalf("quoted completion got %+v; want stdout %q and status 0", got, want)
							}
						})
					}
				}
			}
		}
	}
}

func TestMainCompletionValuesQuotesPreserveOtherInputs(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			args []string
			want mainDispatchResult
		}{
			{[]string{"responses", "create", "--model", `'j'`}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", `--model="j"`}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", `'--model=j'`}, mainDispatchResult{}},
			{[]string{"files", "upload", "--file", `"fixture"`}, mainDispatchResult{code: 10}},
			{[]string{"files", "upload", `--file='fixture'`}, mainDispatchResult{code: 10, stdout: "--file=\n"}},
			{[]string{`'--mtls-client-cert-file=fixture'`}, mainDispatchResult{}},
			{[]string{"responses", "create", "--", `'--format=j'`}, mainDispatchResult{}},
		} {
			t.Run(style+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1", "OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != tc.want {
					t.Fatalf("quoted control got %+v; want %+v", got, tc.want)
				}
			})
		}
	}
	// Bash and PowerShell dispatch decoded values. Remaining quotes are literal data.
	for _, style := range []string{"bash", "pwsh"} {
		for _, tc := range []struct {
			args []string
			code int
		}{
			{[]string{"--format", `'j`}, 11},
			{[]string{"--format", `"j"`}, 11},
			{[]string{`--format='j'`}, 11},
			{[]string{`'--format=j'`}, 0},
		} {
			t.Run(style+" literal/"+strings.Join(tc.args, " "), func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != (mainDispatchResult{code: tc.code}) {
					t.Fatalf("completion interpreted literal quotes as shell syntax: %+v", got)
				}
			})
		}
	}
}

func TestMainCompletionValuesPreserveLiteralInnerQuotes(t *testing.T) {
	for _, tc := range []struct {
		path        []string
		flag, value string
	}{
		{nil, "--format", "y"},
		{[]string{"files", "list"}, "--purpose", "u"},
	} {
		for _, quotes := range []struct{ outer, inner string }{{"'", `"`}, {`"`, "'"}} {
			value := quotes.inner + tc.value + quotes.inner
			for _, closing := range []string{"", quotes.outer} {
				for _, form := range []string{"separated", "assigned", "whole assignment"} {
					args := append([]string(nil), tc.path...)
					code := 11
					switch form {
					case "separated":
						args = append(args, tc.flag, quotes.outer+value+closing)
					case "assigned":
						args = append(args, tc.flag+"="+quotes.outer+value+closing)
					case "whole assignment":
						args = append(args, quotes.outer+tc.flag+"="+value+closing)
						code = 0
					}
					t.Run("zsh/"+form+"/"+strings.Join(args, " "), func(t *testing.T) {
						got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs("zsh", args...)...)
						if got != (mainDispatchResult{code: code}) {
							t.Fatalf("completion removed literal inner quotes: %+v", got)
						}
					})
				}
			}
			// These adapters remove the outer shell quotes before dispatch.
			for _, style := range []string{"bash", "pwsh"} {
				for _, assigned := range []bool{false, true} {
					args := append([]string(nil), tc.path...)
					if assigned {
						args = append(args, tc.flag+"="+value)
					} else {
						args = append(args, tc.flag, value)
					}
					t.Run(style+"/"+strings.Join(args, " "), func(t *testing.T) {
						got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, args...)...)
						if got != (mainDispatchResult{code: 11}) {
							t.Fatalf("completion removed literal inner quotes: %+v", got)
						}
					})
				}
			}
		}
	}
}

func TestMainCompletionValuesStayLocal(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			name string
			env  []string
		}{
			{"configured server", []string{"OPENAI_API_KEY=sk-fake-completion-test", "OPENAI_BASE_URL=" + server.URL}},
			{"invalid configuration", []string{
				"OPENAI_BASE_URL=invalid-completion-url", "OPENAI_CUSTOM_HEADERS=invalid-completion-headers",
				"OPENAI_MTLS_CLIENT_CERT_FILE=/missing/completion-cert.pem", "OPENAI_MTLS_CLIENT_KEY_FILE=/missing/completion-key.pem",
			}},
		} {
			t.Run(style+"/"+tc.name, func(t *testing.T) {
				env := append([]string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, tc.env...)
				got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "files", "upload", "--purpose", "u")...)
				if got != (mainDispatchResult{stdout: "user_data\n"}) {
					t.Fatalf("completion depends on request configuration: %+v", got)
				}
			})
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("completion made %d requests", got)
	}
}

func TestMainCompletionValuesDoNotRestrictPurpose(t *testing.T) {
	for _, operation := range []string{"list", "upload", "create"} {
		t.Run(operation, func(t *testing.T) {
			const purpose = "synthetic_future_purpose"
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/files" {
					t.Errorf("unexpected request path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if operation == "list" {
					if r.Method != http.MethodGet || r.URL.Query().Get("purpose") != purpose {
						t.Errorf("purpose filter changed: %s %s", r.Method, r.URL)
					}
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
					return
				}
				if r.Method != http.MethodPost {
					t.Errorf("unexpected upload method %q", r.Method)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if got := r.FormValue("purpose"); got != purpose {
					t.Errorf("upload purpose changed: %q", got)
				}
				_, _ = io.WriteString(w, `{"id":"file-synthetic","object":"file","bytes":9,"created_at":1700000000,"filename":"upload.txt","purpose":"synthetic_future_purpose","status":"uploaded"}`)
			}))
			defer server.Close()
			args := []string{"openai", "--format", "json", "files", operation, "--purpose", purpose}
			if operation != "list" {
				path := filepath.Join(t.TempDir(), "upload.txt")
				if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--file", path)
			}
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=sk-fake-completion-test", "OPENAI_BASE_URL=" + server.URL}, args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("completion metadata restricted a request: %+v; requests=%d", got, requests.Load())
			}
		})
	}
}
