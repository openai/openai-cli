package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

const storedExportFirst = `{"id":"chatcmpl_export_first","object":"chat.completion","created":1700000000,"model":"synthetic-model","choices":[{"index":0,"message":{"role":"assistant","content":"line one\nline two ☀","refusal":null,"tool_calls":[{"id":"call_synthetic","type":"function","function":{"name":"example","arguments":"{}"}}]},"finish_reason":"tool_calls","future_choice":{"kept":true}}],"metadata":{"tag":"synthetic","empty":""},"usage":{"total_tokens":9007199254740993},"future":{"precise":1.234567890123456789,"nullable":null,"array":[false,42]}}`
const storedExportSecond = `{"id":"chatcmpl_export_second","object":"chat.completion","created":1700000001,"model":"synthetic-model","choices":[],"metadata":{"tag":"second"},"unknown":"preserved"}`

func storedExportPage(records []string, more bool, last string) string {
	return fmt.Sprintf(`{"object":"list","data":[%s],"has_more":%t,"last_id":%q}`, strings.Join(records, ","), more, last)
}

func storedExportEnv(t *testing.T, server *httptest.Server, extra ...string) []string {
	t.Helper()
	home := t.TempDir()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_API_KEY=sk-fake-stored-export", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	return append(env, extra...)
}

func storedExportRun(t *testing.T, server *httptest.Server, input *os.File, extraEnv []string, args ...string) mainDispatchResult {
	t.Helper()
	return runMainDispatchWithStdin(t, "bash", storedExportEnv(t, server, extraEnv...), input, append([]string{"openai"}, args...)...)
}

func assertStoredExportNoStages(t *testing.T, directory string) {
	t.Helper()
	stages, err := filepath.Glob(filepath.Join(directory, ".openai-download-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, stages, "export left an owned temporary file")
}

func TestMainStoredCompletionExportRoutesAndRecords(t *testing.T) {
	for _, route := range [][]string{{"chat", "completions"}, {"chat:completions"}} {
		for _, destination := range []string{"stdout", "file"} {
			t.Run(strings.Join(route, "/")+"/"+destination, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					page := requests.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/chat/completions" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					if body, err := io.ReadAll(r.Body); err != nil || len(body) != 0 {
						t.Errorf("GET body=%q, error=%v", body, err)
					}
					w.Header().Set("Content-Type", "application/json")
					switch page {
					case 1:
						if r.URL.Query().Get("after") != "" {
							t.Error("first page unexpectedly set a cursor")
						}
						var pretty bytes.Buffer
						if err := json.Indent(&pretty, []byte(storedExportPage([]string{storedExportFirst}, true, "chatcmpl_export_first")), "", "  "); err != nil {
							t.Error(err)
						}
						_, _ = w.Write(pretty.Bytes())
					case 2:
						if r.URL.Query().Get("after") != "chatcmpl_export_first" {
							t.Errorf("second cursor=%q", r.URL.Query().Get("after"))
						}
						_, _ = io.WriteString(w, storedExportPage([]string{storedExportSecond}, false, "chatcmpl_export_second"))
					default:
						t.Error("export fetched an extra page or input messages")
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				defer server.Close()
				path := "-"
				directory := t.TempDir()
				if destination == "file" {
					path = filepath.Join(directory, "stored completions.jsonl")
				}
				args := append(append([]string{}, route...), "export", "--output", path)
				got := storedExportRun(t, server, nil, nil, args...)
				require.Zero(t, got.code, "%+v", got)
				require.EqualValues(t, 2, requests.Load())
				wantData := storedExportFirst + "\n" + storedExportSecond + "\n"
				wantReceipt := "Stored completions: 2\nAll pages fetched.\n"
				if destination == "file" {
					require.Empty(t, got.stdout)
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, wantData, string(data))
					wantReceipt = "Saved " + path + "\n" + wantReceipt
				} else {
					require.Equal(t, wantData, got.stdout)
				}
				require.Equal(t, wantReceipt, got.stderr)
				assertStoredExportNoStages(t, directory)
			})
		}
	}
}

