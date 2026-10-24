package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainTrainingInputsAreUnavailable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	file := filepath.Join(dir, "synthetic-private-upload.txt")
	purpose := filepath.Join(dir, "purpose.txt")
	event := filepath.Join(dir, "event.txt")
	for path, data := range map[string]string{file: "synthetic-private-content", purpose: "fine-tune", event: "fine_tuning.job.succeeded"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const fileError = "This file purpose is unavailable in the CLI. Use the Platform API directly."
	const eventError = "This webhook request contains events unavailable in the CLI. Use the Platform API directly."
	uploadArgs := []string{"uploads", "create", "--filename", "synthetic-private-upload.txt", "--bytes", "25", "--mime-type", "text/plain"}
	for _, test := range []struct {
		name, input, message string
		args                 []string
	}{
		{"upload purpose", "", fileError, []string{"files", "upload", file, "--purpose", "fine-tune"}},
		{"vision purpose", "", fileError, []string{"files", "create", "--file", file, "--purpose", "vision"}},
		{"scalar purpose file", "", fileError, []string{"files", "upload", file, "--purpose", "@" + purpose}},
		{"file JSON", `{"purpose":"fine-tune"}`, fileError, []string{"files", "create", "--file", file}},
		{"file YAML", "purpose: vision\n", fileError, []string{"files", "upload", file}},
		{"trusted piped scalar", `{"purpose":"@` + purpose + `"}`, fileError, []string{"files", "create", "--file", file}},
		{"upload JSON", `{"purpose":"fine-tune"}`, fileError, uploadArgs},
		{"upload YAML", "purpose: vision\n", fileError, uploadArgs},
		{"upload scalar", "", fileError, append(append([]string{}, uploadArgs...), "--purpose", "@"+purpose)},
		{"file query", "", fileError, []string{"files", "list", "--purpose", "fine-tune"}},
		{"result query", "", fileError, []string{"files", "list", "--purpose", "fine-tune-results"}},
		{"vision query", "", fileError, []string{"files", "list", "--purpose", "vision"}},
		{"query scalar file", "", fileError, []string{"files", "list", "--purpose", "@" + purpose}},
		{"webhook mixed create", "", eventError, []string{"webhooks", "create", "--name", "synthetic-private-endpoint", "--url", "https://synthetic.invalid/", "--event-type", "response.completed", "--event-type", "fine_tuning.job.succeeded"}},
		{"webhook mixed update", `{"event_types":["response.completed","fine_tuning.job.failed"]}`, eventError, []string{"webhooks", "update", "synthetic-private-endpoint"}},
		{"webhook update YAML", "event_types:\n  - fine_tuning.job.cancelled\n", eventError, []string{"webhooks", "update", "synthetic-private-endpoint"}},
		{"webhook test scalar", "", eventError, []string{"webhooks", "test", "synthetic-private-endpoint", "--event-type", "@" + event}},
		{"webhook test JSON", `{"event_type":"fine_tuning.job.succeeded"}`, eventError, []string{"webhooks", "test", "synthetic-private-endpoint"}},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				stdin := retirementStdin(t, test.input)
				env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-private-key", "OPENAI_UNTRUSTED_STDIN=false"}
				args := append([]string{"openai", "--format-error", format}, test.args...)
				got := runMainDispatchWithStdin(t, "bash", env, stdin, args...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("retired input = %+v; want exit 1 and no stdout", got)
				}
				message := strings.TrimSpace(got.stderr)
				if format == "json" {
					payload := decodeMainStructuredError(t, format, got.stderr)
					message, _ = payload["message"].(string)
					if _, exists := payload["status_code"]; exists {
						t.Error("local rejection invented an HTTP status")
					}
				}
				if message != test.message || strings.Contains(got.stderr, "synthetic-private") {
					t.Fatalf("retired input diagnostic = %q; want %q", message, test.message)
				}
				if count := requests.Load(); count != 0 {
					t.Fatalf("retired inputs sent %d requests", count)
				}
			})
		}
	}
}

