package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

const skillUploadResponse = `{"id":"skill_synthetic","object":"skill","default_version":"2","extra":{"note":"preserved"}}`

type skillUploadPart struct {
	name, filename, contentType string
	data                        []byte
}

type skillUploadRequest struct {
	method, path string
	header       http.Header
	parts        []skillUploadPart
}

func readSkillUpload(t *testing.T, r *http.Request) []skillUploadPart {
	t.Helper()
	reader, err := r.MultipartReader()
	if err != nil {
		t.Errorf("request is not multipart: %v", err)
		return nil
	}
	var parts []skillUploadPart
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Errorf("read multipart: %v", err)
			return parts
		}
		// Part.FileName removes directories. Inspect the actual wire filename.
		_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			t.Errorf("parse multipart disposition: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Errorf("read multipart part: %v", err)
		}
		parts = append(parts, skillUploadPart{part.FormName(), disposition["filename"], part.Header.Get("Content-Type"), data})
	}
}

func skillUploadServer(t *testing.T, status int, response string) (*httptest.Server, func() []skillUploadRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []skillUploadRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := skillUploadRequest{r.Method, r.URL.Path, r.Header.Clone(), readSkillUpload(t, r)}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return server, func() []skillUploadRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]skillUploadRequest(nil), requests...)
	}
}