func TestMainStoredCompletionExportRequestConfiguration(t *testing.T) {
	for _, override := range []string{"environment", "explicit", "headers"} {
		t.Run(override, func(t *testing.T) {
			var requests atomic.Int32
			wantAuth, wantOrg, wantProject, wantCustom := "Bearer sk-fake-stored-export", "org-env", "proj-env", "env-last"
			if override != "environment" {
				wantAuth, wantOrg, wantProject = "Bearer sk-fake-explicit-export", "org-explicit", "proj-explicit"
			}
			if override == "headers" {
				wantAuth, wantOrg, wantProject, wantCustom = "Bearer sk-fake-header-export", "org-header", "proj-header", "flag-last"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := requests.Add(1)
				for name, want := range map[string]string{
					"Authorization": wantAuth, "OpenAI-Organization": wantOrg, "OpenAI-Project": wantProject,
					"X-Synthetic-Custom": wantCustom, "X-Synthetic-Only": "env-only",
				} {
					if values := r.Header.Values(name); len(values) != 1 || values[0] != want {
						t.Errorf("page %d changed %s precedence", page, name)
					}
				}
				for name, want := range map[string]string{"limit": "1", "order": "desc", "model": "synthetic-model", "metadata[tag]": "synthetic value"} {
					if got := r.URL.Query().Get(name); got != want {
						t.Errorf("page %d: %s=%q, want %q", page, name, got, want)
					}
				}
				wantAfter := "initial_cursor"
				if page == 2 {
					wantAfter = "chatcmpl_export_first"
				}
				if r.URL.Path != "/custom/v1/chat/completions" || r.URL.Query().Get("after") != wantAfter {
					t.Errorf("page %d has path=%q cursor=%q", page, r.URL.Path, r.URL.Query().Get("after"))
				}
				w.Header().Set("Content-Type", "application/json")
				if page == 1 {
					_, _ = io.WriteString(w, storedExportPage([]string{storedExportFirst}, true, "chatcmpl_export_first"))
				} else {
					_, _ = io.WriteString(w, storedExportPage(nil, false, ""))
				}
			}))
			defer server.Close()
			args := []string{"--base-url", server.URL + "/custom/v1", "chat", "completions", "export", "--output", "-", "--quiet",
				"--after", "initial_cursor", "--limit", "1", "--order", "desc", "--model", "synthetic-model", "--metadata", `{"tag":"synthetic value"}`}
			if override != "environment" {
				args = append(args, "--api-key", "sk-fake-explicit-export", "--organization", "org-explicit", "--project", "proj-explicit")
			}
			if override == "headers" {
				args = append(args, "--header", "X-Synthetic-Custom: flag-first", "-H", "X-Synthetic-Custom: flag-last",
					"--header", "Authorization: Bearer sk-fake-header-export", "--header", "OpenAI-Organization: org-header", "-H", "OpenAI-Project: proj-header")
			}
			got := storedExportRun(t, server, nil, []string{"OPENAI_BASE_URL=not%url", "OPENAI_ORG_ID=org-env", "OPENAI_PROJECT_ID=proj-env",
				"OPENAI_CUSTOM_HEADERS=X-Synthetic-Custom: env-first\nx-synthetic-custom: env-last\nX-Synthetic-Only: env-only"}, args...)
			require.Equal(t, mainDispatchResult{0, storedExportFirst + "\n", ""}, got)
			require.EqualValues(t, 2, requests.Load())
		})
	}
}

