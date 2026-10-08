package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

func codexTestCommand(out io.Writer, open func(context.Context, string) error) *cli.Command {
	return &cli.Command{
		Name: "openai", Writer: out, ErrWriter: io.Discard, Reader: codexRejectReader{},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
		Commands: []*cli.Command{codexCommand(open)},
	}
}

type codexRejectReader struct{}

func (codexRejectReader) Read([]byte) (int, error) { panic("Codex instructions must not read stdin") }

func codexRejectOpen(context.Context, string) error { panic("unexpected browser launch") }

func TestCodexInstructionsLocalAndDeterministic(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "://irrelevant-invalid-url")
	t.Setenv("OPENAI_MTLS_CLIENT_KEY_FILE", "/nonexistent/irrelevant-key")
	configDir := t.TempDir()
	t.Setenv("CODEX_HOME", configDir)
	t.Setenv("PATH", t.TempDir())
	configPath := filepath.Join(configDir, "config.toml")
	const config = "invalid config: must remain unchanged\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"auto", "text", "json", "JSON"} {
		t.Run(format, func(t *testing.T) {
			var previous string
			for iteration := 0; iteration < 2; iteration++ {
				var out strings.Builder
				root := codexTestCommand(&out, codexRejectOpen)
				if err := root.Run(t.Context(), []string{"openai", "--format", format, "codex"}); err != nil {
					t.Fatal(err)
				}
				if iteration > 0 && previous != out.String() {
					t.Fatal("repeated instruction output changed")
				}
				previous = out.String()
				if strings.EqualFold(format, "json") {
					var got codexInstructions
					if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
						t.Fatal(err)
					}
					if got.Command != "codex" || got.DefaultConfig != "~/.codex/config.toml" || got.ProjectConfig != ".codex/config.toml" {
						t.Fatalf("wrong command or configuration: %+v", got)
					}
					if len(got.Installation) != 2 || got.Installation[0].Command != "npm install -g @openai/codex" || got.Installation[1].Command != "brew install --cask codex" {
						t.Fatalf("wrong installation instructions: %+v", got.Installation)
					}
					if got.ConfigExample != "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"" || len(got.Destinations) != 4 {
						t.Fatalf("missing configuration or destinations: %+v", got)
					}
				} else {
					for _, want := range []string{"separate command named codex", "npm install -g @openai/codex", "brew install --cask codex", "\n  codex\n", "~/.codex/config.toml", "Trusted project configuration", "CODEX_HOME", codexConfigURL} {
						if !strings.Contains(out.String(), want) {
							t.Errorf("instruction output lacks %q", want)
						}
					}
				}
			}
		})
	}
	data, err := os.ReadFile(configPath)
	if err != nil || string(data) != config {
		t.Fatalf("changed Codex configuration: %q, %v", data, err)
	}
	entries, err := os.ReadDir(configDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("created configuration artifacts: %v, %v", entries, err)
	}
}

func TestCodexFixedDestinations(t *testing.T) {
	for name, wantURL := range map[string]string{
		"docs": "https://learn.chatgpt.com/docs/cli", "config": "https://learn.chatgpt.com/docs/config-file/config-basic",
		"app": "https://learn.chatgpt.com/docs/app", "web": "https://chatgpt.com/",
	} {
		for _, format := range []string{"auto", "text", "json"} {
			t.Run(name+"/"+format, func(t *testing.T) {
				var out strings.Builder
				root := codexTestCommand(&out, codexRejectOpen)
				if err := root.Run(t.Context(), []string{"openai", "--format", format, "codex", "--destination", name}); err != nil {
					t.Fatal(err)
				}
				if format == "json" {
					var got map[string]string
					if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, map[string]string{"destination": name, "url": wantURL}) {
						t.Fatalf("unexpected destination result: %+v", got)
					}
				} else if out.String() != wantURL+"\n" {
					t.Fatalf("destination output = %q; want %q", out.String(), wantURL+"\n")
				}
			})
		}
	}
}

