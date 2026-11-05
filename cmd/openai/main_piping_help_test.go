package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestMainPipingDiscovery(t *testing.T) {
	env := []string{"OPENAI_BASE_URL=invalid-for-requests"}
	root := runMainDispatchWithEnv(t, "bash", env, "openai", "--help")
	_, intro, ok := strings.Cut(root.stdout, "START HERE\n")
	intro, _, _ = strings.Cut(intro, "\nSHORTCUTS\n")
	if root.code != 0 || !ok || !strings.Contains(intro, "openai help piping") {
		t.Fatal("root help must introduce the piping guide")
	}

	for path, body := range map[string]string{
		"responses create":        `{"model":"gpt-5.5","input":"Say hello"}`,
		"chat completions create": `{"model":"gpt-5.5","messages":[{"role":"user","content":"Say hello"}]}`,
	} {
		args := append(strings.Fields("openai "+path), "--help")
		got := runMainDispatchWithEnv(t, "bash", env, args...)
		intro, _, ok := strings.Cut(got.stdout, "\nDESCRIPTION\n")
		if got.code != 0 || !ok || !strings.Contains(intro, "--request-file=PATH|-") ||
			!strings.Contains(intro, "echo '"+body+"' | openai "+path+" --request-file -") {
			t.Errorf("%s omitted the request pipe from its synopsis:\n%s", path, intro)
		}
	}
}

func TestMainPipingHelpExamplesRunAsPrinted(t *testing.T) {
	work, binary := buildNativeShellCLI(t)
	for _, shell := range []nativeShell{
		{name: "bash", args: []string{"--noprofile", "--norc", "-c"}},
		{name: "zsh", args: []string{"-f", "-c"}},
		{name: "fish", args: []string{"--no-config", "-c"}},
		{name: "pwsh", args: []string{"-NoProfile", "-NonInteractive", "-Command"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.name)
			if err != nil {
				t.Skipf("%s is unavailable", shell.name)
			}
			shell.executable = path
			home := t.TempDir()
			directory := t.TempDir()
			invocation := terminal.ShellQuoter(shell.name)(filepath.Join(work, binary))
			if shell.name == "pwsh" {
				invocation = "& " + invocation
			}
			for _, tc := range []struct{ help, endpoint, body string }{
				{"responses create --help", "/responses", `{"model":"gpt-5.5","input":"Say hello"}`},
				{"chat completions create --help", "/chat/completions", `{"model":"gpt-5.5","messages":[{"role":"user","content":"Say hello"}]}`},
				{"help piping", "/responses", `{"model":"gpt-5.5","input":"Say hello"}`},
			} {
				t.Run(tc.help, func(t *testing.T) {
					help := runNativeShell(t, shell, directory, home, "invalid-for-requests", invocation+" "+tc.help)
					require.Zero(t, help.code, help.stderr)
					var pipeline string
					for line := range strings.SplitSeq(help.stdout, "\n") {
						if strings.Contains(line, " | ") && strings.HasSuffix(strings.TrimSpace(line), "--request-file -") {
							pipeline = strings.TrimPrefix(strings.TrimSpace(line), "$ ")
							break
						}
					}
					require.NotEmpty(t, pipeline)
					server, requests := globalFlagsServer(t, "{}")
					got := runNativeShell(t, shell, directory, home, server.URL, pipeline)
					require.Zero(t, got.code, got.stderr)
					req := globalFlagsOneRequest(t, requests)
					require.Equal(t, tc.endpoint, req.path)
					require.JSONEq(t, tc.body, string(req.body))
				})
			}
		})
	}
}

func TestMainPipingHelp(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	const sentinel = "synthetic unread stdin"
	if _, err := io.WriteString(writer, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	env := []string{"OPENAI_BASE_URL=invalid-for-requests", "OPENAI_MTLS_CLIENT_CERT_FILE=/missing/synthetic.pem"}
	for _, args := range [][]string{{"openai", "help", "piping"}, {"openai", "help", "piping", "--help"}} {
		result := runMainDispatchWithStdin(t, "bash", env, input, args...)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("%q failed: %+v", args, result)
		}
		for _, example := range []string{
			"printf hello | openai tokenizer view",
			"printf hello | openai tokenizer inspect --format json",
			"printf hello | openai tokenizer count -n",
			"count --transform input_bytes",
			"openai responses create --model MODEL --input @- < prompt.txt",
			"openai files create --purpose user_data --file - < notes.txt",
			"chat completions create",
			`echo '{"model":"gpt-5.5","input":"Say hello"}' | openai responses create --request-file -`,
			"openai embeddings create < request.json",
			"openai models list --format jsonl",
		} {
			if !strings.Contains(result.stdout, example) {
				t.Errorf("%q omitted %q", args, example)
			}
		}
	}
	got, err := io.ReadAll(input)
	if err != nil || string(got) != sentinel {
		t.Fatalf("help consumed stdin, got %q, err %v", got, err)
	}

	result := runMainDispatchWithEnv(t, "bash", env, "openai", "help", "piping", "synthetic-private-operand")
	if result.code != 3 || result.stdout != "" || !strings.Contains(result.stderr, "Unknown help topic") || strings.Contains(result.stderr, "synthetic-private-operand") {
		t.Fatalf("unexpected extra-argument result: %+v", result)
	}
}