func skillUploadZIP(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"demo/SKILL.md", []byte("---\nname: demo\ndescription: Synthetic test\n---\n# Demo\n"), 0o644},
		{"demo/scripts/run.sh", []byte("#!/bin/sh\nprintf 'synthetic\\n'\n"), 0o755},
		{"demo/data.bin", []byte{0, 255, 128, 27, '\r', '\n'}, 0o644},
	} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		file, err := w.CreateHeader(header)
		require.NoError(t, err)
		_, err = file.Write(entry.data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func skillUploadRoutes() []struct {
	name, path string
	args       []string
} {
	return []struct {
		name, path string
		args       []string
	}{
		{"skill", "/skills", []string{"skills", "create"}},
		{"version nested", "/skills/skill_synthetic/versions", []string{"skills", "versions", "create", "--skill-id", "skill_synthetic"}},
		{"version colon", "/skills/skill_synthetic/versions", []string{"skills:versions", "create", "--skill-id", "skill_synthetic"}},
	}
}

func TestMainSkillUploadZIPBytesAcrossRoutes(t *testing.T) {
	t.Chdir(t.TempDir())
	payload := skillUploadZIP(t)
	const name = "@skill's archive space.zip"
	require.NoError(t, os.WriteFile(name, payload, 0o600))
	for _, route := range skillUploadRoutes() {
		t.Run(route.name, func(t *testing.T) {
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			got := runShellFileCommand(t, server, nil, nil, append(route.args, "--files", name)...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.JSONEq(t, skillUploadResponse, got.stdout)
			wire := requests()
			require.Len(t, wire, 1)
			require.Equal(t, http.MethodPost, wire[0].method)
			require.Equal(t, route.path, wire[0].path)
			require.Len(t, wire[0].parts, 1)
			require.Equal(t, "files", wire[0].parts[0].name)
			require.Equal(t, name, wire[0].parts[0].filename)
			require.Equal(t, payload, wire[0].parts[0].data)
		})
	}
}

func TestMainSkillUploadZIPStdin(t *testing.T) {
	payload := skillUploadZIP(t)
	for _, source := range []string{"-", "/dev/stdin"} {
		t.Run(source, func(t *testing.T) {
			if source == "/dev/stdin" && runtime.GOOS == "windows" {
				t.Skip("native /dev/stdin is unavailable on Windows")
			}
			for _, route := range skillUploadRoutes() {
				t.Run(route.name, func(t *testing.T) {
					server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
					got := runShellFileCommand(t, server, shellFileInput(t, payload), []string{"OPENAI_UNTRUSTED_STDIN=true"}, append(route.args, "--files", source)...)
					require.Zero(t, got.code, "%+v", got)
					require.Empty(t, got.stderr)
					wire := requests()
					require.Len(t, wire, 1)
					require.Equal(t, route.path, wire[0].path)
					require.Equal(t, []skillUploadPart{{"files", "skill.zip", "application/zip", payload}}, wire[0].parts)
				})
			}
		})
	}
}

func TestMainSkillUploadDuplicateStdinFailsBeforeRead(t *testing.T) {
	server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
	read, write, err := os.Pipe()
	require.NoError(t, err)
	defer read.Close()
	defer write.Close()
	for _, route := range skillUploadRoutes() {
		t.Run(route.name, func(t *testing.T) {
			got := runShellFileCommand(t, server, read, nil, append(route.args, "--files", "-", "--files", "-")...)
			require.NotZero(t, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.Contains(t, got.stderr, "multiple request parameters use stdin")
			require.Empty(t, requests())
		})
	}
}

func TestMainSkillUploadDirectoryArchive(t *testing.T) {
	t.Chdir(t.TempDir())
	const root = "@skill space"
	want := map[string][]byte{
		root + "/SKILL.md":       []byte("---\nname: synthetic\ndescription: Fixture only\n---\n# Skill\n"),
		root + "/scripts/run.sh": []byte("#!/bin/sh\nprintf 'synthetic\\n'\n"),
		root + "/data/bytes.bin": {0, 255, 128, '\n'},
		root + "/.config":        []byte("synthetic hidden file\n"),
	}
	for name, data := range want {
		require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o700))
		require.NoError(t, os.WriteFile(name, data, 0o644))
	}
	require.NoError(t, os.Chmod(filepath.Join(root, "scripts", "run.sh"), 0o755))
	var firstArchive []byte
	for _, route := range skillUploadRoutes() {
		t.Run(route.name, func(t *testing.T) {
			tmp := t.TempDir()
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			got := runShellFileCommand(t, server, nil, []string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp}, append(route.args, "--files", root)...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			wire := requests()
			require.Len(t, wire, 1)
			require.Equal(t, route.path, wire[0].path)
			require.Len(t, wire[0].parts, 1)
			part := wire[0].parts[0]
			require.Equal(t, "files", part.name)
			require.Equal(t, root+".zip", part.filename)
			require.Equal(t, "application/zip", part.contentType)
			archive, err := zip.NewReader(bytes.NewReader(part.data), int64(len(part.data)))
			require.NoError(t, err)
			seen := make(map[string][]byte)
			var names []string
			for _, file := range archive.File {
				if file.FileInfo().IsDir() {
					continue
				}
				names = append(names, file.Name)
				require.NotContains(t, seen, file.Name, "duplicate archive member")
				reader, err := file.Open()
				require.NoError(t, err)
				seen[file.Name], err = io.ReadAll(reader)
				require.NoError(t, err)
				require.NoError(t, reader.Close())
				if runtime.GOOS != "windows" {
					if strings.HasSuffix(file.Name, "run.sh") {
						require.NotZero(t, file.Mode().Perm()&0o111, "executable bit was lost")
					} else {
						require.Zero(t, file.Mode().Perm()&0o111, "data became executable")
					}
				}
			}
			require.IsIncreasing(t, names, "archive member order must be stable")
			require.Equal(t, want, seen)
			if firstArchive == nil {
				firstArchive = part.data
			} else {
				require.Equal(t, firstArchive, part.data, "same directory must produce the same ZIP bytes")
			}
			entries, err := os.ReadDir(tmp)
			require.NoError(t, err)
			require.Empty(t, entries, "temporary ZIP survived successful upload")
		})
	}
}

func TestMainSkillUploadDefaultAndBodyOverrides(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "skill.zip")
	payload := skillUploadZIP(t)
	require.NoError(t, os.WriteFile(archive, payload, 0o600))
	for _, tc := range []struct {
		name, input, wantDefault, wantExtra string
		flags                               []string
	}{
		{"omitted", "", "", "", nil},
		{"explicit false", "", "false", "", []string{"--default=false"}},
		{"explicit true", "", "true", "", []string{"--default=true"}},
		{"JSON false", `{"default":false,"note":"synthetic body"}`, "false", "synthetic body", nil},
		{"YAML true", "default: true\nnote: synthetic body\n", "true", "synthetic body", nil},
		{"JSON quoted false", `{"default":"false","note":"synthetic body"}`, "false", "synthetic body", nil},
		{"YAML quoted true", "default: \"true\"\nnote: synthetic body\n", "true", "synthetic body", nil},
		{"flag overrides JSON", `{"default":true,"note":"synthetic body"}`, "false", "synthetic body", []string{"--default=false"}},
		{"flag overrides YAML", "default: false\nnote: synthetic body\n", "true", "synthetic body", []string{"--default=true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			args := append([]string{"skills", "versions", "create", "skill_synthetic", "--files", archive}, tc.flags...)
			got := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.input)), []string{"OPENAI_UNTRUSTED_STDIN=true"}, args...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			wire := requests()
			require.Len(t, wire, 1)
			values := make(map[string]string)
			files := 0
			for _, part := range wire[0].parts {
				if part.name == "files" {
					files++
					require.Equal(t, payload, part.data)
				} else {
					values[part.name] = string(part.data)
				}
			}
			require.Equal(t, 1, files)
			if tc.wantDefault == "" {
				require.NotContains(t, values, "default")
			} else {
				require.Equal(t, tc.wantDefault, values["default"])
			}
			require.Equal(t, tc.wantExtra, values["note"])
		})
	}
}

