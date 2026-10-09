//go:build !windows

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// Execute the receipt through Bash after changing the upload source. The copied
// command must inspect metadata in the same request context without saving bytes.
func TestMainFilesReceiptFollowupPreservesSourceAndContext(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for Unix receipt checks")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is required for Unix receipt checks")
	}
	for _, name := range []string{"default", "context before command", "context after command", "empty project", "processing error", "inspection fails", "API key override", "header override", "URL query"} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			const filename = "upload space.txt"
			original := []byte("hello files!\n")
			require.NoError(t, os.WriteFile(filepath.Join(work, filename), original, 0o600))
			status := "uploaded"
			if name == "processing error" {
				status = "error"
			}
			response := fmt.Sprintf(`{"id":%q,"object":"file","bytes":13,"filename":%q,"purpose":"user_data","status":%q}`, filesWorkflowID, filename, status)
			var uploads, inspections, downloads atomic.Int32
			project, organization := "", ""
			key := "sk-fake-files-receipt-test"
			if strings.HasPrefix(name, "context ") || name == "processing error" {
				project, organization = "project-'quoted", "org-one"
			}
			if name == "API key override" {
				key = "sk-fake-private-override"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("OpenAI-Project") != project || r.Header.Get("OpenAI-Organization") != organization {
					t.Errorf("request context changed: project=%q organization=%q", r.Header.Get("OpenAI-Project"), r.Header.Get("OpenAI-Organization"))
				}
				if r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("unexpected synthetic credential selection")
				}
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/files":
					uploads.Add(1)
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					file, header, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						return
					}
					defer file.Close()
					body, err := io.ReadAll(file)
					if err != nil || string(body) != string(original) || header.Filename != filename || r.FormValue("purpose") != "user_data" {
						t.Error("multipart filename, purpose, or bytes changed")
					}
				case r.Method == http.MethodGet && r.URL.Path == "/files/"+filesWorkflowID:
					inspections.Add(1)
					if name == "inspection fails" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusNotFound)
						_, _ = io.WriteString(w, `{"error":{"message":"Synthetic file missing."}}`)
						return
					}
				case r.Method == http.MethodGet && r.URL.Path == "/files/"+filesWorkflowID+"/content":
					downloads.Add(1)
					w.Header().Set("Content-Type", "application/octet-stream")
					_, _ = w.Write(original)
					return
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response)
			}))
			defer server.Close()
			inheritedEndpoint := server.URL
			environment := map[string]string{}
			var flags []string
			if project != "" {
				flags = []string{"--project=" + project, "--organization=" + organization, "--base-url=" + server.URL}
				// A missing copied base URL must fail locally, without contacting another backend.
				inheritedEndpoint = "http://127.0.0.1:1"
			}
			switch name {
			case "empty project":
				flags = []string{"--project="}
				environment["OPENAI_PROJECT_ID"] = "project-inherited"
			case "API key override":
				flags = []string{"--api-key=" + key}
			case "header override":
				flags = []string{"--header=X-Synthetic: fake-private-header"}
			case "URL query":
				flags = []string{"--base-url=" + server.URL + "?token=fake-private-query"}
			}
			args := []string{"openai", "files", "upload", filename, "--purpose=user_data"}
			if name == "context before command" {
				args = append(append([]string{"openai"}, flags...), args[1:]...)
			} else {
				args = append(args, flags...)
			}
			receipt := runFilesReceiptPTY(t, python, bash, work, inheritedEndpoint, args, environment)
			require.Zero(t, receipt.code, "%+v", receipt)
			require.Empty(t, receipt.stderr)
			require.Contains(t, receipt.stdout, "Uploaded "+filename)
			require.EqualValues(t, 1, uploads.Load())
			for _, private := range []string{"sk-fake-", "fake-private-header", "fake-private-query"} {
				require.NotContains(t, receipt.stdout, private)
			}
			_, command, found := strings.Cut(receipt.stdout, "\nInspect it: ")
			if name == "API key override" || name == "header override" || name == "URL query" {
				require.False(t, found)
				require.NotContains(t, receipt.stdout, "Download it:")
				return
			}
			require.True(t, found, receipt.stdout)
			require.NotContains(t, command, "--output")
			require.NotContains(t, command, " download ")
			changed := []byte("local edits after the upload\n")
			require.NoError(t, os.WriteFile(filepath.Join(work, filename), changed, 0o600))
			inspection := runFilesReceiptPTYCommand(t, python, bash, work, inheritedEndpoint, strings.TrimSpace(command), nil, environment)
			if name == "inspection fails" {
				require.NotZero(t, inspection.code)
				require.Contains(t, inspection.stderr, "HTTP 404:")
				require.Contains(t, inspection.stderr, "Check the resource or model ID.")
				require.Empty(t, inspection.stdout)
			} else {
				require.Zero(t, inspection.code, "%+v", inspection)
				require.Empty(t, inspection.stderr)
				if name == "processing error" {
					require.JSONEq(t, response, inspection.stdout)
				} else {
					require.Contains(t, inspection.stdout, filesWorkflowID)
				}
			}
			contents, err := os.ReadFile(filepath.Join(work, filename))
			require.NoError(t, err)
			require.Equal(t, changed, contents)
			require.EqualValues(t, 1, inspections.Load())
			require.Zero(t, downloads.Load())
			entries, err := os.ReadDir(work)
			require.NoError(t, err)
			require.Len(t, entries, 1, "read-only follow-up must not create a destination")
		})
	}
}
