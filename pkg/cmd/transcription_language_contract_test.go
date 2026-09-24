package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestTranscriptionLanguageFlags(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai").CombinedOutput(); err != nil {
		t.Fatalf("building CLI: %v\n%s", err, output)
	}
	file := filepath.Join(t.TempDir(), "synthetic.wav")
	if err := os.WriteFile(file, []byte("synthetic audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		flags     []string
		language  []string
		languages []string
	}{
		{name: "scalar", flags: []string{"--language", "en"}, language: []string{"en"}},
		{name: "repeated array", flags: []string{"--languages", "en", "--languages", "fr"}, languages: []string{"en", "fr"}},
		{name: "both", flags: []string{"--language", "de", "--languages", "en", "--languages", "fr"}, language: []string{"de"}, languages: []string{"en", "fr"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan map[string][]string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Errorf("parsing transcription multipart request: %v", err)
					http.Error(w, "invalid multipart request", http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				received <- r.MultipartForm.Value
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"text":"synthetic transcription"}`)
			}))
			t.Cleanup(server.Close)
			args := []string{"--base-url", server.URL, "--api-key", "synthetic-test-key", "audio:transcriptions", "create", "--file", file, "--model", "gpt-transcribe"}
			args = append(args, test.flags...)
			command := exec.CommandContext(t.Context(), binary, args...)
			// Exclude local credentials, endpoint overrides, proxies, and mTLS settings.
			command.Env = []string{"FORCE_COLOR=0"}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("CLI(%q): %v\n%s", test.flags, err, output)
			}
			select {
			case fields := <-received:
				if got := fields["language"]; !slices.Equal(got, test.language) {
					t.Errorf("CLI(%q) language = %v, want %v", test.flags, got, test.language)
				}
				if got := fields["languages[]"]; !slices.Equal(got, test.languages) {
					t.Errorf("CLI(%q) languages[] = %v, want %v", test.flags, got, test.languages)
				}
			default:
				t.Fatalf("CLI(%q) sent no request", test.flags)
			}
		})
	}
}
