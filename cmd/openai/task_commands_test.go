package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func taskRouteArgs(route string, flags ...string) []string {
	return append(strings.Fields(route), flags...)
}

func TestMainTaskRouteHelpStaysLocal(t *testing.T) {
	env := []string{"OPENAI_BASE_URL=invalid-task-help-url", "OPENAI_CUSTOM_HEADERS=invalid-task-help-headers"}
	for _, tc := range []struct{ route, flag string }{
		{"audio transcribe", "--file"}, {"audio translate", "--file"}, {"audio speak", "--voice"},
		{"transcribe", "--file"}, {"translate", "--file"}, {"speak", "--voice"},
		{"files upload", "--purpose"}, {"admin projects list", "--limit"}, {"admin audit-logs list", "--limit"},
		{"projects users roles list", "--user-id"},
		{"audio:transcriptions create", "--file"}, {"audio translations create", "--file"},
		{"audio speech create", "--voice"}, {"files create", "--purpose"},
		{"admin organization projects users roles list", "--user-id"}, {"admin:organization:audit-logs list", "--limit"},
	} {
		t.Run(tc.route, func(t *testing.T) {
			for _, full := range []bool{false, true} {
				args := append([]string{"openai"}, taskRouteArgs(tc.route, "--help")...)
				if full {
					args = append([]string{"openai", "help", "--all"}, strings.Fields(tc.route)...)
				}
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				require.Zero(t, got.code, "full=%v: %s", full, got.stderr)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "openai "+tc.route)
				require.Contains(t, got.stdout, tc.flag)
			}
		})
	}
}

func TestMainTaskRouteCompletionProtocols(t *testing.T) {
	env := []string{"OPENAI_BASE_URL=invalid-task-completion-url", "OPENAI_CLI_COMPLETION_FILE_VALUES=1"}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(style, func(t *testing.T) {
			for _, tc := range []struct {
				route, prefix string
				want          []string
			}{
				{"", "tra", []string{"transcribe", "translate"}},
				{"", "spe", []string{"speak"}},
				{"audio", "tra", []string{"transcribe", "translate"}},
				{"audio", "spe", []string{"speak"}},
				{"files", "up", []string{"upload"}},
				{"admin", "proj", []string{"projects"}},
				{"", "proj", []string{"projects"}},
				{"transcribe", "--fi", []string{"--file"}},
				{"translate", "--fi", []string{"--file"}},
				{"speak", "--voi", []string{"--voice"}},
				{"files upload", "--fi", []string{"--file"}},
				{"audio:transcriptions create", "--fi", []string{"--file"}},
				{"audio translations create", "--fi", []string{"--file"}},
				{"audio speech create", "--voi", []string{"--voice"}},
				{"files create", "--fi", []string{"--file"}},
				{"projects users roles list", "--user-i", []string{"--user-id"}},
				{"admin projects retrieve", "--project-i", []string{"--project-id"}},
				{"admin organization projects retrieve", "--project-i", []string{"--project-id"}},
				{"admin:organization:projects:users:roles list", "--user-i", []string{"--user-id"}},
			} {
				args := mainCompletionArgs(style, taskRouteArgs(tc.route, tc.prefix)...)
				got := runMainDispatchWithEnv(t, style, env, args...)
				require.Zero(t, got.code, "%s %s", tc.route, tc.prefix)
				require.Empty(t, got.stderr)
				var names []string
				for _, record := range strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n") {
					if style == "zsh" {
						record, _, _ = strings.Cut(record, ":")
					} else if style == "fish" {
						record, _, _ = strings.Cut(record, "\t")
					}
					names = append(names, record)
				}
				require.Equal(t, tc.want, names, "%s %s", tc.route, tc.prefix)
			}
			for _, route := range []string{"transcribe", "translate", "audio transcribe", "audio translate", "files upload", "files create", "audio transcriptions create", "audio:transcriptions create", "audio translations create"} {
				args := mainCompletionArgs(style, taskRouteArgs(route, "--file", "synthetic-")...)
				got := runMainDispatchWithEnv(t, style, env, args...)
				require.Equal(t, mainDispatchResult{code: 10}, got, "%s file completion", route)
			}
			got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "transcribe", "--model", "synthetic-")...)
			require.Equal(t, mainDispatchResult{code: 11}, got, "model values must not request file completion")
		})
	}
}