func TestMainStoredCompletionExportFilterInputs(t *testing.T) {
	modelPath := filepath.Join(t.TempDir(), "model.txt")
	require.NoError(t, os.WriteFile(modelPath, []byte("synthetic-file-model"), 0o600))
	for _, tc := range []struct {
		name, input, model string
		flags              []string
	}{
		{"JSON", `{"model":"synthetic-input-model","limit":1,"order":"desc","metadata":{"tag":"input"}}`, "synthetic-input-model", nil},
		{"YAML", "model: synthetic-input-model\nlimit: 1\norder: desc\nmetadata:\n  tag: input\n", "synthetic-input-model", nil},
		{"flag overrides JSON", `{"model":"synthetic-input-model","limit":1,"order":"desc","metadata":{"tag":"input"}}`, "synthetic-flag-model", []string{"--model", "synthetic-flag-model"}},
		{"file value", "", "synthetic-file-model", []string{"--model", "@" + modelPath, "--limit", "1", "--order", "desc", "--metadata", `{"tag":"input"}`}},
		{"explicit stdin value", "synthetic-input-model", "synthetic-input-model", []string{"--model", "@-", "--limit", "1", "--order", "desc", "--metadata", `{"tag":"input"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				for name, want := range map[string]string{"model": tc.model, "limit": "1", "order": "desc", "metadata[tag]": "input"} {
					if got := r.URL.Query().Get(name); got != want {
						t.Errorf("%s=%q, want %q", name, got, want)
					}
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Errorf("query input leaked into the GET body: %q, %v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, storedExportPage(nil, false, ""))
			}))
			defer server.Close()
			var input *os.File
			if tc.input != "" {
				input = shellFileInput(t, []byte(tc.input))
			}
			args := append([]string{"chat", "completions", "export", "--output", "-", "--quiet"}, tc.flags...)
			got := storedExportRun(t, server, input, nil, args...)
			require.Equal(t, mainDispatchResult{0, "", ""}, got)
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestMainStoredCompletionExportPreservesListFilterPresence(t *testing.T) {
	for _, flags := range [][]string{nil, {"--limit", "0"}, {"--limit", "-1"}, {"--model="}, {"--metadata", "null"}} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			queries := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				queries <- r.URL.Query().Encode()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, storedExportPage(nil, false, ""))
			}))
			defer server.Close()
			for _, operation := range []string{"list", "export"} {
				args := []string{"chat:completions", operation, "--format", "jsonl"}
				if operation == "export" {
					args = append(args, "--output", "-")
				}
				got := storedExportRun(t, server, nil, nil, append(args, flags...)...)
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
			}
			require.Len(t, queries, 2)
			require.Equal(t, <-queries, <-queries, "export changed the existing list filter contract")
		})
	}
}

func TestMainStoredCompletionExportValidation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, input string
		args        []string
	}{
		{"missing output", "", nil},
		{"empty output", "", []string{"--output="}},
		{"extra argument", "", []string{"--output", "-", "extra"}},
		{"max items", "", []string{"--output", "-", "--max-items", "-1"}},
		{"transform", "", []string{"--output", "-", "--transform", "id"}},
		{"raw output", "", []string{"--output", "-", "--raw-output"}},
		{"invalid header", "", []string{"--output", "-", "--header", "synthetic-private-header"}},
		{"malformed JSON", `{"model":`, []string{"--output", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input *os.File
			if tc.input != "" {
				input = shellFileInput(t, []byte(tc.input))
			}
			args := append([]string{"chat", "completions", "export"}, tc.args...)
			got := storedExportRun(t, server, input, nil, args...)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.NotEmpty(t, got.stderr)
			require.NotContains(t, got.stderr, "All pages fetched.")
			require.Zero(t, requests.Load(), "validation must precede requests")
		})
	}
	for _, format := range []string{"text", "json", "yaml", "raw"} {
		t.Run("format/"+format, func(t *testing.T) {
			got := storedExportRun(t, server, nil, nil, "chat:completions", "export", "--output", "-", "--format", format)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.NotEmpty(t, got.stderr)
			require.Zero(t, requests.Load())
		})
	}
}

func TestMainStoredCompletionExportReceiptPolicyAndEmptyFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   []string
		receipt bool
	}{
		{"readable", nil, true},
		{"explicit auto", []string{"--format", "auto"}, true},
		{"quiet", []string{"--quiet"}, false},
		{"JSONL", []string{"--format", "jsonl"}, false},
		{"JSON error", []string{"--format-error", "json"}, false},
		{"JSONL readable override", []string{"--format", "jsonl", "--format-error", "text"}, true},
	} {
		for _, output := range []string{"file", "stdout"} {
			t.Run(tc.name+"/"+output, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, storedExportPage(nil, false, ""))
				}))
				defer server.Close()
				directory, path := t.TempDir(), "-"
				if output == "file" {
					path = filepath.Join(directory, "empty.jsonl")
				}
				args := append([]string{"chat", "completions", "export", "--output", path}, tc.flags...)
				got := storedExportRun(t, server, nil, nil, args...)
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				if output == "file" {
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Empty(t, data)
				}
				if tc.receipt {
					want := "Stored completions: 0\nAll pages fetched.\n"
					if output == "file" {
						want = "Saved " + path + "\n" + want
					}
					require.Equal(t, want, got.stderr)
				} else {
					require.Empty(t, got.stderr)
				}
				assertStoredExportNoStages(t, directory)
			})
		}
	}
}

func TestMainStoredCompletionExportPreservesExistingDestinations(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path, target := filepath.Join(directory, "export.jsonl"), filepath.Join(directory, "target")
			switch kind {
			case "file":
				require.NoError(t, os.WriteFile(path, []byte("keep original"), 0o600))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0o700))
			default:
				if kind == "symlink" {
					require.NoError(t, os.WriteFile(target, []byte("keep original"), 0o600))
				}
				if err := os.Symlink(target, path); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlink creation unavailable: %v", err)
					}
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, storedExportPage(nil, false, ""))
			}))
			defer server.Close()
			got := storedExportRun(t, server, nil, nil, "chat", "completions", "export", "--output", path)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.Zero(t, requests.Load())
			after, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after), "export replaced an existing destination")
			if kind == "file" || kind == "symlink" {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, "keep original", string(data))
			}
			if kind == "dangling symlink" {
				_, err := os.Lstat(target)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			assertStoredExportNoStages(t, directory)
		})
	}
}

func TestMainStoredCompletionExportPublishCollision(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "export.jsonl")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := os.WriteFile(path, []byte("concurrent owner"), 0o600); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, storedExportPage([]string{storedExportFirst}, false, "chatcmpl_export_first"))
	}))
	defer server.Close()
	got := storedExportRun(t, server, nil, nil, "chat", "completions", "export", "--output", path)
	require.Equal(t, 1, got.code, "%+v", got)
	require.Empty(t, got.stdout)
	require.NotContains(t, got.stderr, "All pages fetched.")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "concurrent owner", string(data))
	assertStoredExportNoStages(t, directory)
}

func TestMainStoredCompletionExportReceiptPathControls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain these controls")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, storedExportPage(nil, false, ""))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "receipt\nStored completions: 999\nAll pages fetched.\t\x1b[31m.jsonl")
	got := storedExportRun(t, server, nil, nil, "chat", "completions", "export", "--output", path)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stdout)
	require.Equal(t, 3, strings.Count(got.stderr, "\n"), "path controls injected receipt rows")
	require.NotContains(t, got.stderr, "\x1b")
	require.Contains(t, got.stderr, `\nStored completions: 999\n`)
	require.True(t, strings.HasSuffix(got.stderr, "\nStored completions: 0\nAll pages fetched.\n"))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestMainStoredCompletionExportLatePageFailure(t *testing.T) {
	for _, failure := range []string{"API", "malformed JSON", "missing data", "invalid record"} {
		for _, output := range []string{"file", "stdout"} {
			t.Run(failure+"/"+output, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					page := requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if page == 1 {
						_, _ = io.WriteString(w, storedExportPage([]string{storedExportFirst}, true, "chatcmpl_export_first"))
						return
					}
					switch failure {
					case "API":
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic-private-api-message","type":"permission_error","code":"permission_denied","param":null}}`)
					case "malformed JSON":
						_, _ = io.WriteString(w, `{"object":"list","data":[`)
					case "missing data":
						_, _ = io.WriteString(w, `{"object":"list","has_more":false}`)
					case "invalid record":
						_, _ = io.WriteString(w, `{"object":"list","data":[null],"has_more":false}`)
					}
				}))
				defer server.Close()
				directory, path := t.TempDir(), "-"
				if output == "file" {
					path = filepath.Join(directory, "export.jsonl")
				}
				got := storedExportRun(t, server, nil, nil, "chat:completions", "export", "--output", path)
				require.Equal(t, 1, got.code, "%+v", got)
				require.EqualValues(t, 2, requests.Load())
				require.NotEmpty(t, got.stderr)
				require.NotContains(t, got.stderr, "All pages fetched.")
				require.NotContains(t, got.stderr, "synthetic-private-api-message")
				if failure == "API" {
					require.Contains(t, got.stderr, "HTTP 403")
				}
				if output == "file" {
					require.Empty(t, got.stdout)
					require.Contains(t, got.stderr, "No destination file was created.")
					require.Contains(t, got.stderr, "Complete records processed: 1.")
					_, err := os.Lstat(path)
					require.ErrorIs(t, err, os.ErrNotExist)
				} else {
					require.Equal(t, storedExportFirst+"\n", got.stdout)
					require.Contains(t, got.stderr, "Export incomplete after 1 complete records.")
				}
				assertStoredExportNoStages(t, directory)
			})
		}
	}
}

func TestMainStoredCompletionExportClosedStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("closed pipe behavior requires native Windows validation")
	}
	for _, failure := range []string{"write", "upstream API"} {
		t.Run(failure, func(t *testing.T) {
			release := make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if requests.Add(1) == 1 {
					_, _ = io.WriteString(w, storedExportPage([]string{storedExportFirst}, true, "chatcmpl_export_first"))
					return
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if failure == "upstream API" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic-api-error","type":"permission_error","code":"permission_denied"}}`)
				} else {
					_, _ = io.WriteString(w, storedExportPage([]string{storedExportSecond}, false, "chatcmpl_export_second"))
				}
			}))
			defer server.Close()
			child, stdout, stderr, ctx := startStreamingTextCommand(t, server, "chat", "completions", "export", "--output", "-")
			readStreamingTextPrefix(t, ctx, stdout, storedExportFirst+"\n")
			require.NoError(t, stdout.Close())
			close(release)
			err := child.Wait()
			require.NoError(t, ctx.Err())
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.Equal(t, 1, exit.ExitCode(), "stderr=%q", stderr.String())
			require.Contains(t, stderr.String(), "Export incomplete after 1 complete records.")
			require.NotContains(t, stderr.String(), "All pages fetched.")
			if failure == "upstream API" {
				require.Contains(t, stderr.String(), "HTTP 403")
			}
		})
	}
}

