//go:build !windows

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainAutomaticDownloadFilenameControls(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for automatic-download PTY checks")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is required for automatic-download PTY checks")
	}
	payload := []byte{0, 255, 254, 'b', 'i', 'n', '\n'}
	for _, filename := range []struct{ name, encoded, want string }{
		{"layout", "report%0AForged%09status.bin", "report_Forged_status.bin"},
		{"terminal", "report%1B%5D52%3Bc%3Bsynthetic%07%0D%C2%9B%7F.bin", "report_]52;c;synthetic____.bin"},
		{"unicode and basename", "..%2F..%2Fr%C3%A9sum%C3%A9%20%E6%97%A5%E6%9C%AC.bin", "résumé 日本.bin"},
	} {
		t.Run(filename.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+filename.encoded)
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			for _, command := range shellBinarySaveCommands() {
				t.Run(strings.Join(command, " "), func(t *testing.T) {
					work := t.TempDir()
					args := append([]string{"openai"}, command...)
					got := runFilesReceiptPTY(t, python, bash, work, server.URL, args)
					require.Zero(t, got.code, "%+v", got)
					require.Empty(t, got.stdout)
					require.Equal(t, "Wrote output to: "+filename.want+"\n", got.stderr)
					path := filepath.Join(work, filename.want)
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, payload, data)
					info, err := os.Stat(path)
					require.NoError(t, err)
					require.Equal(t, os.FileMode(0600), info.Mode().Perm())
				})
			}
		})
	}
}
