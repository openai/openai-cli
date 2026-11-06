package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainParserNumericExamplesRecoverRequests(t *testing.T) {
	const rejected = "synthetic-private-invalid-number"
	examplePattern := regexp.MustCompile(`such as ([0-9]+(?:\.[0-9]+)?)\.`)
	for _, tc := range []struct {
		name, option, expectedType, example, path, method string
		args                                              []string
		response                                          string
	}{
		{
			name: "Files request limit", option: "--limit", expectedType: "an integer", example: "1",
			path: "/files", method: http.MethodGet,
			args:     []string{"files", "list", "--limit", rejected, "--purpose", "user_data", "--order", "asc"},
			response: `{"object":"list","data":[{"id":"file_recovered","object":"file","bytes":1,"created_at":1,"filename":"fixture.txt","purpose":"user_data"}],"has_more":false}`,
		},
		{
			name: "Models client limit", option: "--max-items", expectedType: "an integer", example: "1",
			path: "/models", method: http.MethodGet,
			args:     []string{"models", "list", "--max-items", rejected},
			response: `{"object":"list","data":[{"id":"model_first","object":"model","created":1,"owned_by":"synthetic"},{"id":"model_second","object":"model","created":1,"owned_by":"synthetic"}]}`,
		},
		{
			name: "Responses request number", option: "--temperature", expectedType: "a number", example: "0.5",
			path: "/responses", method: http.MethodPost,
			args:     []string{"responses", "create", "--temperature", rejected, "--model", "synthetic-model", "--input", "synthetic-private-input", "--instructions", "synthetic-private-instructions"},
			response: `{"id":"resp_recovered","object":"response","output":[]}`,
		},
	} {
		for _, mode := range []struct {
			name, format string
			flags        []string
			extract      bool
		}{
			{name: "auto", format: "auto", flags: []string{"--format-error", "auto"}},
			{name: "text", format: "text", flags: []string{"--format-error", "text"}},
			{name: "explore", format: "explore", flags: []string{"--format-error", "explore"}},
			{name: "JSON", format: "json", flags: []string{"--format-error", "json"}},
			{name: "JSONL", format: "jsonl", flags: []string{"--format-error", "jsonl"}},
			{name: "pretty", format: "pretty", flags: []string{"--format-error", "pretty"}},
			{name: "raw", format: "raw", flags: []string{"--format-error", "raw"}},
			{name: "YAML", format: "yaml", flags: []string{"--format-error", "yaml"}},
			{name: "quiet", format: "text", flags: []string{"--format-error", "text", "--quiet"}},
			{name: "inherited JSON", format: "json"},
			{name: "extraction", flags: []string{"--format-error", "json", "--transform-error", "message"}, extract: true},
		} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				type request struct {
					method, path, project, header string
					query                         url.Values
					body                          map[string]any
				}
				var requestCount atomic.Int32
				requests := make(chan request, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requestCount.Add(1)
					got := request{method: r.Method, path: r.URL.Path, query: r.URL.Query(), project: r.Header.Get("OpenAI-Project"), header: r.Header.Get("X-Recovery-Probe")}
					if r.Method == http.MethodPost {
						if err := json.NewDecoder(r.Body).Decode(&got.body); err != nil {
							t.Errorf("decode synthetic request: %v", err)
						}
					}
					select {
					case requests <- got:
					default:
						t.Error("unexpected repeated request")
					}
					w.Header().Set("Content-Type", "application/json")
					if r.Method != tc.method || r.URL.Path != tc.path {
						w.WriteHeader(http.StatusBadRequest)
					}
					if _, err := io.WriteString(w, tc.response); err != nil {
						t.Errorf("write synthetic response: %v", err)
					}
				}))
				defer server.Close()

				args := []string{"--project", "project_synthetic", "--header", "X-Recovery-Probe: retained"}
				args = append(args, mode.flags...)
				args = append(args, tc.args...)
				failed := runShellFileCommand(t, server, nil, []string{"NO_COLOR=1"}, args...)
				if failed.code != 1 || failed.stdout != "" {
					t.Fatalf("invalid numeric input must fail on stderr: %+v", failed)
				}
				if requestCount.Load() != 0 {
					t.Fatal("invalid numeric input made a request")
				}
				if strings.Contains(failed.stderr, "synthetic-private") || strings.ContainsAny(failed.stderr, "\x1b\a\r") {
					t.Fatalf("diagnostic exposed private input or controls: %q", failed.stderr)
				}
				message := strings.TrimSpace(failed.stderr)
				if mode.extract {
					if err := json.Unmarshal([]byte(failed.stderr), &message); err != nil {
						t.Fatalf("parse complete extracted message: %v", err)
					}
				} else if mode.format != "auto" && mode.format != "text" && mode.format != "pretty" {
					payload := decodeMainStructuredError(t, mode.format, failed.stderr)
					if len(payload) != 1 {
						t.Fatalf("local error invented additional fields: %#v", payload)
					}
					message, _ = payload["message"].(string)
				}
				firstLine := "Invalid value for " + tc.option + ". Expected " + tc.expectedType + ", such as " + tc.example + "."
				help := "openai " + strings.Join(tc.args[:2], " ") + " --help"
				if mode.format == "pretty" {
					// Pretty abbreviates long strings. Its displayed numeric example must remain complete.
					if !strings.Contains(message, "message: Invalid value for "+tc.option+". Expected "+tc.expectedType) {
						t.Fatalf("pretty error lost its flag or type: %q", message)
					}
				} else if want := firstLine + "\nOptions and examples: " + help; message != want {
					t.Fatalf("incomplete numeric recovery: got %q, want %q", message, want)
				}
				example := examplePattern.FindStringSubmatch(message)
				if len(example) != 2 || example[1] != tc.example {
					t.Fatalf("missing or truncated numeric example: %q", message)
				}
				if mode.format != "pretty" {
					_, copiedHelp, found := strings.Cut(message, "Options and examples: ")
					if !found || copiedHelp != help {
						t.Fatalf("wrong recovery scope: %q", message)
					}
					// This fixture uses the known executable name, so its help words need no shell unquoting.
					recovery := runShellFileCommand(t, server, nil, nil, strings.Fields(copiedHelp)[1:]...)
					if recovery.code != 0 || recovery.stderr != "" || !strings.Contains(recovery.stdout, "openai "+strings.Join(tc.args[:2], " ")) {
						t.Fatalf("copied recovery help failed: %+v", recovery)
					}
				}
				if requestCount.Load() != 0 {
					t.Fatal("recovery help made a request before the explicit retry")
				}

				// Change only the rejected value. Never replay the request automatically from the error path.
				corrected := slices.Clone(args)
				index := slices.Index(corrected, rejected)
				if index < 0 {
					t.Fatal("fixture lacks its rejected value")
				}
				corrected[index] = example[1]
				recovered := runShellFileCommand(t, server, nil, []string{"NO_COLOR=1"}, corrected...)
				if recovered.code != 0 || recovered.stderr != "" || !json.Valid([]byte(recovered.stdout)) {
					t.Fatalf("explicit numeric recovery failed: %+v", recovered)
				}
				if requestCount.Load() != 1 {
					t.Fatalf("explicit retry made %d requests", requestCount.Load())
				}
				var received request
				select {
				case received = <-requests:
				default:
					t.Fatal("explicit retry has no recorded request")
				}
				if received.method != tc.method || received.path != tc.path || received.project != "project_synthetic" || received.header != "retained" {
					t.Fatalf("retry changed request context: %#v", received)
				}
				switch tc.path {
				case "/files":
					want := url.Values{"limit": {example[1]}, "purpose": {"user_data"}, "order": {"asc"}}
					if !reflect.DeepEqual(received.query, want) || !strings.Contains(recovered.stdout, "file_recovered") {
						t.Fatalf("Files retry changed query or output: %#v, %q", received.query, recovered.stdout)
					}
				case "/models":
					if len(received.query) != 0 || !strings.Contains(recovered.stdout, "model_first") || strings.Contains(recovered.stdout, "model_second") {
						t.Fatalf("Models retry did not apply the client limit: %#v, %q", received.query, recovered.stdout)
					}
				case "/responses":
					value, err := strconv.ParseFloat(example[1], 64)
					if err != nil {
						t.Fatal(err)
					}
					want := map[string]any{"temperature": value, "model": "synthetic-model", "input": "synthetic-private-input", "instructions": "synthetic-private-instructions"}
					if !reflect.DeepEqual(received.body, want) || !strings.Contains(recovered.stdout, "resp_recovered") {
						t.Fatalf("Responses retry changed request fields or output: %#v, %q", received.body, recovered.stdout)
					}
				}
			})
		}
	}
}

