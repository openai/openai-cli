//go:build !windows

package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainFilesReceiptKnownFieldsPreserveFullFallback(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for Unix receipt checks")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is required for Unix receipt checks")
	}
	for _, tc := range []struct {
		name, fields string
		receipt      bool
	}{
		{"created string", `"created_at":"invalid"`, false},
		{"created null", `"created_at":null`, false},
		{"expiry object", `"expires_at":{"preserve":"this field"}`, false},
		{"details array", `"status_details":["preserve","these details"]`, false},
		{"details number", `"status_details":12`, false},
		{"nullable fields", `"expires_at":null,"status_details":null`, true},
		{"signed and large timestamps", `"created_at":-1,"expires_at":9223372036854775808`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			const filename = "upload space.txt"
			payload := []byte("hello files!\n")
			require.NoError(t, os.WriteFile(filepath.Join(work, filename), payload, 0o600))
			response := `{"id":"` + filesWorkflowID + `","object":"file","filename":"upload space.txt","purpose":"user_data","bytes":13,` + tc.fields + `}`
			server, requests := filesWorkflowUploadServerWithResponse(t, filename, payload, http.StatusOK, response)
			upload := runFilesReceiptPTY(t, python, bash, work, server.URL, []string{"openai", "files", "upload", filename, "--purpose=user_data"})
			require.Zero(t, upload.code, "%+v", upload)
			require.Empty(t, upload.stderr)
			if tc.receipt {
				require.Contains(t, upload.stdout, "Uploaded "+filename)
				require.Contains(t, upload.stdout, "Inspect it:")
			} else {
				legacy := runFilesReceiptPTY(t, python, bash, work, server.URL, []string{"openai", "files", "create", "--file", filename, "--purpose=user_data"})
				require.Equal(t, legacy, upload, "malformed known fields must retain the complete legacy response rendering")
				require.NotContains(t, upload.stdout, "Uploaded ")
				require.NotContains(t, upload.stdout, "Inspect it:")
			}
			machine := runFilesReceiptPTY(t, python, bash, work, server.URL, []string{"openai", "files", "upload", filename, "--purpose=user_data", "--format=json"})
			require.Zero(t, machine.code, "%+v", machine)
			require.Empty(t, machine.stderr)
			require.JSONEq(t, response, machine.stdout)
			wantRequests := int32(3)
			if tc.receipt {
				wantRequests = 2
			}
			require.Equal(t, wantRequests, requests.Load(), "each process must upload the exact fixture once")
		})
	}
}