func TestMainStoredCompletionExportStructuredError(t *testing.T) {
	const apiError = `{"message":"synthetic-api-error","type":"permission_error","code":"permission_denied","param":null,"future":9007199254740993}`
	for _, format := range []string{"json", "jsonl", "yaml"} {
		t.Run(format, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":`+apiError+`}`)
			}))
			defer server.Close()
			got := storedExportRun(t, server, nil, nil, "chat", "completions", "export", "--output", "-", "--format-error", format)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.Equal(t, decodeMainErrorObject(t, "json", apiError), decodeMainErrorObject(t, format, got.stderr))
		})
	}
}

func TestMainStoredCompletionExportPreservesRepeatedTerminalRecord(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, storedExportPage([]string{storedExportFirst}, requests.Add(1) == 1, "chatcmpl_export_first"))
	}))
	defer server.Close()
	got := storedExportRun(t, server, nil, nil, "chat", "completions", "export", "--output", "-", "--format", "JSONL")
	require.Equal(t, mainDispatchResult{0, storedExportFirst + "\n" + storedExportFirst + "\n", ""}, got)
	require.EqualValues(t, 2, requests.Load())
}

func TestMainStoredCompletionExportCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal does not support these signals on Windows")
	}
	for _, signal := range []struct {
		name  string
		value os.Signal
		code  int
	}{{"SIGINT", os.Interrupt, 130}, {"SIGTERM", syscall.SIGTERM, 143}} {
		for _, output := range []string{"file", "stdout"} {
			t.Run(signal.name+"/"+output, func(t *testing.T) {
				ready, stopped, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if requests.Add(1) == 1 {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, storedExportPage([]string{storedExportFirst}, true, "chatcmpl_export_first"))
						return
					}
					close(ready)
					select {
					case <-r.Context().Done():
						close(stopped)
					case <-release:
					}
				}))
				defer server.Close()
				defer close(release)
				directory, path := t.TempDir(), "-"
				if output == "file" {
					path = filepath.Join(directory, "canceled.jsonl")
				}
				child, stdout, stderr, ctx := startStreamingTextCommand(t, server, "chat", "completions", "export", "--output", path)
				if output == "stdout" {
					// Drain page one before awaiting page two; a small pipe can block its final write.
					readStreamingTextPrefix(t, ctx, stdout, storedExportFirst+"\n")
				}
				select {
				case <-ready:
				case <-ctx.Done():
					t.Fatal("export did not request its second page")
				}
				require.NoError(t, child.Process.Signal(signal.value))
				rest, readErr := io.ReadAll(stdout)
				require.NoError(t, readErr)
				err := child.Wait()
				require.NoError(t, ctx.Err(), "cancellation timed out")
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				require.Equal(t, signal.code, exit.ExitCode(), "stderr=%q", stderr.String())
				require.Empty(t, rest)
				require.NotContains(t, stderr.String(), "All pages fetched.")
				select {
				case <-stopped:
				case <-ctx.Done():
					t.Fatal("export left the HTTP request open")
				}
				if output == "file" {
					_, err := os.Lstat(path)
					require.ErrorIs(t, err, os.ErrNotExist)
				}
				assertStoredExportNoStages(t, directory)
			})
		}
	}
}

