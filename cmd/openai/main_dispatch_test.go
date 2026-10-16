package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMainDispatchProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_MAIN_DISPATCH_PROCESS") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing subprocess argument separator")
	}
	os.Args = os.Args[separator+1:]
	main()
	os.Exit(0)
}

type mainDispatchResult struct {
	code           int
	stdout, stderr string
}

// Match the argument shape sent by each bundled shell adapter.
func mainCompletionArgs(style string, args ...string) []string {
	argv := []string{"openai", "__complete"}
	if style == "bash" || style == "fish" {
		argv = append(argv, "--")
	} else if style == "pwsh" {
		argv = append(argv, "openai")
	}
	return append(argv, args...)
}

// Run the production entrypoint in a fresh process: its command tree and flag
// state must not be shared between ordinary invocations and completion probes.
func runMainDispatch(t *testing.T, style string, argv ...string) mainDispatchResult {
	t.Helper()
	return runMainDispatchWithEnv(t, style, nil, argv...)
}

func runMainDispatchWithEnv(t *testing.T, style string, env []string, argv ...string) mainDispatchResult {
	t.Helper()
	return runMainDispatchWithStdin(t, style, env, nil, argv...)
}

// runMainDispatchWithStdin connects stdin to the given file when it is not nil.
func runMainDispatchWithStdin(t *testing.T, style string, env []string, stdin *os.File, argv ...string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := append([]string{"-test.run=^TestMainDispatchProcess$", "--"}, argv...)
	child := exec.CommandContext(ctx, binary, args...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if !strings.HasPrefix(name, "OPENAI_") && name != "COMPLETION_STYLE" {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "COMPLETION_STYLE="+style,
		"OPENAI_BASE_URL=http://127.0.0.1:1")
	child.Env = append(child.Env, env...)
	if stdin != nil {
		child.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	if ctx.Err() != nil {
		t.Fatalf("main timed out: %v; stdout=%q stderr=%q", ctx.Err(), stdout.String(), stderr.String())
	}
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return mainDispatchResult{code, stdout.String(), stderr.String()}
}

func TestMainDispatchOrdinaryArguments(t *testing.T) {
	want := runMainDispatch(t, "bash", "openai", "models", "retrieve", "--model", "ordinary", "--help")
	if want.code != 0 || want.stderr != "" || !strings.Contains(want.stdout, "openai models retrieve") {
		t.Fatalf("ordinary help control failed: %+v", want)
	}
	for _, prefix := range [][]string{nil, {"--debug"}, {"--base-url", "http://127.0.0.1:1"}} {
		for _, value := range []string{"ordinary", "__complete", "before__complete", "__complete_after", ""} {
			argv := append([]string{"openai"}, prefix...)
			argv = append(argv, "models", "retrieve", "--model", value, "--help")
			t.Run(strings.Join(argv, "/"), func(t *testing.T) {
				// Help displays the parsed value as a default, so compare with
				// the equivalent equals form, which never selected completion.
				want := runMainDispatch(t, "bash", "openai", "models", "retrieve", "--model="+value, "--help")
				if want.code != 0 || want.stderr != "" || !strings.Contains(want.stdout, "openai models retrieve") {
					t.Fatalf("equals-form help control failed: %+v", want)
				}
				if got := runMainDispatch(t, "bash", argv...); got != want {
					t.Fatalf("got %+v; want %+v", got, want)
				}
			})
		}
	}
	for _, argv := range [][]string{
		{"__complete", "models", "retrieve", "--model", "ordinary", "--help"},
		{"openai", "--organization", "__complete", "models", "retrieve", "--model", "ordinary", "--help"},
	} {
		t.Run(strings.Join(argv, "/"), func(t *testing.T) {
			if got := runMainDispatch(t, "bash", argv...); got != want {
				t.Fatalf("got %+v; want %+v", got, want)
			}
		})
	}
}

func TestMainDispatchEmptyArguments(t *testing.T) {
	want := runMainDispatch(t, "bash", "openai", "--help")
	if want.code != 0 || want.stderr != "" || !strings.HasPrefix(want.stdout, "NAME:\n   openai\n") {
		t.Fatalf("root help control failed: %+v", want)
	}
	for _, argv := range [][]string{nil, {"openai"}, {""}, {"__complete"}, {"openai", "", "--help"}} {
		if got := runMainDispatch(t, "bash", argv...); got != want {
			t.Fatalf("argv=%q: got %+v; want %+v", argv, got, want)
		}
	}
}

func TestMainDispatchCompletionForms(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(style, func(t *testing.T) {
			modelCompletions := "models\nmoderations\n"
			if style == "zsh" {
				modelCompletions = "models:List and inspect available models.\nmoderations:Classify potentially harmful text and image inputs.\n"
			} else if style == "fish" {
				modelCompletions = "models\tList and inspect available models.\nmoderations\tClassify potentially harmful text and image inputs.\n"
			}
			// Match the bundled scripts' argument shapes. Native shell execution
			// is separate from these production-entrypoint protocol checks.
			prefix := mainCompletionArgs(style)
			for _, tc := range []struct {
				args []string
				want mainDispatchResult
			}{
				{[]string{"mo"}, mainDispatchResult{0, modelCompletions, ""}},
				{[]string{"--format", "__complete"}, mainDispatchResult{11, "", ""}},
				{[]string{"models", "retrieve", "--model", "__complete"}, mainDispatchResult{11, "", ""}},
				{[]string{"--mtls-client-cert-file", "candidate-"}, mainDispatchResult{10, "", ""}},
				{[]string{"--format", "__complete", "mo"}, mainDispatchResult{0, modelCompletions, ""}},
				{[]string{"--format", "two words", "mo"}, mainDispatchResult{0, modelCompletions, ""}},
				{[]string{"--format", "", "mo"}, mainDispatchResult{0, modelCompletions, ""}},
				{[]string{"models", "retrieve", "--model", "@candidate-"}, mainDispatchResult{11, "", ""}},
			} {
				t.Run(strings.Join(tc.args, "/"), func(t *testing.T) {
					argv := append(slices.Clone(prefix), tc.args...)
					if got := runMainDispatch(t, style, argv...); got != tc.want {
						t.Fatalf("got %+v; want %+v", got, tc.want)
					}
				})
			}
		})
	}
}

func TestMainDispatchCompletionEmpty(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		absentArgs := mainCompletionArgs(style)
		if style == "pwsh" {
			// A zero-word direct PowerShell request uses the supported separator form.
			absentArgs = []string{"openai", "__complete", "--"}
		}
		for _, input := range []struct {
			name  string
			words []string
		}{
			{name: "no_words"},
			{name: "empty_word", words: []string{""}},
		} {
			t.Run(style+"/"+input.name, func(t *testing.T) {
				args := mainCompletionArgs(style, input.words...)
				if input.name == "no_words" {
					args = absentArgs
				}
				home := t.TempDir()
				env := []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "XDG_CONFIG_HOME=" + home}
				got := runMainDispatchWithEnv(t, style, env, args...)
				if got.code != 0 || got.stderr != "" || got.stdout == "" {
					t.Fatalf("empty completion failed: %+v", got)
				}
				if input.name == "empty_word" {
					if absent := runMainDispatchWithEnv(t, style, env, absentArgs...); got != absent {
						t.Fatalf("absent and empty completion words differ: %+v versus %+v", absent, got)
					}
				}
				names := make(map[string]bool)
				for _, line := range strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n") {
					name := line
					if style == "zsh" {
						name, _, _ = strings.Cut(line, ":")
					} else if style == "fish" {
						name, _, _ = strings.Cut(line, "\t")
					}
					// A hidden colon alias leaves a backslash before zsh's first separator.
					if name == "" || names[name] || strings.HasPrefix(name, "@") || strings.HasPrefix(name, "__") || strings.ContainsAny(name, ":\\") {
						t.Fatalf("empty completion exposed an invalid or hidden candidate: %q", line)
					}
					names[name] = true
				}
				for _, name := range []string{"help", "images", "models", "responses"} {
					if !names[name] {
						t.Errorf("empty completion omitted public command %q", name)
					}
				}
			})
		}
	}
}