func TestMainSkillUploadPipedFileInputs(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "skill space.zip")
	payload := skillUploadZIP(t)
	require.NoError(t, os.WriteFile(archive, payload, 0o600))
	quoted, err := json.Marshal(archive)
	require.NoError(t, err)
	for _, tc := range []struct{ name, input string }{
		{"JSON", `{"files":[` + string(quoted) + `],"default":false}`},
		{"YAML", "files:\n  - " + string(quoted) + "\ndefault: false\n"},
	} {
		for _, untrusted := range []string{"false", "true"} {
			t.Run(tc.name+"/untrusted="+untrusted, func(t *testing.T) {
				server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
				got := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.input)), []string{"OPENAI_UNTRUSTED_STDIN=" + untrusted}, "skills", "versions", "create", "skill_synthetic")
				if untrusted == "true" {
					require.NotZero(t, got.code, "%+v", got)
					require.Empty(t, got.stdout)
					require.Contains(t, got.stderr, "--files")
					require.Empty(t, requests())
					return
				}
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
				wire := requests()
				require.Len(t, wire, 1)
				var files []skillUploadPart
				for _, part := range wire[0].parts {
					if part.name == "files" {
						files = append(files, part)
					}
				}
				require.Len(t, files, 1)
				require.Equal(t, payload, files[0].data)
			})
		}
	}
}

func TestMainSkillUploadIndividualFilesAndRequestConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	const root = "skill space"
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o700))
	files := []string{filepath.Join(root, "SKILL.md"), filepath.Join(root, "scripts", "@run.sh")}
	payloads := [][]byte{[]byte("# Synthetic skill\n"), {0, 255, '\n'}}
	for i, file := range files {
		require.NoError(t, os.WriteFile(file, payloads[i], 0o600))
	}
	for _, route := range skillUploadRoutes() {
		t.Run(route.name, func(t *testing.T) {
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			args := append([]string{"--base-url", server.URL + "/prefix/", "--api-key", "sk-fake-explicit-skill", "--project", "proj_synthetic", "--organization", "org_synthetic", "--header", "X-Skill-Test: explicit"}, route.args...)
			args = append(args, "--files", files[0], "--files", files[1])
			got := runShellFileCommand(t, server, nil, []string{"OPENAI_CUSTOM_HEADERS=X-Skill-Test: environment\nX-Skill-Extra: preserved"}, args...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			wire := requests()
			require.Len(t, wire, 1)
			require.Equal(t, "/prefix"+route.path, wire[0].path)
			require.Equal(t, "Bearer sk-fake-explicit-skill", wire[0].header.Get("Authorization"))
			require.Equal(t, "proj_synthetic", wire[0].header.Get("OpenAI-Project"))
			require.Equal(t, "org_synthetic", wire[0].header.Get("OpenAI-Organization"))
			require.Equal(t, "explicit", wire[0].header.Get("X-Skill-Test"))
			require.Equal(t, "preserved", wire[0].header.Get("X-Skill-Extra"))
			require.Len(t, wire[0].parts, 2)
			for i, part := range wire[0].parts {
				require.Equal(t, "files[]", part.name)
				require.Equal(t, filepath.ToSlash(files[i]), part.filename)
				require.Equal(t, payloads[i], part.data)
			}
		})
	}
}