func TestMainTaskAudioUploadsPreserveRequestsAndOutput(t *testing.T) {
	for _, task := range []struct {
		resource, verb string
	}{
		{"transcriptions", "transcribe"},
		{"translations", "translate"},
	} {
		t.Run(task.verb, func(t *testing.T) {
			routes := []string{"audio:" + task.resource + " create", "audio " + task.resource + " create", "audio " + task.verb, task.verb}
			flags := readableAudioArgs(t, "audio:"+task.resource)[2:]
			for _, response := range []struct{ format, contentType, body string }{
				{"json", "application/json", `{"text":"Synthetic task transcript.","language":"english","duration":2.5,"usage":{"input_tokens":2,"output_tokens":3}}`},
				{"text", "text/plain", "Synthetic task transcript.\n"},
			} {
				t.Run(response.format, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						if r.Method != http.MethodPost || r.URL.Path != "/audio/"+task.resource || r.Header.Get("Authorization") != "Bearer sk-fake-readable-test" {
							t.Errorf("unexpected audio request: %s %s", r.Method, r.URL.Path)
						}
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						defer r.MultipartForm.RemoveAll()
						if r.FormValue("model") != "fake-audio-model" || r.FormValue("response_format") != response.format {
							t.Errorf("audio fields changed: %v", r.MultipartForm.Value)
						}
						file, header, err := r.FormFile("file")
						if err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						defer file.Close()
						upload, err := io.ReadAll(file)
						if err != nil || string(upload) != "synthetic audio upload" || header.Filename != "synthetic.wav" {
							t.Errorf("audio upload changed: filename=%q bytes=%q error=%v", header.Filename, upload, err)
						}
						w.Header().Set("Content-Type", response.contentType)
						io.WriteString(w, response.body)
					}))
					defer server.Close()
					for _, format := range []string{"auto", "json", "raw"} {
						var want mainDispatchResult
						for index, route := range routes {
							args := append(taskRouteArgs(route, flags...), "--response-format", response.format, "--format", format)
							got := runReadableCommand(t, server, args...)
							if index == 0 {
								want = got
								require.Zero(t, want.code)
								require.Empty(t, want.stderr)
								require.Contains(t, want.stdout, "Synthetic task transcript.")
							}
							require.Equal(t, want, got, "%s --format %s", route, format)
						}
					}
					require.EqualValues(t, len(routes)*3, requests.Load())
				})
			}
		})
	}
}

func TestMainTaskSpeechPreservesJSONInputAndBinaryOutput(t *testing.T) {
	const audio = "ID3\x00synthetic task audio\xff\x1b\r\n"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/audio/speech" || r.Header.Get("Authorization") != "Bearer sk-fake-readable-test" {
			t.Errorf("unexpected speech request: %s %s", r.Method, r.URL.Path)
		}
		if body["model"] != "fake-audio-model" || body["input"] != "Synthetic speech" || body["voice"] != "alloy" || body["speed"] != 1.25 || body["instructions"] != "Synthetic instruction" {
			t.Errorf("speech body changed: %v", body)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, audio)
	}))
	defer server.Close()
	flags := readableAudioArgs(t, "audio:speech", "--speed", "1.25", "--instructions", "Synthetic instruction")[2:]
	routes := []string{"audio:speech create", "audio speech create", "audio speak", "speak"}
	path := filepath.Join(t.TempDir(), "speech.mp3")
	for _, output := range []string{"-", path} {
		var want mainDispatchResult
		for index, route := range routes {
			got := runReadableCommand(t, server, append(taskRouteArgs(route, flags...), "--output", output)...)
			if index == 0 {
				want = got
				require.Zero(t, want.code)
				if output == "-" {
					require.Empty(t, want.stderr)
					require.Equal(t, audio, want.stdout)
				} else {
					require.Empty(t, want.stdout)
					require.Equal(t, "Wrote output to: "+path+"\n", want.stderr)
				}
			}
			require.Equal(t, want, got, route)
			if output != "-" {
				saved, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, audio, string(saved), route)
				require.NoError(t, os.Remove(path))
			}
		}
	}
	require.EqualValues(t, len(routes)*2, requests.Load())
}