func TestMainStoredCompletionExportHelpAndListCompatibility(t *testing.T) {
	for _, route := range [][]string{{"chat", "completions"}, {"chat:completions"}} {
		args := append(append([]string{"openai"}, route...), "export", "--help")
		got := runMainDispatch(t, "bash", args...)
		require.Zero(t, got.code, "%+v", got)
		require.Empty(t, got.stderr)
		for _, flag := range []string{"--output", "--after", "--limit", "--model", "--metadata", "--order"} {
			require.Contains(t, got.stdout, flag)
		}
		require.Contains(t, strings.ToLower(got.stdout), "jsonl")
		require.NotContains(t, got.stdout, "--max-items")
	}
	for _, flags := range [][]string{{"--format", "jsonl", "--max-items", "-1"}, {"--format", "jsonl", "--max-items", "-1", "--transform", "id"}} {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			page := requests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if page == 1 {
				_, _ = io.WriteString(w, storedExportPage([]string{storedExportFirst}, true, "chatcmpl_export_first"))
			} else {
				_, _ = io.WriteString(w, storedExportPage([]string{storedExportSecond}, false, "chatcmpl_export_second"))
			}
		}))
		args := append([]string{"chat", "completions", "list"}, flags...)
		got := storedExportRun(t, server, nil, nil, args...)
		server.Close()
		require.Zero(t, got.code, "%+v", got)
		require.Empty(t, got.stderr)
		require.EqualValues(t, 2, requests.Load())
		require.Contains(t, got.stdout, "chatcmpl_export_first")
		require.Contains(t, got.stdout, "chatcmpl_export_second")
	}
}
