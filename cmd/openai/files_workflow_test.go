package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const filesWorkflowID = "file-example-full-copyable-0123456789abcdefghijklmnopqrstuvwxyz"
const filesWorkflowMetadata = `{"id":"` + filesWorkflowID + `","object":"file","bytes":13,"created_at":1700000000,"filename":"upload space.txt","purpose":"user_data","status":"uploaded","extra":{"note":"preserved value"}}`

func filesWorkflowUploadServer(t *testing.T, filename string, payload []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/files" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Errorf("unexpected upload request: %s %s, type=%q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if values := r.MultipartForm.Value["purpose"]; len(values) != 1 || values[0] != "user_data" {
			t.Errorf("upload purpose changed: %q", values)
		}
		if len(r.MultipartForm.File["file"]) != 1 {
			t.Errorf("expected exactly one uploaded file")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil || string(body) != string(payload) || header.Filename != filename {
			t.Errorf("upload changed: filename=%q bytes=%q error=%v", header.Filename, body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, filesWorkflowMetadata)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestMainFilesWorkflowUploadPaths(t *testing.T) {
	for _, tc := range []struct {
		name, filename string
		payload        []byte
		args           []string
	}{
		{"spaces", "upload space.txt", []byte("hello files!\n"), []string{"upload", "upload space.txt", "--purpose", "user_data"}},
		{"options first", "upload space.txt", []byte("hello files!\n"), []string{"upload", "--purpose", "user_data", "upload space.txt"}},
		{"apostrophe", "upload's copy.txt", []byte("apostrophe\n"), []string{"upload", "upload's copy.txt", "--purpose", "user_data"}},
		{"literal at", "@upload space.txt", []byte("literal at\n"), []string{"upload", "@upload space.txt", "--purpose", "user_data"}},
		{"literal dash", "-upload space.txt", []byte("literal dash\n"), []string{"upload", "--purpose", "user_data", "--", "-upload space.txt"}},
		{"literal double dash", "--", []byte("literal marker\n"), []string{"upload", "--purpose", "user_data", "--", "--"}},
		{"empty", "empty.txt", []byte{}, []string{"upload", "empty.txt", "--purpose", "user_data"}},
		{"binary", "binary.bin", []byte{0, 255, 27, 13, 10, 128}, []string{"upload", "binary.bin", "--purpose", "user_data"}},
		{"upload flag", "@upload space.txt", []byte("flag literal\n"), []string{"upload", "--file", "@upload space.txt", "--purpose", "user_data"}},
		{"create flag", "@upload space.txt", []byte("legacy literal\n"), []string{"create", "--file", "@upload space.txt", "--purpose", "user_data"}},
		{"upload assigned flag", "upload space.txt", []byte("assigned\n"), []string{"upload", "--file=upload space.txt", "--purpose=user_data"}},
		{"upload repeated flag", "upload space.txt", []byte("last file wins\n"), []string{"upload", "--file", "missing.txt", "--file", "upload space.txt", "--purpose", "user_data"}},
		{"create repeated flag", "upload space.txt", []byte("last file wins\n"), []string{"create", "--file", "missing.txt", "--file", "upload space.txt", "--purpose", "user_data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.WriteFile(tc.filename, tc.payload, 0o600))
			server, requests := filesWorkflowUploadServer(t, tc.filename, tc.payload)
			got := runReadableCommand(t, server, append([]string{"files"}, tc.args...)...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, filesWorkflowID)
			require.NotContains(t, got.stdout, "Uploaded ")
			require.NotContains(t, got.stdout, "Download it:")
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestMainFilesWorkflowUploadValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("upload.txt", []byte("synthetic"), 0o600))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing purpose", []string{"upload", "upload.txt"}},
		{"missing path", []string{"upload", "--purpose", "user_data"}},
		{"missing local file", []string{"upload", "missing.txt", "--purpose", "user_data"}},
		{"extra positional", []string{"upload", "upload.txt", "second.txt", "--purpose", "user_data"}},
		{"positional and file flag", []string{"upload", "upload.txt", "--file", "upload.txt", "--purpose", "user_data"}},
		{"file flag and positional", []string{"upload", "--file", "upload.txt", "--purpose", "user_data", "upload.txt"}},
		{"create keeps flag syntax", []string{"create", "upload.txt", "--purpose", "user_data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			got := runReadableCommand(t, server, append([]string{"files"}, tc.args...)...)
			require.NotZero(t, got.code, "%+v", got)
			require.NotEmpty(t, got.stderr)
			require.NotContains(t, got.stdout+got.stderr, "Uploaded ")
			require.NotContains(t, got.stdout+got.stderr, "Download it:")
			require.Equal(t, before, requests.Load(), "validation must precede the request")
		})
	}
}

func TestMainFilesWorkflowUploadStdinCompatibility(t *testing.T) {
	t.Chdir(t.TempDir())
	const filename = "@upload space.txt"
	payload := []byte("synthetic stdin parameters\n")
	require.NoError(t, os.WriteFile(filename, payload, 0o600))
	server, requests := filesWorkflowUploadServer(t, filename, payload)
	for _, tc := range []struct {
		name, stdin     string
		legacy, current []string
	}{
		{"JSON purpose", `{"purpose":"user_data"}`, []string{"--file", filename}, []string{filename}},
		{"YAML purpose", "purpose: user_data\n", []string{"--file", filename}, []string{filename}},
		{"JSON request", `{"purpose":"user_data","file":"@upload space.txt"}`, nil, nil},
		{"flag overrides purpose", `{"purpose":"batch"}`, []string{"--file", filename, "--purpose", "user_data"}, []string{filename, "--purpose", "user_data"}},
		{"path overrides JSON file", `{"purpose":"user_data","file":"missing.txt"}`, []string{"--file", filename}, []string{filename}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputPath := filepath.Join(t.TempDir(), "request.json")
			require.NoError(t, os.WriteFile(inputPath, []byte(tc.stdin), 0o600))
			var want mainDispatchResult
			for index, args := range [][]string{
				append([]string{"openai", "files", "create"}, tc.legacy...),
				append([]string{"openai", "files", "upload"}, tc.current...),
			} {
				input, err := os.Open(inputPath)
				require.NoError(t, err)
				got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-files-workflow-test", "FORCE_COLOR=0"}, input, args...)
				require.NoError(t, input.Close())
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
				if index == 0 {
					want = got
				}
				require.Equal(t, want, got)
			}
		})
	}
	require.EqualValues(t, 10, requests.Load())
}

func TestMainFilesWorkflowMachineOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload space.txt")
	payload := []byte("hello files!\n")
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	uploadServer, uploadRequests := filesWorkflowUploadServer(t, filepath.Base(path), payload)
	var metadataRequests atomic.Int32
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataRequests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/files/"+filesWorkflowID {
			t.Errorf("metadata request changed: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, filesWorkflowMetadata)
	}))
	defer metadataServer.Close()
	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"piped default", nil, ""},
		{"explicit auto", []string{"--format", "auto"}, ""},
		{"explicit text", []string{"--format", "text"}, ""},
		{"json", []string{"--format", "json"}, "json"},
		{"jsonl", []string{"--format", "jsonl"}, "json"},
		{"raw", []string{"--format", "raw"}, filesWorkflowMetadata + "\n"},
		{"extract ID", []string{"--transform", "id"}, `"` + filesWorkflowID + "\"\n"},
		{"raw ID", []string{"--transform", "id", "--raw-output"}, filesWorkflowID + "\n"},
		{"raw timestamp", []string{"--transform", "created_at", "--raw-output"}, "1700000000\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, route := range []struct {
				server          *httptest.Server
				legacy, current []string
			}{
				{uploadServer, []string{"files", "create", "--file", path, "--purpose", "user_data"}, []string{"files", "upload", path, "--purpose", "user_data"}},
				{metadataServer, []string{"files", "retrieve", filesWorkflowID}, []string{"files", "get", filesWorkflowID}},
			} {
				want := runReadableCommand(t, route.server, append(route.legacy, tc.flags...)...)
				got := runReadableCommand(t, route.server, append(route.current, tc.flags...)...)
				require.Zero(t, want.code, "%+v", want)
				require.Empty(t, want.stderr)
				require.Equal(t, want, got, "%q", route.current)
				switch tc.want {
				case "json":
					var value map[string]any
					require.NoError(t, json.Unmarshal([]byte(got.stdout), &value))
					require.JSONEq(t, filesWorkflowMetadata, got.stdout)
				case "":
					require.Contains(t, got.stdout, filesWorkflowID)
					require.Contains(t, got.stdout, "1700000000")
				default:
					require.Equal(t, tc.want, got.stdout)
				}
				require.NotContains(t, got.stdout, "Download it:")
			}
		})
	}
	require.EqualValues(t, 18, uploadRequests.Load())
	require.EqualValues(t, 18, metadataRequests.Load())
}