func TestCodexRejectsUnsupportedInputsBeforeSideEffects(t *testing.T) {
	for _, args := range [][]string{
		{"codex", "extra"}, {"codex", "--open"}, {"codex", "--destination", ""},
		{"codex", "--destination", "unknown"}, {"codex", "--destination", "https://example.com/"},
		{"codex", "--destination", "$(synthetic-private-command)\x1b[31m"},
		{"codex", "--destination", "docs", "--destination", "web"},
		{"--format", "explore", "codex"}, {"--format", "yaml", "codex"},
		{"--format", "jsonl", "codex"}, {"--format", "", "codex"},
		{"--transform", "url", "codex"}, {"--transform", "", "codex"},
		{"--raw-output", "codex"}, {"--raw-output=false", "codex"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out strings.Builder
			root := codexTestCommand(&out, codexRejectOpen)
			err := root.Run(t.Context(), append([]string{"openai"}, args...))
			if err == nil {
				t.Fatal("accepted unsupported input")
			}
			if strings.Contains(err.Error(), "synthetic-private-command") {
				t.Fatalf("exposed rejected destination: %v", err)
			}
			if strings.Contains(out.String(), "https://") || strings.Contains(out.String(), "npm install") {
				t.Fatalf("printed result before validating input: %q", out.String())
			}
		})
	}
}

func TestCodexOpenIsExplicitAndFollowsOutput(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var out strings.Builder
			calls := 0
			root := codexTestCommand(&out, func(ctx context.Context, url string) error {
				calls++
				if url != codexDocsURL || !strings.Contains(out.String(), codexDocsURL) {
					t.Fatalf("wrong destination or missing prior output: %q; %q", url, out.String())
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
					t.Fatalf("missing ten-second launcher deadline: %v; %v", deadline, ok)
				}
				return nil
			})
			if err := root.Run(t.Context(), []string{"openai", "--format", format, "codex", "--destination", "docs", "--open"}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("browser calls = %d; want 1", calls)
			}
		})
	}
}

type codexFailedWriter struct{ err error }

func (w codexFailedWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCodexOutputFailurePreventsOpen(t *testing.T) {
	failure := errors.New("synthetic private writer failure")
	for _, format := range []string{"text", "json"} {
		for _, writerError := range []error{failure, nil} {
			root := codexTestCommand(codexFailedWriter{writerError}, codexRejectOpen)
			err := root.Run(t.Context(), []string{"openai", "--format", format, "codex", "--destination", "docs", "--open"})
			want := writerError
			if want == nil {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) || strings.Contains(err.Error(), "synthetic private") {
				t.Fatalf("writer failure lost or exposed: %v; want cause %v", err, want)
			}
		}
	}
}

func TestCodexOpenFailurePreservesURL(t *testing.T) {
	for _, failure := range []error{os.ErrNotExist, errors.New("synthetic private launcher output"), context.DeadlineExceeded} {
		var out strings.Builder
		root := codexTestCommand(&out, func(context.Context, string) error { return failure })
		err := root.Run(t.Context(), []string{"openai", "codex", "--destination", "web", "--open"})
		if !errors.Is(err, failure) || out.String() != codexWebURL+"\n" {
			t.Fatalf("lost failure or manual URL: %q; %v", out.String(), err)
		}
		if !strings.Contains(err.Error(), "Open this URL manually: "+codexWebURL) || strings.Contains(err.Error(), "synthetic private") {
			t.Fatalf("unsafe or incomplete browser error: %v", err)
		}
	}
}

func TestCodexCancellation(t *testing.T) {
	t.Run("before output", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var out strings.Builder
		root := codexTestCommand(&out, codexRejectOpen)
		err := root.Run(ctx, []string{"openai", "codex", "--destination", "docs", "--open"})
		if !errors.Is(err, context.Canceled) || out.Len() != 0 {
			t.Fatalf("canceled command produced output or lost cancellation: %q; %v", out.String(), err)
		}
	})
	t.Run("during launcher", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var out strings.Builder
		root := codexTestCommand(&out, func(ctx context.Context, _ string) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		})
		err := root.Run(ctx, []string{"openai", "codex", "--destination", "docs", "--open"})
		if !errors.Is(err, context.Canceled) || out.String() != codexDocsURL+"\n" {
			t.Fatalf("lost cancellation or prior output: %q; %v", out.String(), err)
		}
	})
}

