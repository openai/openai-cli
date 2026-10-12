package cmd

import (
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

// Exercise fresh command instances so flag state and stdin consumption match
// real invocations, including redirection from files.
func TestAudioVoiceInputs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-api")
	t.Setenv("OPENAI_ADMIN_KEY", "")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	t.Setenv("OPENAI_BASE_URL", "")
	dir := t.TempDir()
	binary := filepath.Join(dir, "openai-test")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/openai")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)

	sample := filepath.Join(dir, "sample.wav")
	require.NoError(t, os.WriteFile(sample, []byte("synthetic sample bytes"), 0600))
	typeFile := filepath.Join(dir, "type.txt")
	require.NoError(t, os.WriteFile(typeFile, []byte("audio_sample"), 0600))
	bodyFile := filepath.Join(dir, "voice.yml")
	require.NoError(t, os.WriteFile(bodyFile, []byte("name: Synthetic\ntype: audio_sample\nconsent: cons_fake\naudio_sample: "+sample+"\n"), 0600))

	for _, tc := range []struct {
		name, stdin, bodyFile, wantMissing, wantType string
		flags                                        []string
		wantSample, invalidType                      bool
	}{
		{name: "default missing both", flags: []string{"--name", "Synthetic"}, wantMissing: "--audio-sample, --consent"},
		{name: "sample missing", flags: []string{"--name", "Synthetic", "--consent", "cons_fake"}, wantMissing: "--audio-sample"},
		{name: "consent missing", flags: []string{"--name", "Synthetic", "--audio-sample", sample}, wantMissing: "--consent"},
		{name: "missing piped sample", stdin: `{"name":"Synthetic","consent":"cons_fake"}`, wantMissing: "--audio-sample"},
		{name: "name missing in body", stdin: `{"consent":"cons_fake"}`, flags: []string{"--audio-sample", sample}, wantMissing: "--name"},
		{name: "redirected YAML body", bodyFile: bodyFile, wantType: "audio_sample", wantSample: true},
		{name: "sample flags default", flags: []string{"--name", "Synthetic", "--consent", "cons_fake", "--audio-sample", sample}, wantSample: true},
		{name: "piped sample", stdin: "name: Synthetic\ntype: audio_sample\nconsent: cons_fake\naudio_sample: " + sample + "\n", wantType: "audio_sample", wantSample: true},
		{name: "type from file replay", flags: []string{"--name", "Synthetic", "--type", "@" + typeFile, "--consent", "cons_fake", "--audio-sample", sample}, wantType: "audio_sample", wantSample: true},
		{name: "unknown flag type", flags: []string{"--name", "Synthetic", "--type", "synthetic-private-unknown", "--consent", "cons_fake", "--audio-sample", sample}, invalidType: true},
		{name: "empty explicit type", flags: []string{"--name", "Synthetic", "--type", "", "--consent", "cons_fake", "--audio-sample", sample}, invalidType: true},
		{name: "JSON null type", stdin: `{"name":"Synthetic","type":null,"consent":"cons_fake"}`, flags: []string{"--audio-sample", sample}, invalidType: true},
		{name: "JSON numeric type", stdin: `{"name":"Synthetic","type":17,"consent":"cons_fake"}`, flags: []string{"--audio-sample", sample}, invalidType: true},
		{name: "YAML boolean type", stdin: "name: Synthetic\ntype: true\nconsent: cons_fake\n", flags: []string{"--audio-sample", sample}, invalidType: true},
		{name: "JSON array type", stdin: `{"name":"Synthetic","type":["synthetic-private-unknown"],"consent":"cons_fake"}`, flags: []string{"--audio-sample", sample}, invalidType: true},
		{name: "YAML mapping type", stdin: "name: Synthetic\ntype: {kind: synthetic-private-unknown}\nconsent: cons_fake\n", flags: []string{"--audio-sample", sample}, invalidType: true},
		{name: "valid flag overrides invalid piped type", stdin: `{"name":"Synthetic","type":["synthetic-private-unknown"]}`, flags: []string{"--type", "audio_sample", "--consent", "cons_fake", "--audio-sample", sample}, wantType: "audio_sample", wantSample: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.invalidType {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic","type":"audio_sample"}`)
					return
				}
				require.Equal(t, "/audio/voices", r.URL.Path)
				require.NoError(t, r.ParseMultipartForm(1<<20))
				defer r.MultipartForm.RemoveAll()
				require.Equal(t, "Synthetic", r.FormValue("name"))
				gotType := r.FormValue("type")
				if f, _, err := r.FormFile("type"); err == nil {
					content, err := io.ReadAll(f)
					require.NoError(t, err)
					require.NoError(t, f.Close())
					gotType = string(content)
				}
				require.Equal(t, tc.wantType, gotType)
				if tc.wantSample {
					require.Equal(t, "cons_fake", r.FormValue("consent"))
					f, _, err := r.FormFile("audio_sample")
					require.NoError(t, err)
					content, err := io.ReadAll(f)
					require.NoError(t, err)
					require.NoError(t, f.Close())
					require.Equal(t, "synthetic sample bytes", string(content))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic","type":"audio_sample"}`)
			}))
			defer server.Close()
			args := append([]string{"--base-url", server.URL + "/", "audio:voices", "create"}, tc.flags...)
			command := exec.Command(binary, args...)
			if tc.bodyFile != "" {
				f, err := os.Open(tc.bodyFile)
				require.NoError(t, err)
				defer f.Close()
				command.Stdin = f
			} else if tc.stdin != "" {
				command.Stdin = strings.NewReader(tc.stdin)
			}
			output, err := command.CombinedOutput()
			if tc.wantMissing != "" {
				require.Error(t, err, "missing input accepted: %s", output)
				require.Zero(t, requests.Load(), "invalid requests must not reach HTTP")
				require.Contains(t, string(output), "Missing required options: "+tc.wantMissing+".")
			} else if tc.invalidType {
				require.Error(t, err, "unsupported voice type accepted: %s", output)
				require.Zero(t, requests.Load(), "invalid type must not reach HTTP")
				require.Contains(t, string(output), "--type must be audio_sample")
				require.NotContains(t, string(output), "synthetic-private-")
			} else {
				require.NoError(t, err, "%s", output)
				require.EqualValues(t, 1, requests.Load())
			}
		})
	}
}