func TestMainFilesWorkflowDownloadBytes(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body string }{
		{"text", "text/plain", "synthetic text\nsecond line\n"},
		{"binary", "application/octet-stream", "\x00\xff\x1b\r\nsynthetic\x80\x00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/files/"+filesWorkflowID+"/content" {
					t.Errorf("download request changed: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			for _, flags := range [][]string{nil, {"--output", "-"}, {"--format", "json"}, {"--format", "raw"}, {"--raw-output"}} {
				for _, route := range []string{"content", "download"} {
					got := runReadableCommand(t, server, append([]string{"files", route, filesWorkflowID}, flags...)...)
					require.Equal(t, mainDispatchResult{stdout: tc.body}, got, "%s %v", route, flags)
				}
			}
			for _, flag := range []string{"--output", "-o"} {
				path := filepath.Join(t.TempDir(), "downloaded copy's.bin")
				var want mainDispatchResult
				for index, route := range []string{"content", "download"} {
					got := runReadableCommand(t, server, "files", route, "--file-id", filesWorkflowID, flag, path)
					require.Zero(t, got.code, "%+v", got)
					require.Empty(t, got.stderr)
					if index == 0 {
						want = got
					}
					require.Equal(t, want, got)
					body, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, tc.body, string(body))
					require.NoError(t, os.Remove(path))
				}
			}
			require.EqualValues(t, 14, requests.Load())
		})
	}
}