func TestMainParserDurationExamplesRecoverRequests(t *testing.T) {
	const rejected = "synthetic-private-duration\x1b[31m"
	for _, flag := range []string{"--poll-interval", "--wait-timeout"} {
		for _, mode := range localRecoveryModes() {
			t.Run(flag+"/"+mode.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/batches/batch_synthetic" || r.URL.RawQuery != "" ||
						r.Header.Get("OpenAI-Project") != "project_synthetic" || r.Header.Get("X-Recovery-Probe") != "retained" {
						t.Errorf("duration retry changed request context: %s %s", r.Method, r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					if _, err := io.WriteString(w, `{"id":"batch_synthetic","object":"batch","status":"completed","request_counts":{"total":1,"completed":1,"failed":0}}`); err != nil {
						t.Errorf("write synthetic response: %v", err)
					}
				}))
				defer server.Close()
				args := append([]string{"--project", "project_synthetic", "--header", "X-Recovery-Probe: retained"}, mode.flags...)
				args = append(args, "batches", "retrieve", "batch_synthetic", "--wait", flag, rejected)
				failed := runShellFileCommand(t, server, nil, []string{"NO_COLOR=1"}, args...)
				message := localRecoveryMessage(t, failed, mode, 1)
				want := "Invalid value for " + flag + ". Expected a duration, such as 1s.\nOptions and examples: openai batches retrieve --help"
				if message != want || requests.Load() != 0 {
					t.Fatalf("duration guidance = %q, requests = %d", message, requests.Load())
				}
				_, recovery, found := strings.Cut(message, "Options and examples: ")
				if !found {
					t.Fatal("missing duration recovery command")
				}
				help := runShellFileCommand(t, server, nil, nil, strings.Fields(recovery)[1:]...)
				if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, flag) || requests.Load() != 0 {
					t.Fatalf("copied duration recovery failed: %+v, requests = %d", help, requests.Load())
				}
				_, example, found := strings.Cut(message, "such as ")
				example, _, complete := strings.Cut(example, ".\n")
				if !found || !complete || example != "1s" {
					t.Fatalf("duration example is not executable: %q", message)
				}
				// Change only the rejected value, retaining all request options.
				corrected := slices.Clone(args)
				corrected[len(corrected)-1] = example
				recovered := runShellFileCommand(t, server, nil, []string{"NO_COLOR=1"}, corrected...)
				if recovered.code != 0 || recovered.stderr != "" || !json.Valid([]byte(recovered.stdout)) || requests.Load() != 1 {
					t.Fatalf("duration retry failed: %+v, requests = %d", recovered, requests.Load())
				}
				if flag == "--wait-timeout" {
					// Zero disables this deadline and remains valid parser syntax.
					corrected[len(corrected)-1] = "0"
					unlimited := runShellFileCommand(t, server, nil, []string{"NO_COLOR=1"}, corrected...)
					if unlimited.code != 0 || unlimited.stderr != "" || !json.Valid([]byte(unlimited.stdout)) || requests.Load() != 2 {
						t.Fatalf("zero timeout changed: %+v, requests = %d", unlimited, requests.Load())
					}
				}
			})
		}
	}
}
