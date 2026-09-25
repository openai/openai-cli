package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func TestMainReadableErrorsStayOnStderr(t *testing.T) {
	for _, test := range []struct {
		name, contentType, body, want string
		status                        int
	}{
		{"API error", "application/json", `{"error":{"message":"synthetic private response detail","code":"model_not_found","param":"model"}}`, "Request failed (404", http.StatusNotFound},
		{"gateway text", "text/plain", "synthetic private response detail", "Request failed (502", http.StatusBadGateway},
		{"malformed JSON", "application/json", `{"error":{"message":"synthetic private response detail"`, "Request failed (502", http.StatusBadGateway},
		{"unexpected shape", "application/json", `{"error":"synthetic private response detail"}`, "Could not decode JSON", http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runMainAPIErrorResponse(t, test.status, test.contentType, test.body)
			if !strings.Contains(result.stderr, test.want) {
				t.Errorf("missing readable diagnostic %q: %q", test.want, result.stderr)
			}
			if strings.Contains(result.stderr, "synthetic private response detail") || json.Valid([]byte(result.stderr)) {
				t.Errorf("default error did not use a safe summary: %q", result.stderr)
			}
		})
	}
}

func TestMainReadableAPIArgumentGuidance(t *testing.T) {
	for _, test := range []struct {
		name, parameter, code string
		status                int
		args, want            []string
	}{
		{"encoding choice", "encoding_format", "invalid_value", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"--encoding-format", "float or base64", "help --all embeddings create"}},
		{"nested indexed field", "messages[0].content", "invalid_type", 422,
			[]string{"chat:completions", "create", "--model", "model-synthetic", "--message", `{"role":"user","content":"synthetic"}`}, []string{"--message", "Check the value's type", "help --all chat:completions create"}},
		{"nested command flag", "session.model", "invalid_value", 400,
			[]string{"live:sessions", "accept", "--session-id", "sess_synthetic", "--session", `{"type":"live","model":"synthetic"}`}, []string{"--session.model", "help --all live:sessions accept"}},
		{"required value", "input", "missing_required_parameter", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"Add --input with a value"}},
		{"unsupported field", "temperature", "unsupported_parameter", 400,
			[]string{"responses", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"does not support --temperature", "Remove it or choose a model"}},
		{"model context", "input", "context_length_exceeded", 400,
			[]string{"responses", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"Shorten the input or conversation"}},
		{"unknown sensitive param", "input\x1b]52;c;synthetic-private-secret\a\u202e", "synthetic-private-code", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"The API rejected the request", "help --all embeddings create"}},
		{"wrong command flag", "temperature", "invalid_value", 400,
			[]string{"embeddings", "create", "--model", "model-synthetic", "--input", "synthetic"}, []string{"The API rejected the request"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			apiError := map[string]any{
				"message": "synthetic-private-secret rejected input https://synthetic.invalid/?token=synthetic-private-token\x1b]52;c;synthetic-private-secret\a\u202e",
				"param":   test.parameter, "code": test.code, "type": "invalid_request_error",
			}
			payload, err := json.Marshal(map[string]any{"error": apiError})
			if err != nil {
				t.Fatal(err)
			}
			got := runMainCommandAPIErrorResponse(t, test.status, "application/json", string(payload), test.args)
			for _, want := range test.want {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("missing %q: %q", want, got.stderr)
				}
			}
			for _, private := range []string{"synthetic-private-", "https://", "token=", "\x1b", "\a", "\u202e"} {
				if strings.Contains(got.stderr, private) {
					t.Errorf("untrusted details shown: %q", got.stderr)
				}
			}
			if test.name == "wrong command flag" && strings.Contains(got.stderr, "--temperature") {
				t.Errorf("suggested a flag absent from this command: %q", got.stderr)
			}
			got = runMainCommandAPIErrorResponse(t, test.status, "application/json", string(payload), test.args, "--format", "json")
			if value := decodeMainErrorObject(t, "json", got.stderr); !reflect.DeepEqual(value, apiError) {
				t.Errorf("explicit JSON changed API error fields: got %#v, want %#v", value, apiError)
			}
		})
	}
}

