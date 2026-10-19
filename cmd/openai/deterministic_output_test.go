package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainDispatchOutputPipedFormatsIgnoreForcedDecoration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, readableModelResponse)
	}))
	defer server.Close()
	for _, format := range []string{"json", "jsonl", "raw", "yaml", "pretty", "text", "explore"} {
		for _, forced := range []string{"FORCE_COLOR=1", "CLICOLOR_FORCE=1"} {
			t.Run(format+"/"+forced, func(t *testing.T) {
				got := runMainDispatchWithEnv(t, "bash", []string{
					"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-output-test", forced,
				}, "openai", "--format", format, "models", "retrieve", "model_synthetic")
				if got.code != 0 || strings.Contains(got.stdout, "\x1b") {
					t.Fatalf("piped result changed or contains decoration: %+v", got)
				}
				if format == "json" || format == "jsonl" || format == "raw" || format == "explore" {
					var data map[string]any
					if err := json.Unmarshal([]byte(got.stdout), &data); err != nil || data["id"] != "model_synthetic" {
						t.Fatalf("invalid selected JSON: %v; %q", err, got.stdout)
					}
				} else if !strings.Contains(got.stdout, "model_synthetic") {
					t.Fatalf("selected data disappeared: %q", got.stdout)
				}
			})
		}
	}
}

func TestMainDispatchOutputQuietPreservesSelectedPayloads(t *testing.T) {
	const binary = "synthetic\x00\xff\x1b[31m\r\nbytes"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/models/model_synthetic":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, readableModelResponse)
		case "/files":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"file_a","object":"file"},{"id":"file_b","object":"file"}],"has_more":false}`)
		case "/files/file_synthetic/content":
			w.Header().Set("Content-Type", "application/octet-stream")
			io.WriteString(w, binary)
		case "/containers/cntr_synthetic":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "synthetic unexpected route", http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"models", "retrieve", "model_synthetic"},
		{"--format", "json", "models", "retrieve", "model_synthetic"},
		{"--format", "jsonl", "files", "list"},
		{"--format", "yaml", "files", "list"},
		{"--format", "raw", "files", "list"},
		{"--format", "pretty", "models", "retrieve", "model_synthetic"},
		{"--transform", "extra.note", "--raw-output", "models", "retrieve", "model_synthetic"},
		{"files", "content", "file_synthetic", "--output", "-"},
		{"containers", "delete", "cntr_synthetic"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			before := requests.Load()
			ordinary := runReadableCommand(t, server, args...)
			quiet := runReadableCommand(t, server, append([]string{"--quiet"}, args...)...)
			if ordinary.code != 0 || quiet.code != ordinary.code || quiet.stdout != ordinary.stdout || quiet.stderr != "" {
				t.Fatalf("quiet changed selected data: ordinary=%+v quiet=%+v", ordinary, quiet)
			}
			if requests.Load()-before != 2 {
				t.Fatalf("quiet changed request count: %d", requests.Load()-before)
			}
		})
	}
}

func TestMainDispatchOutputVerboseKeepsDataAndProtectsMachineErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "model_failure") {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"synthetic server detail","type":"invalid_request_error"}}`)
			return
		}
		io.WriteString(w, readableModelResponse)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, model string
		flags       []string
		verbose     bool
		failure     bool
	}{
		{"human success", "model_synthetic", nil, true, false},
		{"machine stdout text stderr", "model_synthetic", []string{"--format", "json", "--format-error", "text"}, true, false},
		{"machine success", "model_synthetic", []string{"--format", "json"}, false, false},
		{"machine failure", "model_failure", []string{"--format", "json"}, false, true},
		{"explicit machine error", "model_failure", []string{"--format-error", "json"}, false, true},
		{"human failure", "model_failure", nil, true, true},
		{"quiet takes precedence", "model_synthetic", []string{"--quiet"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--verbose", "--organization", "synthetic-private-organization"}, tc.flags...)
			args = append(args, "models", "retrieve", tc.model)
			got := runReadableCommand(t, server, args...)
			if (got.code != 0) != tc.failure || strings.Contains(got.stdout, "Command result:") ||
				strings.Contains(got.stderr, "Command result:") != tc.verbose || strings.Contains(got.stderr, "synthetic-private-organization") {
				t.Fatalf("verbose policy changed data, privacy, or failure status: %+v", got)
			}
			if tc.verbose {
				removeVerboseElapsed(t, got.stderr)
			} else if strings.Contains(got.stderr, "Elapsed:") {
				t.Fatalf("suppressed verbose timing reached stderr: %+v", got)
			}
			if tc.failure && !tc.verbose {
				var detail map[string]any
				if json.Unmarshal([]byte(got.stderr), &detail) != nil || got.stdout != "" {
					t.Fatalf("verbose corrupted machine error framing: %+v", got)
				}
			}
		})
	}
}

func TestMainDispatchOutputQuietKeepsExplicitHelpVersionAndCompletion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"models", "retrieve", "--help"}, {"--version"}, {"-v"}, {"@completion", "bash"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ordinary := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
			if ordinary.code != 0 || ordinary.stdout == "" || ordinary.stderr != "" {
				t.Fatalf("explicit help/protocol failed: %+v", ordinary)
			}
			for _, flag := range []string{"--quiet", "--verbose"} {
				t.Run(flag, func(t *testing.T) {
					got := runMainDispatch(t, "bash", append([]string{"openai", flag}, args...)...)
					if got != ordinary {
						t.Fatalf("%s changed explicit help/protocol: ordinary=%+v got=%+v", flag, ordinary, got)
					}
				})
			}
		})
	}
}