func TestMainModelsDeleteIsUnavailable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-private-key"}
	for _, args := range [][]string{
		{"models", "delete", "synthetic-private-model"},
		{"models", "delete", "--model", "synthetic-private-model"},
		{"models", "delete", "--help"},
		{"help", "--all", "models", "delete"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
		unknown := strings.HasPrefix(got.stderr, "Unknown command.") || strings.HasPrefix(got.stderr, "Unknown help topic.") ||
			strings.HasPrefix(got.stderr, "The --model option is not available for this command.")
		if (got.code != 1 && got.code != 3) || !unknown || got.stdout != "" || strings.Contains(got.stderr, "synthetic-private") {
			t.Errorf("models delete remains available: %+v", got)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("models delete sent a request")
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		got := runMainDispatch(t, style, mainCompletionArgs(style, "models", "")...)
		if got.code != 0 || strings.Contains(got.stdout, "delete") || !strings.Contains(got.stdout, "list") {
			t.Errorf("models completion = %+v", got)
		}
	}
}

func TestMainTrainingReferencesAreAbsentFromHelp(t *testing.T) {
	for _, route := range [][]string{
		{"files", "upload"}, {"files", "create"}, {"uploads", "create"},
		{"chat", "completions", "create"}, {"chat:completions", "create"}, {"models"},
	} {
		got := runMainDispatch(t, "bash", append([]string{"openai", "help", "--all"}, route...)...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("full help %v = %+v", route, got)
		}
		// Startup can retain the test executable in a custom usage synopsis.
		// Its temporary directory may contain the retired feature's name.
		help := strings.ReplaceAll(got.stdout, os.Args[0], "openai")
		for _, forbidden := range []string{"fine-tun", "fine_tun", "distillation", "vision fine"} {
			if strings.Contains(strings.ToLower(help), forbidden) {
				t.Errorf("help %v retains %q:\n%s", route, forbidden, help)
			}
		}
		for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
			args := append(append([]string{}, route...), "--")
			completion := runMainDispatch(t, style, mainCompletionArgs(style, args...)...)
			if completion.code != 0 || completion.stderr != "" || strings.Contains(completion.stdout, "fine-tun") || strings.Contains(completion.stdout, "distillation") {
				t.Errorf("%s flag completion %v = %+v", style, route, completion)
			}
		}
	}
}

func TestMainRetirementPreservesSharedAPIData(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("purpose") != "batch" {
				t.Errorf("query changed: %s", r.URL.RawQuery)
			}
			io.WriteString(w, `{"object":"list","data":[{"id":"file_synthetic","object":"file","purpose":"fine-tune"}],"has_more":false}`)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		purpose := r.FormValue("purpose")
		if purpose == "" {
			part, _, err := r.FormFile("purpose")
			if err != nil {
				t.Error(err)
				return
			}
			data, err := io.ReadAll(part)
			part.Close()
			if err != nil {
				t.Error(err)
			}
			purpose = string(data)
		}
		if purpose != "batch" {
			t.Errorf("purpose changed: %q", purpose)
		}
		io.WriteString(w, `{"id":"file_synthetic","object":"file","purpose":"batch"}`)
	}))
	t.Cleanup(server.Close)
	file, purpose := filepath.Join(t.TempDir(), "sample.txt"), filepath.Join(t.TempDir(), "purpose.txt")
	for path, value := range map[string]string{file: "fine-tune is arbitrary user data here", purpose: "batch"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-key"}
	for _, args := range [][]string{
		{"files", "upload", file, "--purpose", "batch"},
		{"files", "create", "--file", file, "--purpose", "@" + purpose},
		{"files", "list", "--purpose", "batch"},
	} {
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--format", "json"}, args...)...)
		if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) {
			t.Fatalf("shared route = %+v", got)
		}
		if args[1] == "list" && !strings.Contains(got.stdout, "fine-tune") {
			t.Fatal("shared API response lost historical data")
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("shared requests = %d; want 3", requests.Load())
	}
}

func retirementStdin(t *testing.T, contents string) *os.File {
	t.Helper()
	if contents == "" {
		return nil
	}
	file, err := os.CreateTemp(t.TempDir(), "stdin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	if _, err := file.WriteString(contents); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return file
}
