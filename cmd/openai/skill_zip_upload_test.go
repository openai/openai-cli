package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainSkillZIPUpload(t *testing.T) {
	t.Chdir(t.TempDir())
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	member, err := writer.Create("demo/SKILL.md")
	require.NoError(t, err)
	_, err = io.WriteString(member, "---\nname: demo\ndescription: Example skill\n---\n# Demo\n")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, os.WriteFile("@demo.ZIP", archive.Bytes(), 0600))
	require.NoError(t, os.WriteFile("default.txt", []byte("false"), 0600))

	for _, tc := range []struct {
		name, path, filename string
		args                 []string
		stdin                []byte
	}{
		{"named archive", "/skills", "@demo.ZIP", []string{"skills", "create", "--files", "@demo.ZIP"}, nil},
		{"stdin archive", "/skills/skill_demo/versions", "skill.zip", []string{"skills", "versions", "create", "skill_demo", "--default=false", "--files", "-"}, archive.Bytes()},
		{"trusted piped path", "/skills/skill_demo/versions", "@demo.ZIP", []string{"skills:versions", "create", "--skill-id", "skill_demo", "--default=false"}, []byte(`{"files":["@demo.ZIP"]}`)},
		{"scalar file reference", "/skills/skill_demo/versions", "@demo.ZIP", []string{"skills", "versions", "create", "skill_demo", "--files", "@demo.ZIP"}, []byte(`{"default":"@file://default.txt"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const response = `{"id":"skill_demo"}`
			server, requests := globalFlagsServer(t, response)
			got := runShellFileCommand(t, server, shellFileInput(t, tc.stdin), nil, tc.args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.JSONEq(t, response, got.stdout)
			req := globalFlagsOneRequest(t, requests)
			require.Equal(t, http.MethodPost, req.method)
			require.Equal(t, tc.path, req.path)

			wire := httptest.NewRequest(req.method, req.path, bytes.NewReader(req.body))
			wire.Header = req.header
			file, header, err := wire.FormFile("files")
			require.NoError(t, err)
			defer file.Close()
			require.Len(t, wire.MultipartForm.File, 1)
			require.Len(t, wire.MultipartForm.File["files"], 1)
			require.Equal(t, tc.filename, header.Filename)
			require.Equal(t, "application/zip", header.Header.Get("Content-Type"))
			body, err := io.ReadAll(file)
			require.NoError(t, err)
			require.Equal(t, archive.Bytes(), body)
			if tc.path != "/skills" {
				require.Equal(t, []string{"false"}, wire.MultipartForm.Value["default"])
			}
		})
	}
}

func TestMainSkillZIPRequestSelection(t *testing.T) {
	server, requests := globalFlagsServer(t, `{"id":"skill_demo"}`)
	got := runShellFileCommand(t, server, shellFileInput(t, []byte(`{"default":false}`)), nil,
		"skills", "versions", "create", "--skill-id", "skill_demo")
	require.Zero(t, got.code, "%s", got.stderr)
	req := globalFlagsOneRequest(t, requests)
	require.Equal(t, "application/json", req.header.Get("Content-Type"))
	require.JSONEq(t, `{"default":false}`, string(req.body))

	got = runShellFileCommand(t, server, shellFileInput(t, []byte(`{"files":["private.zip"]}`)),
		[]string{"OPENAI_UNTRUSTED_STDIN=true"}, "skills", "create")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "provide --files explicitly")
	require.Empty(t, requests)

	got = runShellFileCommand(t, server, shellFileInput(t, nil), nil,
		"skills", "versions", "create", "--skill-id", "", "--files", "missing.zip")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "nonempty --skill-id")
	require.Empty(t, requests)
}
