package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestMainExploreHelpShowsLocalOptions(t *testing.T) {
	got := runMainDispatch(t, "zsh", "openai", "explore", "--help")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("Explore help failed: %+v", got)
	}
	for _, want := range []string{"--format", "--format-error", "openai help --all explore"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("Explore help omitted %q", want)
		}
	}
	for _, unrelated := range []string{"--api-key", "--mtls-client-key-file", "Key setup:", "\nNOTES\n"} {
		if strings.Contains(got.stdout, unrelated) {
			t.Errorf("Explore help included %q", unrelated)
		}
	}
}

func TestMainExploreRootHelpIntroducesOfflineSearch(t *testing.T) {
	for _, args := range [][]string{{"openai"}, {"openai", "--help"}, {"openai", "help"}, {"openai", "help", "--all"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url", "OPENAI_API_KEY="}, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("root help failed: %+v", got)
			}
			_, start, found := strings.Cut(got.stdout, "\nSTART HERE\n")
			if !found || !strings.HasPrefix(start, "  openai help setup ") || !strings.Contains(start, "Find image commands and examples offline.") {
				t.Fatalf("root help does not introduce setup and local search: %s", got.stdout)
			}
			var example string
			for _, line := range strings.Split(start, "\n") {
				if strings.HasPrefix(line, "  openai explore ") {
					example, _, _ = strings.Cut(strings.TrimSpace(line), "  ")
					break
				}
			}
			if example != "openai explore images" {
				t.Fatalf("missing copyable search example: %q", example)
			}
			search := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url", "OPENAI_API_KEY="}, strings.Fields(example)...)
			if search.code != 0 || search.stderr != "" || !strings.Contains(search.stdout, "MATCHING COMMANDS") || !strings.Contains(search.stdout, "openai images") {
				t.Fatalf("copied command did not run offline search: %+v", search)
			}
		})
	}
}

func TestMainExploreStaysLocalWithInvalidAPIConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"explore"},
		{"--format", "text", "explore"},
		{"explore", "--shell-integration", "--shell", "zsh"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "zsh", []string{
				"OPENAI_BASE_URL=not%url",
				"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/cert.pem",
				"OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic-private/key.pem",
			}, append([]string{"openai"}, args...)...)
			if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "terminal") {
				t.Fatalf("expected terminal requirement instead of API setup: %+v", got)
			}
			if strings.Contains(got.stderr, "synthetic-private") || strings.Contains(got.stderr, "OPENAI_BASE_URL") {
				t.Fatalf("local search used API configuration: %+v", got)
			}
		})
	}
}

func TestMainExploreFlagValueDoesNotBypassAPIConfiguration(t *testing.T) {
	got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url"},
		"openai", "models", "retrieve", "--model", "explore")
	if got.code == 0 || !strings.Contains(got.stderr, "OPENAI_BASE_URL") {
		t.Fatalf("an API argument bypassed request configuration: %+v", got)
	}
}

func TestMainExploreQueryWorksOfflineInPipes(t *testing.T) {
	for _, query := range [][]string{{"models", "list"}, {"MoDeLs LiSt"}} {
		got := runMainDispatchWithEnv(t, "zsh", []string{
			"OPENAI_API_KEY=", "OPENAI_BASE_URL=not%url", "TERM=dumb",
			"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/cert.pem",
			"OPENAI_MTLS_CLIENT_KEY_FILE=/synthetic-private/key.pem",
		}, append([]string{"openai", "explore"}, query...)...)
		if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "MATCHING COMMANDS\n  openai models list\n") {
			t.Fatalf("offline ranked search failed: %+v", got)
		}
		if strings.Contains(got.stdout, "\x1b") || strings.Contains(got.stdout, "synthetic-private") {
			t.Fatalf("search exposed terminal escapes or API configuration: %+v", got)
		}
	}
	query := "no-results-synthetic-private-operand\x1b[31m"
	got := runMainDispatch(t, "zsh", "openai", "explore", query)
	if got.code != 0 || got.stderr != "" || got.stdout != "No matching commands. Try fewer words.\n" {
		t.Fatalf("empty search should succeed without echoing its query: %+v", got)
	}
	images := runMainDispatch(t, "zsh", "openai", "explore", "images")
	if images.code != 0 || !strings.Contains(images.stdout, "openai images generate") || strings.Contains(images.stdout, "openai explore images") {
		t.Fatalf("search should find image commands without suggesting the same search: %+v", images)
	}
}