func TestMainSkillUploadAbsoluteFilesPreserveCommonRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo-skill")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o700))
	files := []string{filepath.Join(root, "SKILL.md"), filepath.Join(root, "scripts", "helper.sh")}
	payloads := [][]byte{[]byte("# Synthetic skill\n"), []byte("#!/bin/sh\nprintf 'synthetic\\n'\n")}
	for i, name := range files {
		require.NoError(t, os.WriteFile(name, payloads[i], 0o600))
	}
	for _, route := range skillUploadRoutes() {
		t.Run(route.name, func(t *testing.T) {
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			got := runShellFileCommand(t, server, nil, nil, append(route.args, "--files", files[0], "--files", files[1])...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			wire := requests()
			require.Len(t, wire, 1)
			require.Len(t, wire[0].parts, 2)
			for i, name := range []string{"demo-skill/SKILL.md", "demo-skill/scripts/helper.sh"} {
				require.Equal(t, "files[]", wire[0].parts[i].name)
				require.Equal(t, name, wire[0].parts[i].filename)
				require.Equal(t, payloads[i], wire[0].parts[i].data)
			}
		})
	}
}

func TestMainSkillUploadLocalFailuresAndRecovery(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing.zip")
	empty := filepath.Join(root, "empty")
	require.NoError(t, os.Mkdir(empty, 0o700))
	linked := filepath.Join(root, "linked")
	require.NoError(t, os.Mkdir(linked, 0o700))
	target := filepath.Join(root, "outside.md")
	require.NoError(t, os.WriteFile(target, []byte("synthetic"), 0o600))
	symlinkErr := os.Symlink(target, filepath.Join(linked, "SKILL.md"))
	for _, tc := range []struct{ name, path string }{{"missing", missing}, {"empty directory", empty}, {"symlink member", linked}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "symlink member" && symlinkErr != nil {
				t.Skipf("symlink creation unavailable: %v", symlinkErr)
			}
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			for _, route := range skillUploadRoutes() {
				tmp := t.TempDir()
				got := runShellFileCommand(t, server, nil, []string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp}, append(route.args, "--files", tc.path)...)
				require.NotZero(t, got.code, "%s: %+v", route.name, got)
				require.Empty(t, got.stdout)
				require.NotEmpty(t, got.stderr)
				require.Contains(t, got.stderr, "--files")
				require.NotContains(t, got.stderr, root, "diagnostics leaked the private absolute path")
				require.Empty(t, requests(), "local failure sent a request")
				entries, err := os.ReadDir(tmp)
				require.NoError(t, err)
				require.Empty(t, entries, "temporary ZIP survived local failure")
			}
		})
	}
	// Follow the missing-path guidance by correcting --files to an existing ZIP.
	require.NoError(t, os.WriteFile(missing, skillUploadZIP(t), 0o600))
	server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
	got := runShellFileCommand(t, server, nil, nil, "skills", "create", "--files", missing)
	require.Zero(t, got.code, "%+v", got)
	require.Len(t, requests(), 1)
}

func TestMainSkillUploadServerFailuresDoNotRetry(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "malformed.zip")
	payload := []byte{'P', 'K', 0, 255, '\n'}
	require.NoError(t, os.WriteFile(archive, payload, 0o600))
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		for _, route := range skillUploadRoutes() {
			t.Run(http.StatusText(status)+"/"+route.name, func(t *testing.T) {
				server, requests := skillUploadServer(t, status, `{"error":{"message":"Synthetic invalid ZIP archive","type":"invalid_request_error","code":"invalid_skill_zip"}}`)
				got := runShellFileCommand(t, server, nil, nil, append(route.args, "--files", archive, "--format-error", "json")...)
				require.NotZero(t, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				require.True(t, json.Valid([]byte(got.stderr)), "error stream must contain one JSON document: %q", got.stderr)
				require.Contains(t, got.stderr, "Synthetic invalid ZIP archive")
				wire := requests()
				require.Len(t, wire, 1, "upload mutation must not retry")
				require.Len(t, wire[0].parts, 1)
				require.Equal(t, payload, wire[0].parts[0].data, "the API must receive the original malformed ZIP")
			})
		}
	}
}

func TestMainSkillUploadRedirectsDoNotReplay(t *testing.T) {
	payload := skillUploadZIP(t)
	archive := filepath.Join(t.TempDir(), "skill.zip")
	require.NoError(t, os.WriteFile(archive, payload, 0o600))
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var uploads, replays atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					replays.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, skillUploadResponse)
					return
				}
				uploads.Add(1)
				parts := readSkillUpload(t, r)
				if len(parts) != 1 || !bytes.Equal(parts[0].data, payload) {
					t.Error("initial redirect request did not contain the original ZIP")
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
			}))
			defer server.Close()
			got := runShellFileCommand(t, server, nil, nil, "skills", "create", "--files", archive)
			require.NotZero(t, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.Contains(t, got.stderr, "redirect")
			require.EqualValues(t, 1, uploads.Load())
			require.Zero(t, replays.Load(), "redirect replayed an upload mutation")
		})
	}
}