func TestMainTaskFileUploadPreservesMultipartAndOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic upload.jsonl")
	const upload = "{\"synthetic\":true}\n"
	require.NoError(t, os.WriteFile(path, []byte(upload), 0600))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/files" || r.Header.Get("Authorization") != "Bearer sk-fake-readable-test" || r.Header.Get("X-Trace") != "synthetic-task-upload" {
			t.Errorf("unexpected file request: %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("purpose") != "batch" || r.FormValue("expires_after[anchor]") != "created_at" || r.FormValue("expires_after[seconds]") != "3600" {
			t.Errorf("upload fields changed: %v", r.MultipartForm.Value)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil || string(body) != upload || header.Filename != filepath.Base(path) {
			t.Errorf("upload contents changed: %q, %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"file_task","object":"file","bytes":19,"created_at":17,"filename":"synthetic upload.jsonl","purpose":"batch"}`)
	}))
	defer server.Close()
	for _, format := range []string{"auto", "json", "raw"} {
		flags := []string{"--file", path, "--purpose", "batch", "--expires-after.anchor", "created_at", "--expires-after.seconds", "3600", "-H", "X-Trace: synthetic-task-upload", "--format", format}
		want := runReadableCommand(t, server, taskRouteArgs("files create", flags...)...)
		require.Zero(t, want.code)
		require.Empty(t, want.stderr)
		require.Contains(t, want.stdout, "file_task")
		got := runReadableCommand(t, server, taskRouteArgs("files upload", flags...)...)
		require.Equal(t, want, got)
	}
	require.EqualValues(t, 6, requests.Load())
}

func TestMainTaskAdminRoutesPreserveCredentialAndFlagScope(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		routes     []string
		flags      []string
	}{
		{"projects", "/organization/projects", []string{"admin:organization:projects list", "admin organization projects list", "admin projects list", "projects list"}, []string{"--limit", "2", "--include-archived=true"}},
		{"project user roles", "/projects/proj_task/users/user_task/roles", []string{"admin:organization:projects:users:roles list", "admin organization projects users roles list", "admin projects users roles list", "projects users roles list"}, []string{"proj_task", "user_task", "--limit", "2"}},
		{"audit logs", "/organization/audit_logs", []string{"admin:organization:audit-logs list", "admin organization audit-logs list", "admin audit-logs list"}, []string{"--limit", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != tc.path || r.URL.Query().Get("limit") != "2" {
					t.Errorf("admin route changed: %s %s", r.Method, r.URL.RequestURI())
				}
				if tc.name == "projects" && r.URL.Query().Get("include_archived") != "true" {
					t.Errorf("project query flag lost: %s", r.URL.RawQuery)
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-admin-task-key" || r.Header.Get("OpenAI-Project") != "project_header" || r.Header.Get("OpenAI-Organization") != "org_header" {
					t.Errorf("admin credentials or project/organization scope changed")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"object":"list","data":[{"id":"synthetic_task_result","name":"Synthetic result","created_at":17}],"has_more":false}`)
			}))
			defer server.Close()
			env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-user-task-key", "OPENAI_ADMIN_KEY=synthetic-admin-task-key", "OPENAI_PROJECT=environment_project"}
			for _, format := range []string{"auto", "json", "raw"} {
				var want mainDispatchResult
				for index, route := range tc.routes {
					args := append([]string{"openai", "--project", "project_header", "--organization", "org_header", "--format", format}, taskRouteArgs(route, tc.flags...)...)
					got := runMainDispatchWithEnv(t, "bash", env, args...)
					if index == 0 {
						want = got
						require.Zero(t, want.code)
						require.Empty(t, want.stderr)
						require.Contains(t, want.stdout, "synthetic_task_result")
					}
					require.Equal(t, want, got, route)
				}
			}
			require.EqualValues(t, len(tc.routes)*3, requests.Load())
		})
	}
}

func TestMainTaskRoutesPreserveAPIErrorsAndExit(t *testing.T) {
	for _, task := range []struct{ resource, verb string }{{"transcriptions", "transcribe"}, {"translations", "translate"}, {"speech", "speak"}} {
		t.Run(task.verb, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":{"message":"Synthetic task rejection","type":"permission_error","code":"forbidden"}}`)
			}))
			defer server.Close()
			flags := readableAudioArgs(t, "audio:"+task.resource)[2:]
			for _, format := range []string{"text", "json"} {
				flags := append(append([]string{}, flags...), "--format-error", format)
				want := runReadableCommand(t, server, taskRouteArgs("audio:"+task.resource+" create", flags...)...)
				require.NotZero(t, want.code)
				require.Empty(t, want.stdout)
				require.NotEmpty(t, want.stderr)
				for _, route := range []string{"audio " + task.resource + " create", "audio " + task.verb, task.verb} {
					got := runReadableCommand(t, server, taskRouteArgs(route, flags...)...)
					require.Equal(t, want, got, route)
				}
			}
		})
	}
}

