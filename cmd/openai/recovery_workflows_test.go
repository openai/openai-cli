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

func TestMainUnavailableOptionsExplainScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"file", []string{"models", "list", "--file=synthetic-private-value"}, "The --file option is not available for this command.\nOptions and examples: openai models list --help"},
		{"number", []string{"models", "list", "--temperature", "0.2"}, "The --temperature option is not available for this command.\nOptions and examples: openai models list --help"},
		{"parent scope", []string{"files", "--limit", "2", "list"}, "The --limit option is not available for this command.\nOptions and examples: openai files --help"},
		{"literal separator", []string{"models", "list", "--", "--file", "synthetic-private-value"}, "Unexpected extra arguments.\nOptions and examples: openai models list --help"},
		{"private unknown", []string{"models", "list", "--synthetic-private-option=synthetic-private-value"}, "An option is not recognized.\nOptions and examples: openai models list --help"},
		{"control unknown", []string{"models", "list", "--synthetic-private\x1b[31m-option=synthetic-private-value"}, "An option is not recognized.\nOptions and examples: openai models list --help"},
	} {
		for _, mode := range []struct {
			name, format string
			flags        []string
			extract      bool
		}{
			{name: "text"}, {name: "quiet", flags: []string{"--quiet"}},
			{name: "JSON", format: "json", flags: []string{"--format-error", "json"}},
			{name: "inherited JSONL", format: "jsonl", flags: []string{"--format", "jsonl"}},
			{name: "YAML", format: "yaml", flags: []string{"--format-error", "yaml"}},
			{name: "raw", format: "raw", flags: []string{"--format-error", "raw"}},
			{name: "extraction", flags: []string{"--format-error", "json", "--transform-error", "message"}, extract: true},
		} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				args := append([]string{"openai"}, mode.flags...)
				args = append(args, tc.args...)
				got := runMainDispatch(t, "bash", args...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("expected status1 and stderr only: %+v", got)
				}
				message := got.stderr
				if mode.extract {
					if err := json.Unmarshal([]byte(got.stderr), &message); err != nil {
						t.Fatal(err)
					}
				} else if mode.format != "" {
					payload := decodeMainStructuredError(t, mode.format, got.stderr)
					message, _ = payload["message"].(string)
				}
				if strings.TrimSpace(message) != tc.want {
					t.Fatalf("got %q; want %q", message, tc.want)
				}
				if strings.Contains(got.stderr, "synthetic-private") {
					t.Fatal("rejected value entered diagnostic", got.stderr)
				}
			})
		}
	}
}

func TestMainRecoveryMovesOptionToSupportedScope(t *testing.T) {
	var requests atomic.Int32
	limits := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		limits <- r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"file_synthetic","object":"file","bytes":4,"created_at":1,"filename":"fixture.txt","purpose":"user_data"}],"has_more":false}`)
	}))
	defer server.Close()
	got := runShellFileCommand(t, server, nil, nil, "files", "--limit", "2", "list")
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "The --limit option is not available") {
		t.Fatalf("wrong-scope result: %+v", got)
	}
	payload := decodeMainStructuredError(t, "json", got.stderr)
	message, _ := payload["message"].(string)
	_, help, found := strings.Cut(message, "Options and examples: ")
	if !found {
		t.Fatal(message)
	}
	// This fixture invokes the known command name, so its emitted words need no shell unquoting.
	recovery := runMainDispatch(t, "bash", strings.Fields(help)...)
	if recovery.code != 0 || recovery.stderr != "" || !strings.Contains(recovery.stdout, "list") {
		t.Fatalf("copied help failed: %+v", recovery)
	}
	if requests.Load() != 0 {
		t.Fatal("failure/help made a request")
	}
	corrected := runShellFileCommand(t, server, nil, nil, "files", "list", "--limit", "2")
	if corrected.code != 0 || corrected.stderr != "" || !json.Valid([]byte(corrected.stdout)) || !strings.Contains(corrected.stdout, "file_synthetic") {
		t.Fatalf("corrected command failed: %+v", corrected)
	}
	if requests.Load() != 1 || <-limits != "2" {
		t.Fatal("corrected command lost the option or replayed a request")
	}
}

func TestMainRecoveryRepairsMissingFile(t *testing.T) {
	uploads := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		fields := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			fields[part.FormName()] = string(data)
		}
		uploads <- fields
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"file_recovered","object":"file","bytes":19,"created_at":1,"filename":"fixture.txt","purpose":"user_data"}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "synthetic-private-file.txt")
	args := []string{"files", "upload", path, "--purpose", "user_data"}
	failed := runShellFileCommand(t, server, nil, nil, args...)
	if failed.code != 1 || failed.stdout != "" || strings.Contains(failed.stderr, path) {
		t.Fatalf("missing-file result: %+v", failed)
	}
	payload := decodeMainStructuredError(t, "json", failed.stderr)
	if payload["message"] != "Could not open the file for --file. Check the path and permissions." {
		t.Fatal(payload)
	}
	if len(uploads) != 0 {
		t.Fatal("missing file made a request")
	}
	const content = "synthetic recovery\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	recovered := runShellFileCommand(t, server, nil, nil, args...)
	if recovered.code != 0 || recovered.stderr != "" || !strings.Contains(recovered.stdout, "file_recovered") {
		t.Fatalf("file recovery failed: %+v", recovered)
	}
	fields := <-uploads
	if fields["file"] != content || fields["purpose"] != "user_data" {
		t.Fatalf("recovered upload changed bytes/options: %#v", fields)
	}
}

func TestMainRecoveryCorrectsPipedInputAndPreservesFlagLikeValues(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_recovered","object":"response","output":[]}`)
	}))
	defer server.Close()
	args := []string{"responses", "create", "--model", "synthetic-model", "--instructions=--file"}
	failed := runShellFileCommand(t, server, shellFileInput(t, []byte("input: [synthetic-private\n")), nil, args...)
	if failed.code != 1 || failed.stdout != "" || !strings.Contains(failed.stderr, "Could not parse piped input") || strings.Contains(failed.stderr, "synthetic-private") {
		t.Fatalf("piped error: %+v", failed)
	}
	if len(requests) != 0 {
		t.Fatal("malformed input made a request")
	}
	recovered := runShellFileCommand(t, server, shellFileInput(t, []byte(`{"input":"corrected synthetic input"}`)), nil, args...)
	if recovered.code != 0 || recovered.stderr != "" {
		t.Fatalf("corrected piped request: %+v", recovered)
	}
	body := <-requests
	if body["instructions"] != "--file" || body["input"] != "corrected synthetic input" {
		t.Fatalf("literal value/body changed: %#v", body)
	}
}