func TestMainSkillUploadFailureCleansPreparedArchive(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Synthetic skill\n"), 0o600))
	missingPath := filepath.Join(t.TempDir(), "missing-synthetic-skill-input")
	missingInput, err := json.Marshal(map[string]string{"note": "@file://" + missingPath})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, input string
		status      int
		requests    int
	}{
		{"later embed failure", string(missingInput), http.StatusOK, 0},
		{"API failure", "", http.StatusInternalServerError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			server, requests := skillUploadServer(t, tc.status, `{"error":{"message":"Synthetic upload failure","type":"server_error"}}`)
			got := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.input)), []string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp, "OPENAI_UNTRUSTED_STDIN=false"}, "skills", "create", "--files", root)
			require.NotZero(t, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.NotEmpty(t, got.stderr)
			require.Len(t, requests(), tc.requests)
			entries, err := os.ReadDir(tmp)
			require.NoError(t, err)
			require.Empty(t, entries, "temporary ZIP survived failure")
		})
	}
}

func TestMainSkillUploadHelpDoesNotReadInputs(t *testing.T) {
	for _, route := range skillUploadRoutes() {
		t.Run(route.name, func(t *testing.T) {
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			got := runReadableCommand(t, server, append(route.args, "--files", "missing-synthetic-directory", "--help")...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "--files")
			require.Contains(t, got.stdout, "ZIP")
			require.Contains(t, got.stdout, "Symlinks")
			require.Empty(t, requests())
		})
	}
}

func TestMainSkillUploadStructuredOutput(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "skill.zip")
	require.NoError(t, os.WriteFile(archive, skillUploadZIP(t), 0o600))
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"JSON", []string{"--format", "json"}, ""},
		{"JSONL", []string{"--format", "jsonl"}, ""},
		{"YAML", []string{"--format", "yaml"}, ""},
		{"raw", []string{"--format", "raw"}, skillUploadResponse + "\n"},
		{"extraction", []string{"--transform", "id", "--raw-output"}, "skill_synthetic\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := skillUploadServer(t, http.StatusOK, skillUploadResponse)
			got := runReadableCommand(t, server, append([]string{"skills", "create", "--files", archive}, tc.args...)...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			if tc.name == "YAML" {
				var gotValue, wantValue map[string]any
				require.NoError(t, yaml.Unmarshal([]byte(got.stdout), &gotValue))
				require.NoError(t, json.Unmarshal([]byte(skillUploadResponse), &wantValue))
				require.Equal(t, wantValue, gotValue)
			} else if tc.want == "" {
				require.JSONEq(t, skillUploadResponse, got.stdout)
			} else {
				require.Equal(t, tc.want, got.stdout)
			}
			require.Len(t, requests(), 1)
		})
	}
}