func TestCodexBrowserCommandUsesDirectArguments(t *testing.T) {
	for _, tc := range []struct {
		goos string
		name string
		args []string
	}{
		{"darwin", "open", []string{codexDocsURL}},
		{"linux", "xdg-open", []string{codexDocsURL}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", codexDocsURL}},
	} {
		name, args, err := codexBrowserCommand(tc.goos, codexDocsURL)
		if err != nil || name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%s: got %q %v, %v", tc.goos, name, args, err)
		}
	}
	if _, _, err := codexBrowserCommand("unsupported", codexDocsURL); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestCodexMissingBrowserDoesNotFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := openCodexDestination(t.Context(), codexDocsURL); err == nil {
		t.Fatal("browser launch succeeded without a launcher")
	}
}

func TestCodexBrowserEnvironmentKeepsDesktopSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native fixture uses a POSIX launcher")
	}
	dir := t.TempDir()
	name, _, err := codexBrowserCommand(runtime.GOOS, codexDocsURL)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "launcher-result")
	script := `#!/bin/sh
test -z "${OPENAI_API_KEY+x}" || exit 21
test -z "${OPENAI_ADMIN_KEY+x}" || exit 22
test -z "${OPENAI_WEBHOOK_SECRET+x}" || exit 23
test -z "${OPENAI_BASE_URL+x}" || exit 24
test -z "${openai_api_key+x}" || exit 25
test "$DISPLAY" = synthetic-display || exit 26
test "$HOME" = "$F29_BROWSER_HOME" || exit 27
test "$1" = 'https://learn.chatgpt.com/docs/cli' || exit 28
printf 'passed\n' > "$F29_BROWSER_REPORT"
`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	t.Setenv("F29_BROWSER_HOME", dir)
	t.Setenv("F29_BROWSER_REPORT", report)
	t.Setenv("DISPLAY", "synthetic-display")
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "OPENAI_BASE_URL", "openai_api_key"} {
		t.Setenv(key, "synthetic-browser-isolation-value")
	}
	if err := openCodexDestination(t.Context(), codexDocsURL); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(report)
	if err != nil || string(data) != "passed\n" {
		t.Fatalf("launcher verification failed: %v", err)
	}
	if os.Getenv("OPENAI_API_KEY") != "synthetic-browser-isolation-value" {
		t.Fatal("launcher changed the parent's environment")
	}
}

func TestCodexBrowserEnvironmentFiltersOnlyOpenAISettings(t *testing.T) {
	settings := []string{"PATH=/synthetic/bin", "OPENAI_API_KEY=fake", "OpenAi_ADMIN_KEY=fake", "OPENAI_EMPTY=",
		"BROWSER=synthetic-browser", "SystemRoot=C:\\Windows", "=C:=C:\\synthetic", "UNRELATED=exact=value", "OPENAI=fake"}
	want := []string{"PATH=/synthetic/bin", "BROWSER=synthetic-browser", "SystemRoot=C:\\Windows",
		"=C:=C:\\synthetic", "UNRELATED=exact=value", "OPENAI=fake"}
	if got := codexBrowserEnvironment(settings); !reflect.DeepEqual(got, want) {
		t.Fatalf("desktop environment changed: %#v", got)
	}
	for _, settings := range [][]string{nil, {"OPENAI_API_KEY=fake", "openai_admin_key="}} {
		if got := codexBrowserEnvironment(settings); got == nil || len(got) != 0 {
			t.Fatalf("empty child environment must remain explicit: %#v", got)
		}
	}
}