func TestMainLocalErrorsStayOnStderr(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	for _, test := range []struct {
		name, want string
		args       []string
	}{
		{"missing required flag", "--model", []string{"embeddings", "create", "--input", "synthetic-private-input"}},
		{"missing flag value", "--model", []string{"models", "retrieve", "--model"}},
		{"invalid number", "--temperature", []string{"responses", "create", "--model", "synthetic-private-model", "--input", "synthetic-private-input", "--temperature", "synthetic-private-value"}},
		{"unknown flag", "--help", []string{"models", "list", "--synthetic-private-option=synthetic-private-value"}},
	} {
		for _, flags := range [][]string{nil, {"--format-error", "json"}, {"--format", "yaml"}} {
			t.Run(test.name+"/"+strings.Join(flags, "/"), func(t *testing.T) {
				args := append([]string{"openai", "--base-url", server.URL}, flags...)
				args = append(args, test.args...)
				result := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-private-key"}, args...)
				if result.code != 1 || result.stdout != "" || !strings.Contains(result.stderr, test.want) {
					t.Errorf("local error = %+v; want exit 1, empty stdout and guidance for %s", result, test.want)
				}
				if strings.Contains(result.stderr, "synthetic-private-") {
					t.Errorf("local diagnostic echoed sensitive input: %q", result.stderr)
				}
				if len(flags) > 0 {
					payload := decodeMainStructuredError(t, flags[1], result.stderr)
					if _, exists := payload["status_code"]; exists {
						t.Error("local error has an invented HTTP status")
					}
				}
			})
		}
	}
	if requests.Load() != 0 {
		t.Errorf("invalid local arguments sent %d requests, want 0", requests.Load())
	}
}

func TestMainSensitiveLocalErrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	missingFile := filepath.Join(t.TempDir(), "synthetic-private-fake-secret.txt")
	for _, test := range []struct {
		name, stdin, want string
		env, args         []string
	}{
		{
			name: "invalid base URL", want: "OPENAI_BASE_URL",
			env:  []string{"OPENAI_BASE_URL=synthetic-private-host/?token=fake-secret"},
			args: []string{"models", "list"},
		},
		{
			name: "missing file", want: "local file",
			args: []string{"responses", "create", "--model", "synthetic-model", "--input", "@" + missingFile},
		},
		{
			name: "malformed piped YAML", stdin: "synthetic-private-input: [synthetic-private-fake-secret\n", want: "piped input",
			args: []string{"responses", "create", "--model", "synthetic-model"},
		},
		{
			name: "scalar piped body", stdin: "synthetic-private-fake-secret\n", want: "JSON or YAML object",
			args: []string{"responses", "create", "--model", "synthetic-model"},
		},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				var stdin *os.File
				if test.stdin != "" {
					path := filepath.Join(t.TempDir(), "request")
					if err := os.WriteFile(path, []byte(test.stdin), 0o600); err != nil {
						t.Fatal(err)
					}
					var err error
					stdin, err = os.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = stdin.Close() })
				}
				args := []string{"openai", "--base-url", server.URL, "--format-error", format}
				args = append(args, test.args...)
				env := append([]string{"OPENAI_API_KEY=synthetic-private-key"}, test.env...)
				result := runMainDispatchWithStdin(t, "bash", env, stdin, args...)
				if result.code == 0 || result.stdout != "" || !strings.Contains(result.stderr, test.want) {
					t.Errorf("local error = %+v; want failure, empty stdout and guidance containing %q", result, test.want)
				}
				for _, private := range []string{"synthetic-private-", "fake-secret", "token=", missingFile} {
					if strings.Contains(result.stderr, private) {
						t.Errorf("local diagnostic echoed sensitive input: %q", result.stderr)
					}
				}
				if format == "json" {
					decodeMainStructuredError(t, format, result.stderr)
				}
			})
		}
	}
	if requests.Load() != 0 {
		t.Errorf("invalid local input sent %d requests, want 0", requests.Load())
	}
}

func TestMainWrappedExitCodeProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_WRAPPED_ERROR_PROCESS") != "1" {
		return
	}
	cmd.Command = &cli.Command{
		Name: "openai", ErrWriter: &cmd.CommandErrorBuffer,
		Commands: []*cli.Command{{Name: "synthetic", Category: "API RESOURCE", Commands: []*cli.Command{{
			Name: "fail", Action: func(context.Context, *cli.Command) error {
				return cli.Exit("synthetic-private-failure", 7)
			},
		}}}},
	}
	custom.ConfigureCommand(cmd.Command)
	os.Args = []string{"openai", "synthetic", "fail"}
	main()
	os.Exit(0)
}

func TestMainPreservesWrappedExitCode(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainWrappedExitCodeProcess$")
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, "OPENAI_CLI_WRAPPED_ERROR_PROCESS=1")
	var stdout, stderr strings.Builder
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	if ctx.Err() != nil {
		t.Fatalf("main timed out: %v", ctx.Err())
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 7 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("wrapped exit: err=%v stdout=%q stderr=%q, want exit 7 with stderr only", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "synthetic-private-") {
		t.Errorf("wrapped error echoed sensitive input: %q", stderr.String())
	}
}

func TestMainHelpErrorsUsePresenter(t *testing.T) {
	for _, test := range []struct {
		args []string
		code int
	}{
		{[]string{"help", "synthetic-private-topic"}, 3},
		{[]string{"help", "setup", "synthetic-private-extra"}, 3},
		{[]string{"help", "--synthetic-private-option"}, 1},
		{[]string{"files", "help", "synthetic-private-topic"}, 3},
		{[]string{"files", "help", "--all", "synthetic-private-topic"}, 3},
		{[]string{"models", "synthetic-private-topic"}, 3},
		{[]string{"synthetic-private-topic"}, 3},
	} {
		for _, format := range []string{"auto", "json"} {
			args := append([]string{"openai", "--format-error", format}, test.args...)
			got := runMainDispatch(t, "bash", args...)
			if got.code != test.code || got.stdout != "" || got.stderr == "" || strings.Contains(got.stderr, "synthetic-private-") {
				t.Errorf("help error = %+v, want exit %d and safe stderr only", got, test.code)
			}
			if format == "json" {
				decodeMainStructuredError(t, format, got.stderr)
			}
		}
	}
}

func TestMainNestedHelpErrorFormatPrecedence(t *testing.T) {
	for _, flags := range [][]string{
		{"--format", "json"},
		{"--format", "yaml", "--format-error", "json"},
		{"--format-error", "json", "--transform-error", "message"},
	} {
		for _, help := range [][]string{{"files", "help", "synthetic-private-topic"}, {"help", "files", "synthetic-private-topic"}} {
			args := append(append([]string{"openai"}, flags...), help...)
			got := runMainDispatch(t, "bash", args...)
			if got.code != 3 || got.stdout != "" || strings.Contains(got.stderr, "synthetic-private-") || !json.Valid([]byte(got.stderr)) {
				t.Errorf("nested help ignored format: %+v", got)
			}
			if flags[len(flags)-1] == "message" {
				var message string
				if err := json.Unmarshal([]byte(got.stderr), &message); err != nil || !strings.Contains(message, "Unknown help topic") {
					t.Errorf("nested help extraction = %q, error %v", got.stderr, err)
				}
			}
		}
	}
	got := runMainDispatch(t, "bash", "openai", "--format", "json", "files", "help", "--format-error", "auto", "synthetic-private-topic")
	if got.code != 3 || got.stdout != "" || !strings.Contains(got.stderr, "Unknown help topic") || json.Valid([]byte(got.stderr)) {
		t.Errorf("nested help ignored readable override: %+v", got)
	}
}