func TestMainDispatchOutputHelpGroupsLongFlagsAndPreservesVersion(t *testing.T) {
	t.Run("Output group", func(t *testing.T) {
		got := runMainDispatch(t, "bash", "openai", "--help")
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("root help failed: %+v", got)
		}
		_, globals, ok := strings.Cut(got.stdout, "\nGLOBAL OPTIONS:")
		if !ok {
			t.Fatalf("root help lost global options: %q", got.stdout)
		}
		_, outputAndRest, ok := strings.Cut(globals, "\n   Output\n")
		if !ok {
			t.Fatalf("root help lost the Output group: %q", globals)
		}
		output, _, ok := strings.Cut(outputAndRest, "\n\n   Request options\n")
		if !ok {
			t.Fatalf("Output group boundary is missing: %q", outputAndRest)
		}
		for _, name := range []string{"--quiet", "--verbose"} {
			// A complete definition line also rejects short aliases or value labels.
			definition := "\n   " + name + "\n"
			if strings.Count(got.stdout, definition) != 1 || strings.Count(output, definition) != 1 {
				t.Errorf("%s must appear once, long-only, inside Output: %q", name, got.stdout)
			}
		}
	})
	t.Run("version alias", func(t *testing.T) {
		version := runMainDispatch(t, "bash", "openai", "--version")
		short := runMainDispatch(t, "bash", "openai", "-v")
		if version.code != 0 || version.stdout == "" || version.stderr != "" || short != version {
			t.Fatalf("-v must preserve --version: version=%+v short=%+v", version, short)
		}
	})
}

func TestMainDispatchOutputVerboseBinaryKeepsBytesAndLabelsFormatOption(t *testing.T) {
	const payload = "synthetic-private-binary\x00\xff\x1b[31m\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, payload)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "--verbose", "files", "content", "file_synthetic", "--output", "-")
	details, _ := removeVerboseElapsed(t, got.stderr)
	if got.code != 0 || got.stdout != payload || details != "Command: files content\nFormat option: auto\nCommand result: completed\n" {
		t.Fatalf("verbose changed binary data or reported its bytes as text: %+v", got)
	}
}

func TestMainDispatchOutputVerboseElapsedIncludesCommandWork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, readableModelResponse)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "--verbose", "--format", "raw", "--format-error", "text", "models", "retrieve", "model_synthetic")
	details, elapsed := removeVerboseElapsed(t, got.stderr)
	if got.code != 0 || got.stdout != readableModelResponse+"\n" ||
		details != "Command: models retrieve\nFormat option: raw\nCommand result: completed\n" || requests.Load() != 1 {
		t.Fatalf("timing changed data, diagnostics, requests, or status: %+v; requests=%d", got, requests.Load())
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("elapsed time omitted delayed command work: %s", elapsed)
	}
}

func removeVerboseElapsed(t *testing.T, details string) (string, time.Duration) {
	t.Helper()
	var remaining strings.Builder
	var elapsed time.Duration
	count := 0
	for line := range strings.SplitAfterSeq(details, "\n") {
		if value, ok := strings.CutPrefix(line, "Elapsed: "); ok {
			count++
			var err error
			elapsed, err = time.ParseDuration(strings.TrimSuffix(value, "\n"))
			if err != nil || elapsed < 0 || elapsed%time.Millisecond != 0 {
				t.Fatalf("invalid rounded elapsed time in stderr: %q", line)
			}
		} else {
			remaining.WriteString(line)
		}
	}
	if count != 1 {
		t.Fatalf("stderr must contain one elapsed time, got %d: %q", count, details)
	}
	return remaining.String(), elapsed
}

func TestMainDispatchOutputQuietPreservesSavedImagePaths(t *testing.T) {
	payload := imageGenerationPNG(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, imageGenerationResponse(payload))
	}))
	defer server.Close()
	directory := t.TempDir()
	got := runImageGeneration(t, server, t.TempDir(), "", "--quiet", "images", "generate", "--prompt", "synthetic fixture", "--output-dir", directory)
	if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
		t.Fatalf("quiet image result changed: %+v, requests=%d", got, requests.Load())
	}
	assertImageGenerationFiles(t, directory, got.stdout, 1, payload)
}

func TestMainDispatchOutputQuietKeepsFailurePayloadsAndSuppressesFallbackNotice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"synthetic rejection","type":"invalid_request_error"}}`)
	}))
	defer server.Close()
	for _, format := range []string{"json", "jsonl", "raw", "explore"} {
		t.Run(format, func(t *testing.T) {
			got := runReadableCommand(t, server, "--quiet", "--format-error", format, "models", "retrieve", "model_synthetic")
			var failure map[string]any
			if got.code != 1 || got.stdout != "" || json.Unmarshal([]byte(got.stderr), &failure) != nil || failure["message"] != "synthetic rejection" {
				t.Fatalf("quiet hid or decorated the failure: %+v", got)
			}
		})
	}
}

func TestMainDispatchOutputRedirectedErrorsIgnoreForcedDecoration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"synthetic failure","type":"invalid_request_error"}}`)
	}))
	defer server.Close()
	for _, format := range []string{"json", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-output-test", "FORCE_COLOR=1",
			}, "openai", "--format-error", format, "models", "retrieve", "model_synthetic")
			var data map[string]any
			if got.code != 1 || got.stdout != "" || json.Unmarshal([]byte(got.stderr), &data) != nil {
				t.Fatalf("error format or failure status changed: %+v", got)
			}
		})
	}
}