func TestMainSkillUploadPreservesLifecycleAndDownloads(t *testing.T) {
	payload := skillUploadZIP(t)
	for _, tc := range []struct {
		name, method, path, body string
		args                     []string
		binary                   bool
	}{
		{"retrieve", "GET", "/skills/skill_synthetic", "", []string{"skills", "retrieve", "skill_synthetic"}, false},
		{"default version", "POST", "/skills/skill_synthetic", `{"default_version":"2"}`, []string{"skills", "update", "skill_synthetic", "--default-version", "2"}, false},
		{"delete", "DELETE", "/skills/skill_synthetic", "", []string{"skills", "delete", "skill_synthetic"}, false},
		{"version retrieve", "GET", "/skills/skill_synthetic/versions/2", "", []string{"skills", "versions", "retrieve", "skill_synthetic", "2"}, false},
		{"version delete", "DELETE", "/skills/skill_synthetic/versions/2", "", []string{"skills:versions", "delete", "skill_synthetic", "2"}, false},
		{"skill download", "GET", "/skills/skill_synthetic/content", "", []string{"skills", "content", "retrieve", "skill_synthetic", "--output", "-"}, true},
		{"version download", "GET", "/skills/skill_synthetic/versions/2/content", "", []string{"skills", "versions", "content", "retrieve", "skill_synthetic", "2", "--output", "-"}, true},
		{"colon download", "GET", "/skills/skill_synthetic/versions/2/content", "", []string{"skills:versions:content", "retrieve", "skill_synthetic", "2", "--output", "-"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request changed: %s %s", r.Method, r.URL.Path)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != tc.body {
					t.Errorf("request body=%q error=%v; want %q", body, err, tc.body)
				}
				if tc.binary {
					w.Header().Set("Content-Type", "application/zip")
					_, _ = w.Write(payload)
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, skillUploadResponse)
				}
			}))
			defer server.Close()
			got := runShellFileCommand(t, server, nil, nil, tc.args...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			if tc.binary {
				require.Equal(t, payload, []byte(got.stdout))
			} else {
				require.JSONEq(t, skillUploadResponse, got.stdout)
			}
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestMainSkillUploadPreservesListOptions(t *testing.T) {
	const response = `{"object":"list","data":[{"id":"skill_synthetic","object":"skill"}],"has_more":false}`
	for _, tc := range []struct {
		name, path string
		args       []string
	}{
		{"skills", "/skills", []string{"skills", "list"}},
		{"versions", "/skills/skill_synthetic/versions", []string{"skills", "versions", "list", "skill_synthetic"}},
		{"colon versions", "/skills/skill_synthetic/versions", []string{"skills:versions", "list", "skill_synthetic"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != tc.path {
					t.Errorf("request changed: %s %s", r.Method, r.URL.Path)
				}
				for name, want := range map[string]string{"after": "cursor_synthetic", "limit": "2", "order": "asc"} {
					if got := r.URL.Query().Get(name); got != want {
						t.Errorf("query %s=%q; want %q", name, got, want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response)
			}))
			defer server.Close()
			got := runReadableCommand(t, server, append(tc.args, "--after", "cursor_synthetic", "--limit", "2", "--order", "asc", "--max-items", "-1", "--format", "raw")...)
			require.Equal(t, mainDispatchResult{stdout: response + "\n"}, got)
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

func runSkillUploadCancellationSignals(t *testing.T, check func(*testing.T, os.Signal, int)) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal does not send SIGINT or SIGTERM on Windows")
	}
	for _, tc := range []struct {
		name     string
		signal   os.Signal
		exitCode int
	}{
		{"SIGINT", os.Interrupt, 130},
		{"SIGTERM", syscall.SIGTERM, 143},
	} {
		t.Run(tc.name, func(t *testing.T) { check(t, tc.signal, tc.exitCode) })
	}
}

func TestMainSkillUploadInterruptCleansArchive(t *testing.T) {
	runSkillUploadCancellationSignals(t, checkSkillUploadInterruptCleansArchive)
}

func checkSkillUploadInterruptCleansArchive(t *testing.T, signal os.Signal, exitCode int) {
	root, tmp, home := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Synthetic skill\n"), 0o600))
	ready, closed, stop := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readSkillUpload(t, r)
		close(ready)
		select {
		case <-r.Context().Done():
			close(closed)
		case <-stop:
		}
	}))
	defer server.Close()
	defer close(stop)
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "skills", "create", "--files", root)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home,
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-skill-cancellation", "OPENAI_BASE_URL="+server.URL,
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "FORCE_COLOR=0")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	require.NoError(t, child.Start())
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		waited = true
		t.Fatalf("upload exited before request: %v", err)
	case <-ctx.Done():
		t.Fatal("upload did not start")
	}
	require.NoError(t, child.Process.Signal(signal))
	select {
	case err := <-done:
		waited = true
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		require.Equal(t, exitCode, exit.ExitCode())
	case <-ctx.Done():
		t.Fatal("upload did not stop on interrupt")
	}
	require.NoError(t, ctx.Err())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "Request canceled.")
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries, "temporary ZIP survived cancellation")
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("interrupted upload left HTTP request open")
	}
}

func TestMainSkillUploadInterruptDuringUpload(t *testing.T) {
	runSkillUploadCancellationSignals(t, checkSkillUploadInterruptDuringUpload)
}

