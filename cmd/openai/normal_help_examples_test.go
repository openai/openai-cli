package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainNormalHelpOutputOptions(t *testing.T) {
	for _, args := range [][]string{nil, {"models", "list"}, {"responses", "create"}} {
		t.Run(strings.Join(append([]string{"root"}, args...), "/"), func(t *testing.T) {
			argv := append([]string{"openai"}, args...)
			got := runMainDispatch(t, "bash", append(argv, "--help")...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("normal help failed: %+v", got)
			}
			wantOptions := []string{"GLOBAL OPTIONS:", "--format", "--transform", "--raw-output", "Global option details: openai --help"}
			if len(args) == 0 {
				wantOptions = []string{"\n   Output\n", "--format FORMAT", "--transform PATH", "--raw-output"}
			}
			for _, want := range wantOptions {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("normal help lacks %q:\n%s", want, got.stdout)
				}
			}
		})
	}
	for _, args := range [][]string{{"images", "inline"}, {"images", "inline", "on"}, {"images", "inline", "off"}} {
		got := runMainDispatch(t, "bash", append(append([]string{"openai"}, args...), "--help")...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("local image help failed: %+v", got)
		}
		text := strings.Join(strings.Fields(got.stdout), " ")
		for _, want := range []string{"This local command supports --format auto or text only.", "It cannot use --transform or --raw-output."} {
			if !strings.Contains(text, want) {
				t.Errorf("local help omitted output restriction %q", want)
			}
		}
	}
}

func TestMainNormalHelpModelsLimit(t *testing.T) {
	server, requests := normalHelpModelsServer(t)
	for _, args := range [][]string{{"models", "list", "--help"}, {"help", "models", "list"}} {
		got := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
		text := strings.Join(strings.Fields(got.stdout), " ")
		if got.code != 0 || got.stderr != "" || !strings.Contains(text, "--max-items INTEGER") ||
			!strings.Contains(text, "omit or use -1 for unlimited, or use 0 for no items") {
			t.Fatalf("help does not explain the limit contract: %+v", got)
		}
		if args[0] == "help" && !strings.Contains(text, "Default: unlimited") {
			t.Fatalf("full help displays the wrong omitted default: %s", text)
		}
	}
	for _, tc := range []struct {
		name, want string
		flags      []string
	}{
		{"omitted", "synthetic-model-one\nsynthetic-model-two\n", nil},
		{"unlimited", "synthetic-model-one\nsynthetic-model-two\n", []string{"--max-items", "-1"}},
		{"zero", "", []string{"--max-items", "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests.Store(0)
			args := append([]string{"openai", "models", "list", "--transform", "id", "--raw-output"}, tc.flags...)
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=fake-native-shell-key",
			}, args...)
			if got.code != 0 || got.stderr != "" || got.stdout != tc.want || requests.Load() != 1 {
				t.Fatalf("limit contract changed: result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainNormalHelpNativeModelExamples(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash/zsh invocation checks require a Unix host")
	}
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(work, "openai")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	invocation := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	server, requests := normalHelpModelsServer(t)
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{"bash", "bash", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell is unavailable: %v", err)
				}
				t.Skipf("%s is not installed", shell.name)
			}
			shell.executable = path
			for _, lookup := range []string{"absent", "unrelated", "installed"} {
				t.Run(lookup, func(t *testing.T) {
					directory, home, searchPath := t.TempDir(), t.TempDir(), t.TempDir()
					printedInvocation := invocation
					if lookup == "unrelated" {
						if err := os.WriteFile(filepath.Join(searchPath, "openai"), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700); err != nil {
							t.Fatal(err)
						}
					} else if lookup == "installed" {
						searchPath, printedInvocation = work, "openai"
					}
					t.Setenv("PATH", searchPath)
					requests.Store(0)
					help := runNativeShell(t, shell, directory, home, server.URL, invocation+" models list --help")
					if help.code != 0 || help.stderr != "" || requests.Load() != 0 {
						t.Fatalf("normal help failed or made a request: %+v", help)
					}
					var examples []string
					_, exampleSection, found := strings.Cut(help.stdout, "EXAMPLES:\n")
					if !found {
						t.Fatal("model help omitted examples")
					}
					exampleSection, _, _ = strings.Cut(exampleSection, "\nOPTIONS:")
					for line := range strings.SplitSeq(exampleSection, "\n") {
						if strings.HasPrefix(strings.TrimSpace(line), printedInvocation+" models list") {
							examples = append(examples, strings.TrimSpace(line))
						}
					}
					if len(examples) != 3 {
						t.Fatalf("expected three examples using %q:\n%s", printedInvocation, help.stdout)
					}
					for i, suffix := range []string{"models list", "models list --format json", "models list --transform id --raw-output"} {
						if examples[i] != printedInvocation+" "+suffix {
							t.Fatalf("example changed: %q", examples[i])
						}
						requests.Store(0)
						got := runNativeShell(t, shell, directory, home, server.URL, shell.setKey+examples[i])
						if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
							t.Fatalf("copied example %q failed: result=%+v requests=%d", examples[i], got, requests.Load())
						}
						switch i {
						case 0:
							for _, id := range []string{"synthetic-model-one", "synthetic-model-two"} {
								if strings.Count(got.stdout, id) != 1 {
									t.Fatalf("readable example lost or duplicated %q: %q", id, got.stdout)
								}
							}
						case 1:
							decoder := json.NewDecoder(strings.NewReader(got.stdout))
							for _, id := range []string{"synthetic-model-one", "synthetic-model-two"} {
								var item map[string]any
								if err := decoder.Decode(&item); err != nil || item["id"] != id || item["synthetic_field"] != "retained" || len(item) != 5 {
									t.Fatalf("JSON example lost complete model data: %q (%v)", got.stdout, err)
								}
							}
							var extra any
							if err := decoder.Decode(&extra); err != io.EOF {
								t.Fatalf("JSON example contains extra output: %q (%v)", got.stdout, err)
							}
						case 2:
							if got.stdout != "synthetic-model-one\nsynthetic-model-two\n" {
								t.Fatalf("extraction example did not print unquoted IDs: %q", got.stdout)
							}
						}
					}
				})
			}
		})
	}
}

func normalHelpModelsServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/models" || r.URL.RawQuery != "" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer fake-native-shell-key" {
			t.Error("request lacks the synthetic shell credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"object":"list","data":[`+
			`{"id":"synthetic-model-one","object":"model","created":1,"owned_by":"test","synthetic_field":"retained"},`+
			`{"id":"synthetic-model-two","object":"model","created":2,"owned_by":"test","synthetic_field":"retained"}]}`)
		if err != nil {
			t.Errorf("write synthetic model response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests
}