func TestMainExploreFindsTheSpeechComposer(t *testing.T) {
	got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url"}, "openai", "explore", "speech composer")
	require.Zero(t, got.code)
	require.Empty(t, got.stderr)
	lines := strings.Split(strings.TrimSpace(got.stdout), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	command, description, found := strings.Cut(strings.TrimSpace(lines[1]), "  ")
	require.True(t, found)
	require.Equal(t, "openai speak", command)
	require.Contains(t, description, "composer")
}

func TestMainExploreRanksCommandsForCommonTasks(t *testing.T) {
	for _, tc := range []struct{ query, first string }{
		{"explore text", "tokenizer --help"},
		{"modles list", "models list"},
		{"models ls", "models list"},
		{"show models", "models list"},
		{"see all models", "models list"},
		{"list all models", "models list"},
		{"image generation", "images generate"},
		{"make image", "images generate"},
		{"create an image", "images generate"},
		{"images genrate", "images generate"},
		{"retrieve model", "models retrieve"},
		{"download file", "files download"},
		{"list my files", "files list"},
		{"how do I count tokens offline", "tokenizer count"},
		{"text-to-speech", "speak"},
		{"speech-to-text", "transcribe"},
		{"count tokens offline", "tokenizer count"},
		{"offline token count", "tokenizer count"},
		{"tokens offline co", "tokenizer count"},
		{"  COUNT   tokens off  ", "tokenizer count"},
		{"text to speec", "speak"},
		{"text to audio", "speak"},
		{"read text aloud", "speak"},
		{"speech to text", "transcribe"},
		{"audio to text", "audio transcribe"},
		{"upload a JSONL file", "files upload"},
		{"upload a file for a future batch", "files upload"},
		{"start a batch", "batches submit requests.jsonl"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_BASE_URL=not%url"}, "openai", "explore", tc.query)
			_, results, found := strings.Cut(got.stdout, "MATCHING COMMANDS\n")
			line, _, _ := strings.Cut(results, "\n")
			first, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
			if got.code != 0 || got.stderr != "" || !found || first != "openai "+tc.first {
				t.Fatalf("unexpected search result for %q: %+v", tc.query, got)
			}
		})
	}
}

func TestMainExploreHelpAndStructuredErrors(t *testing.T) {
	help := runMainDispatch(t, "zsh", "openai", "explore", "--help")
	if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, "$ openai explore\n") {
		t.Fatalf("explore help failed: %+v", help)
	}
	full := runMainDispatch(t, "zsh", "openai", "help", "--all", "explore")
	if full.code != 0 || full.stderr != "" || !strings.Contains(full.stdout, `openai explore "count tokens offline"`) {
		t.Fatalf("full Explore help omitted the search example: %+v", full)
	}
	got := runMainDispatch(t, "zsh", "openai", "--format", "json", "explore")
	if got.code != 2 || got.stdout != "" {
		t.Fatalf("unexpected structured failure: %+v", got)
	}
	payload := decodeMainStructuredError(t, "json", got.stderr)
	if !strings.Contains(payload["message"].(string), "format") {
		t.Fatalf("missing format guidance: %#v", payload)
	}
}

func TestMainExploreExplicitMachineErrorsStayStructured(t *testing.T) {
	got := runMainDispatch(t, "zsh", "openai", "--format-error", "json", "explore")
	if got.code != 2 || got.stdout != "" {
		t.Fatalf("unexpected failure: %+v", got)
	}
	payload := decodeMainStructuredError(t, "json", got.stderr)
	if !strings.Contains(payload["message"].(string), "error output") {
		t.Fatalf("missing error-format guidance: %#v", payload)
	}
}

// PowerShell needs its call operator when the executable path is quoted.
func TestMainExploreCopiesTaskFromHelpInPowerShell(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell unavailable")
	}
	work, binary := buildNativeShellCLI(t)
	shell := nativeShell{name: "pwsh", executable: pwsh, args: []string{"-NoProfile", "-NonInteractive", "-Command"}}
	directory, home := t.TempDir(), t.TempDir()
	run := func(command string) string {
		result := runNativeShell(t, shell, directory, home, "invalid-for-requests", command)
		require.Zero(t, result.code, result.stderr)
		require.Empty(t, result.stderr)
		return strings.ReplaceAll(result.stdout, "\r\n", "\n")
	}
	invocation := "& " + terminal.PowerShellQuote(filepath.Join(work, binary))
	short := run(invocation + " explore --help")
	_, reference, found := strings.Cut(short, "\nFULL REFERENCE\n")
	require.True(t, found)
	reference, _, _ = strings.Cut(strings.TrimSpace(reference), "\n")
	require.Equal(t, invocation+" help --all explore", reference)
	help := run(reference)
	var example string
	for line := range strings.SplitSeq(help, "\n") {
		if strings.HasSuffix(line, ` explore "count tokens offline"`) {
			example = strings.TrimPrefix(strings.TrimSpace(line), "$ ")
			break
		}
	}
	require.NotEmpty(t, example)
	output := run(example)
	_, results, found := strings.Cut(output, "MATCHING COMMANDS\n")
	require.True(t, found)
	first, _, _ := strings.Cut(results, "\n")
	command, _, _ := strings.Cut(strings.TrimSpace(first), "  ")
	require.Equal(t, invocation+" tokenizer count", command)
	require.Equal(t, "1", strings.TrimSpace(run(command+" -n --text hello")))
}
