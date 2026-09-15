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

// Run the production entrypoint in a fresh process: its command tree and flag
// state must not be shared between ordinary invocations and completion probes.
func runMainDispatch(t *testing.T, style string, argv ...string) mainDispatchResult {
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
	if want.code != 0 || want.stderr != "" || !strings.Contains(want.stdout, "CLI for the openai API") {
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
			// Match the bundled scripts' argument shapes. Native shell execution
			// is separate from these production-entrypoint protocol checks.
			prefix := []string{"openai", "__complete"}
			if style == "bash" || style == "fish" {
				prefix = append(prefix, "--")
			} else if style == "pwsh" {
				prefix = append(prefix, "openai")
			}
			for _, tc := range []struct {
				args []string
				want mainDispatchResult
			}{
				{[]string{"mo"}, mainDispatchResult{0, "moderations\nmodels\n", ""}},
				{[]string{"--format", "__complete"}, mainDispatchResult{11, "", ""}},
				{[]string{"models", "retrieve", "--model", "__complete"}, mainDispatchResult{11, "", ""}},
				{[]string{"--mtls-client-cert-file", "candidate-"}, mainDispatchResult{10, "", ""}},
				{[]string{"--format", "__complete", "mo"}, mainDispatchResult{0, "moderations\nmodels\n", ""}},
				{[]string{"--format", "two words", "mo"}, mainDispatchResult{0, "moderations\nmodels\n", ""}},
				{[]string{"--format", "", "mo"}, mainDispatchResult{0, "moderations\nmodels\n", ""}},
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