func TestMainTaskResourceRoutesPreserveAPIErrorsAndExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic upload"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":{"message":"Synthetic task rejection","type":"permission_error","code":"forbidden"}}`)
	}))
	defer server.Close()
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-user-task-key", "OPENAI_ADMIN_KEY=synthetic-admin-task-key"}
	for _, tc := range []struct {
		routes []string
		flags  []string
	}{
		{[]string{"files create", "files upload"}, []string{"--file", path, "--purpose", "batch"}},
		{[]string{"admin:organization:projects list", "admin organization projects list", "admin projects list", "projects list"}, nil},
		{[]string{"admin:organization:audit-logs list", "admin organization audit-logs list", "admin audit-logs list"}, nil},
	} {
		for _, format := range []string{"text", "json"} {
			var want mainDispatchResult
			for index, route := range tc.routes {
				args := append([]string{"openai", "--format-error", format}, taskRouteArgs(route, tc.flags...)...)
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				if index == 0 {
					want = got
					require.NotZero(t, want.code)
					require.Empty(t, want.stdout)
					require.NotEmpty(t, want.stderr)
				}
				require.Equal(t, want, got, route)
			}
		}
	}
}

func TestMainTaskRoutesPreserveRequiredFlags(t *testing.T) {
	for _, tc := range []struct {
		routes, required []string
	}{
		{[]string{"audio:transcriptions create", "audio transcriptions create", "audio transcribe", "transcribe"}, []string{"file", "model"}},
		{[]string{"audio:translations create", "audio translations create", "audio translate", "translate"}, []string{"file", "model"}},
		{[]string{"audio:speech create", "audio speech create", "audio speak", "speak"}, []string{"input", "model", "voice"}},
		{[]string{"files create", "files upload"}, []string{"file", "purpose"}},
		{[]string{"admin:organization:projects:users:roles retrieve", "admin organization projects users roles retrieve", "admin projects users roles retrieve", "projects users roles retrieve"}, []string{"project-id", "user-id", "role-id"}},
	} {
		var wantCode int
		for index, route := range tc.routes {
			got := runMainDispatch(t, "bash", append([]string{"openai"}, strings.Fields(route)...)...)
			if index == 0 {
				wantCode = got.code
				require.NotZero(t, wantCode)
			}
			require.Equal(t, wantCode, got.code, route)
			require.Empty(t, got.stdout, route)
			for _, flag := range tc.required {
				require.Contains(t, got.stderr, flag, route)
			}
		}
	}
}

func TestMainTaskAudioInterruptClosesStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send os.Interrupt on Windows")
	}
	for _, task := range []struct{ resource, verb, event, prefix string }{
		{"transcriptions", "transcribe", `{"type":"transcript.text.delta","delta":"Visible task output"}`, "Visible task output"},
		{"speech", "speak", `{"type":"speech.audio.delta","audio":"c3ludGhldGlj"}`, "Type: speech.audio.delta"},
	} {
		var wantCode int
		for index, route := range []string{"audio:" + task.resource + " create", "audio " + task.resource + " create", "audio " + task.verb, task.verb} {
			t.Run(route, func(t *testing.T) {
				disconnected := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					writeStreamingTextEvent(w, task.event)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					close(disconnected)
				}))
				t.Cleanup(server.Close)
				flags := readableAudioArgs(t, "audio:"+task.resource)[2:]
				if task.verb == "speak" {
					flags = append(flags, "--stream-format", "sse")
				} else {
					flags = append(flags, "--stream=true")
				}
				child, stdout, _, ctx := startStreamingTextCommand(t, server, taskRouteArgs(route, flags...)...)
				readStreamingTextPrefix(t, ctx, stdout, task.prefix)
				require.NoError(t, child.Process.Signal(os.Interrupt))
				require.Error(t, child.Wait())
				if index == 0 {
					wantCode = child.ProcessState.ExitCode()
					require.NotZero(t, wantCode)
				}
				require.Equal(t, wantCode, child.ProcessState.ExitCode())
				select {
				case <-disconnected:
				case <-ctx.Done():
					t.Fatal("interrupted task command left the HTTP stream open")
				}
			})
		}
	}
}