func checkSkillUploadInterruptDuringUpload(t *testing.T, signal os.Signal, exitCode int) {
	root, tmp, home := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Synthetic skill\n"), 0o600))
	// Incompressible bytes keep the directory ZIP larger than transport buffers.
	payload := make([]byte, 32<<20)
	_, err := rand.New(rand.NewSource(42)).Read(payload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "data.bin"), payload, 0o600))
	ready, resume := make(chan int64, 1), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	type bodyResult struct {
		bytes int64
		err   error
	}
	drained := make(chan bodyResult, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.ReadFull(r.Body, make([]byte, 1024)); err != nil {
			t.Errorf("initial upload read: %v", err)
			return
		}
		ready <- r.ContentLength
		<-resume // Hold backpressure until the interrupted process exits.
		count, err := io.Copy(io.Discard, r.Body)
		drained <- bodyResult{count + 1024, err}
	}))
	defer server.Close()
	defer release()
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "skills", "create", "--files", root)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home,
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-skill-cancellation", "OPENAI_BASE_URL="+server.URL,
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "FORCE_COLOR=0")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	require.NoError(t, child.Start())
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	var length int64
	select {
	case length = <-ready:
		require.Greater(t, length, int64(len(payload)), "fixture must retain incompressible upload bytes")
	case err := <-done:
		waited = true
		t.Fatalf("upload exited before request: %v", err)
	case <-ctx.Done():
		t.Fatal("upload did not start")
	}
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "fixture completed encoding before cancellation")
	require.NoError(t, child.Process.Signal(signal))
	select {
	case err := <-done:
		waited = true
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		require.Equal(t, exitCode, exit.ExitCode())
	case <-ctx.Done():
		t.Fatal("upload did not stop while the server blocked reads")
	}
	require.NoError(t, ctx.Err())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "Request canceled.")
	entries, err = os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries, "temporary ZIP survived upload cancellation")
	release()
	select {
	case result := <-drained:
		require.Error(t, result.err, "interrupted request unexpectedly completed")
		require.Less(t, result.bytes, length, "the test did not interrupt an active upload")
	case <-time.After(3 * time.Second):
		t.Fatal("interrupted upload left the request body open")
	}
	require.EqualValues(t, 1, requests.Load(), "interrupted upload was retried")
}

func TestMainSkillUploadInterruptDuringPackaging(t *testing.T) {
	runSkillUploadCancellationSignals(t, checkSkillUploadInterruptDuringPackaging)
}

func checkSkillUploadInterruptDuringPackaging(t *testing.T, signal os.Signal, exitCode int) {
	root, tmp, home := t.TempDir(), t.TempDir(), t.TempDir()
	source := filepath.Join(root, "large-synthetic.bin")
	file, err := os.Create(source)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(512<<20))
	require.NoError(t, file.Close())
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected upload before packaging cancellation", http.StatusBadRequest)
	}))
	defer server.Close()
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "skills", "create", "--files", root)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home,
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-skill-cancellation", "OPENAI_BASE_URL="+server.URL,
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "FORCE_COLOR=0")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	require.NoError(t, child.Start())
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	started := false
	for !started {
		select {
		case <-ticker.C:
			err := filepath.WalkDir(tmp, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
					return nil
				}
				archive, err := os.Open(path)
				if err != nil {
					return err
				}
				var signature [4]byte
				count, readErr := archive.Read(signature[:])
				closeErr := archive.Close()
				if closeErr != nil {
					return closeErr
				}
				if readErr != nil && readErr != io.EOF {
					return readErr
				}
				if count == len(signature) && bytes.Equal(signature[:], []byte{'P', 'K', 3, 4}) {
					started = true
					return filepath.SkipAll
				}
				return nil
			})
			require.NoError(t, err)
		case err := <-done:
			waited = true
			t.Fatalf("process exited before observing packaging: %v", err)
		case <-ctx.Done():
			t.Fatal("packaging did not write ZIP bytes before the deadline")
		}
	}
	require.Zero(t, requests.Load(), "fixture finished packaging before cancellation")
	require.NoError(t, child.Process.Signal(signal))
	select {
	case err := <-done:
		waited = true
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		require.Equal(t, exitCode, exit.ExitCode())
	case <-ctx.Done():
		t.Fatal("packaging did not stop on interrupt")
	}
	require.NoError(t, ctx.Err())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "Request canceled.")
	require.Zero(t, requests.Load(), "canceled packaging sent an upload")
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries, "temporary ZIP staging survived packaging cancellation")
	info, err := os.Stat(source)
	require.NoError(t, err)
	require.EqualValues(t, 512<<20, info.Size(), "packaging changed the source file")
}