func TestMainFilesWorkflowFailuresPreserveExits(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/files/"+filesWorkflowID+"/content" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, "synthetic download")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"Synthetic file was not found.","type":"invalid_request_error","code":"file_not_found"}}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "upload.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic"), 0o600))
	destination := filepath.Join(t.TempDir(), "missing", "download.bin")
	for _, pair := range [][2][]string{
		{{"files", "create", "--file", path, "--purpose", "user_data"}, {"files", "upload", path, "--purpose", "user_data"}},
		{{"files", "retrieve", "file-missing"}, {"files", "get", "file-missing"}},
		{{"files", "content", "file-missing"}, {"files", "download", "file-missing"}},
		{{"files", "content", filesWorkflowID, "--output", destination}, {"files", "download", filesWorkflowID, "--output", destination}},
	} {
		want := runReadableCommand(t, server, pair[0]...)
		got := runReadableCommand(t, server, pair[1]...)
		require.NotZero(t, want.code, "%q: %+v", pair[0], want)
		require.NotEmpty(t, want.stderr)
		require.Equal(t, want, got, "%q", pair[1])
		require.NotContains(t, got.stdout+got.stderr, "Uploaded ")
		require.NotContains(t, got.stdout+got.stderr, "Download it:")
	}
	require.EqualValues(t, 8, requests.Load())
}

func TestMainFilesWorkflowShellRedirection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell redirection; native Windows shell coverage is separate")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	for _, shell := range []nativeShell{
		{name: "bash", executable: "bash", args: []string{"--noprofile", "--norc", "-c"}},
		{name: "zsh", executable: "zsh", args: []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				t.Skipf("%s is not installed", shell.executable)
			}
			shell.executable = path
			for _, payload := range []string{"synthetic text\n", "\x00\xff\x1b\r\nsynthetic\x80\x00"} {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/files/"+filesWorkflowID+"/content" {
						t.Errorf("redirected download request changed: %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/octet-stream")
					_, _ = io.WriteString(w, payload)
				}))
				t.Cleanup(server.Close)
				work := t.TempDir()
				for _, route := range []string{"content", "download"} {
					destination := filepath.Join(work, "redirected copy's.bin")
					script := "export OPENAI_CLI_MAIN_DISPATCH_PROCESS=1 OPENAI_API_KEY=sk-fake-files-workflow-test; " + quote(binary) + " -test.run='^TestMainDispatchProcess$' -- openai files " + route + " " + filesWorkflowID + " > " + quote(destination)
					got := runNativeShell(t, shell, work, t.TempDir(), server.URL, script)
					require.Equal(t, mainDispatchResult{}, got, "%s %s", shell.name, route)
					body, err := os.ReadFile(destination)
					require.NoError(t, err)
					require.Equal(t, payload, string(body), "%s %s", shell.name, route)
				}
				require.EqualValues(t, 2, requests.Load())
			}
		})
	}
}

