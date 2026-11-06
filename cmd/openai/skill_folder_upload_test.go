package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainSkillFolderUpload(t *testing.T) {
	parent := t.TempDir()
	const folder = "demo skill"
	dir := filepath.Join(parent, folder)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".assets"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outputs", "cache"), 0700))
	want := map[string]string{
		folder + "/SKILL.md":         "---\nname: demo-skill\ndescription: Synthetic fixture\n---\n",
		folder + "/.assets/data.bin": "\x00\xff\x01",
		folder + "/run helper.sh":    "#!/bin/sh\nexit 0\n",
	}
	for name, data := range want {
		require.NoError(t, os.WriteFile(filepath.Join(parent, filepath.FromSlash(name)), []byte(data), 0600))
	}
	require.NoError(t, os.Chmod(filepath.Join(dir, "run helper.sh"), 0755))

	for _, tc := range []struct {
		name, cwd, route, response string
		args                       []string
	}{
		{"create from current folder", dir, "/skills", `{"id":"skill_demo","default_version":"1"}`,
			[]string{"skills", "create", "--files", "."}},
		{"publish another version", parent, "/skills/skill_demo/versions", `{"id":"sv_demo","version":"2"}`,
			[]string{"skills", "versions", "create", "skill_demo", "--files", folder + string(filepath.Separator), "--default=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(tc.cwd)
			server, requests := globalFlagsServer(t, tc.response)
			got := runShellFileCommand(t, server, nil, nil, tc.args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.JSONEq(t, tc.response, got.stdout)
			req := globalFlagsOneRequest(t, requests)
			require.Equal(t, http.MethodPost, req.method)
			require.Equal(t, tc.route, req.path)
			wire := httptest.NewRequest(req.method, req.path, bytes.NewReader(req.body))
			wire.Header = req.header
			part, header, err := wire.FormFile("files")
			require.NoError(t, err)
			defer part.Close()
			require.Len(t, wire.MultipartForm.File, 1)
			require.Len(t, wire.MultipartForm.File["files"], 1)
			require.Equal(t, folder+".zip", header.Filename)
			require.Equal(t, "application/zip", header.Header.Get("Content-Type"))
			if tc.route != "/skills" {
				require.Equal(t, []string{"false"}, wire.MultipartForm.Value["default"])
			}
			archive, err := zip.NewReader(part, header.Size)
			require.NoError(t, err)
			files := map[string]string{}
			var directories []string
			for _, entry := range archive.File {
				if entry.FileInfo().IsDir() {
					directories = append(directories, entry.Name)
					continue
				}
				require.NotContains(t, files, entry.Name)
				reader, err := entry.Open()
				require.NoError(t, err)
				data, err := io.ReadAll(reader)
				require.NoError(t, reader.Close())
				require.NoError(t, err)
				files[entry.Name] = string(data)
				if entry.Name == folder+"/run helper.sh" && runtime.GOOS != "windows" {
					require.Equal(t, os.FileMode(0111), entry.Mode().Perm()&0111)
				}
			}
			require.Equal(t, want, files)
			require.Contains(t, directories, folder+"/outputs/cache/")
		})
	}
	t.Run("service failure does not replay upload", func(t *testing.T) {
		server, requests := globalFlagsServer(t, `{"error":{"message":"synthetic failure"}}`, http.StatusServiceUnavailable)
		got := runShellFileCommand(t, server, nil, nil, "skills", "versions", "create", "skill_demo", "--files", dir)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		require.NotEmpty(t, got.stderr)
		globalFlagsOneRequest(t, requests)
	})
}

func TestMainSkillFolderInputErrors(t *testing.T) {
	local := t.TempDir()
	t.Chdir(local)
	require.NoError(t, os.Mkdir("empty", 0700))
	server, requests := globalFlagsServer(t, `{"id":"unexpected"}`)
	check := func(t *testing.T, input *os.File, env []string, args ...string) string {
		t.Helper()
		got := runShellFileCommand(t, server, input, env, append([]string{"skills", "create"}, args...)...)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		require.Contains(t, got.stderr, "--files")
		require.Empty(t, requests)
		return got.stderr
	}
	t.Run("empty", func(t *testing.T) { check(t, nil, nil, "--files", "empty") })
	t.Run("missing", func(t *testing.T) { check(t, nil, nil, "--files", "absent") })
	t.Run("untrusted input", func(t *testing.T) {
		diagnostic := check(t, shellFileInput(t, []byte(`{"files":["empty"]}`)), []string{"OPENAI_UNTRUSTED_STDIN=true"})
		require.Contains(t, diagnostic, "provide --files explicitly")
	})
	t.Run("symlink member", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join("empty", "SKILL.md"), []byte("# Synthetic skill"), 0600))
		require.NoError(t, os.Mkdir(filepath.Join("empty", "refs"), 0700))
		target := filepath.Join(local, "private-target")
		require.NoError(t, os.WriteFile(target, []byte("private fixture"), 0600))
		member := "refs/link"
		if runtime.GOOS != "windows" {
			member += `"quoted`
		}
		err := os.Symlink(target, filepath.Join("empty", filepath.FromSlash(member)))
		if runtime.GOOS == "windows" && os.IsPermission(err) {
			t.Skip("symlink creation requires Windows privileges")
		}
		require.NoError(t, err)
		diagnostic := check(t, nil, nil, "--files", filepath.Join(local, "empty"))
		var result struct{ Message string }
		require.NoError(t, json.Unmarshal([]byte(diagnostic), &result))
		require.Contains(t, result.Message, strconv.Quote(member))
		require.NotContains(t, result.Message, local)
		require.NotContains(t, result.Message, "private-target")
	})
}

func TestMainSkillFolderHelp(t *testing.T) {
	for _, route := range []string{"skills create", "skills versions create"} {
		t.Run(route, func(t *testing.T) {
			got := runMainDispatch(t, "", strings.Fields("openai "+route+" --help")...)
			require.Zero(t, got.code, "%s", got.stderr)
			help := strings.Join(strings.Fields(got.stdout), " ")
			require.Contains(t, help, "hidden files")
			full := runMainDispatch(t, "", strings.Fields("openai help --all "+route)...)
			require.Zero(t, full.code, "%s", full.stderr)
			require.Contains(t, strings.Join(strings.Fields(full.stdout), " "), "symbolic links")
		})
	}
}