func TestMainFilesWorkflowHelpAndCompletionStayLocal(t *testing.T) {
	env := []string{"OPENAI_BASE_URL=invalid-files-help-url", "OPENAI_CUSTOM_HEADERS=invalid-files-help-headers", "OPENAI_CLI_COMPLETION_FILE_VALUES=1"}
	for _, route := range []string{"upload", "get", "download", "create", "retrieve", "content"} {
		t.Run(route, func(t *testing.T) {
			cases := [][]string{
				{"openai", "files", route, "--help"},
				{"openai", "help", "--all", "files", route},
				{"openai", "files", route, "--format", "json", "--help"},
			}
			if route == "upload" || route == "create" {
				cases = append(cases, []string{"openai", "files", route, "--file", "missing-local.txt", "--purpose", "user_data", "--help"})
			} else {
				cases = append(cases, []string{"openai", "files", route, "--file-id", "file-missing", "--help"})
			}
			for _, args := range cases {
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "openai files "+route)
				if route == "upload" || route == "create" {
					require.Contains(t, got.stdout, "Path to the file to upload")
					require.NotContains(t, got.stdout, "The File object (not file name)")
				}
			}
		})
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, route := range []string{"upload", "get", "download"} {
			got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "files", route[:2])...)
			require.Zero(t, got.code, "%s: %+v", style, got)
			require.Empty(t, got.stderr)
			require.True(t, strings.HasPrefix(got.stdout, route), "%s: %+v", style, got)
		}
		got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "files", "upload", "--file", "synthetic-")...)
		require.Equal(t, mainDispatchResult{code: 10}, got, "%s file completion", style)
	}
}

func TestMainFilesWorkflowHelpExplainsPaths(t *testing.T) {
	got := runMainDispatch(t, "bash", "openai", "files", "upload", "--help")
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	text := strings.Join(strings.Fields(got.stdout), " ")
	require.Contains(t, text, "With upload, PATH can replace --file PATH.")
	require.Contains(t, text, "A leading @ is literal.")
}

func TestMainFilesWorkflowPositionalCompletion(t *testing.T) {
	env := []string{"OPENAI_BASE_URL=invalid-files-completion-url", "OPENAI_CLI_COMPLETION_FILE_VALUES=1"}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, args := range [][]string{
			{"files", "upload", ""},
			{"files", "upload", "upload sp"},
			{"files", "upload", "@literal"},
			{"files", "upload", "--purpose", "user_data", ""},
			{"files", "upload", "--purpose", "user_data", "--", "-literal"},
		} {
			got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, args...)...)
			require.Equal(t, mainDispatchResult{code: 10}, got, "%s %q", style, args)
		}
		for _, args := range [][]string{
			{"files", "upload", "first.txt", ""},
			{"files", "upload", "--file", "first.txt", ""},
			{"files", "upload", "--file=first.txt", ""},
			{"files", "upload", "--purpose", ""},
		} {
			got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, args...)...)
			require.Equal(t, mainDispatchResult{code: 11}, got, "%s %q", style, args)
		}
	}
}

func TestMainFilesWorkflowHelpAfterOperands(t *testing.T) {
	for _, args := range [][]string{
		{"files", "upload", "missing.txt", "--purpose", "user_data", "--help"},
		{"files", "get", "file-example", "--help"},
		{"files", "download", "file-example", "--output", "absent/target.txt", "--help"},
	} {
		got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=invalid-files-help-url"}, append([]string{"openai"}, args...)...)
		require.Zero(t, got.code, "%+v", got)
		require.Empty(t, got.stderr)
		require.Contains(t, got.stdout, "SYNOPSIS:")
		require.Contains(t, got.stdout, "openai files "+args[1])
	}
}
