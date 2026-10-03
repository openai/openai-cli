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
func TestAudioVoiceVariantInputs(t *testing.T) {
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
	require.NoError(t, os.WriteFile(typeFile, []byte("prompt"), 0600))
	bodyFile := filepath.Join(dir, "voice.yml")
	require.NoError(t, os.WriteFile(bodyFile, []byte("name: Synthetic\ntype: prompt\nprompt: Synthetic narrator\n"), 0600))

	for _, tc := range []struct {
		name, stdin, bodyFile, wantMissing, wantType, wantConflict string
		flags                                                      []string
		wantSample, invalidType                                    bool
	}{
		{name: "prompt missing", flags: []string{"--name", "Synthetic", "--type", "prompt"}, wantMissing: "--prompt"},
		{name: "default missing both", flags: []string{"--name", "Synthetic"}, wantMissing: "--audio-sample, --consent"},
		{name: "sample missing", flags: []string{"--name", "Synthetic", "--consent", "cons_fake"}, wantMissing: "--audio-sample"},
		{name: "consent missing", flags: []string{"--name", "Synthetic", "--audio-sample", sample}, wantMissing: "--consent"},
		{name: "missing piped prompt", stdin: `{"name":"Synthetic","type":"prompt"}`, wantMissing: "--prompt"},
		{name: "missing piped default sample", stdin: `{"name":"Synthetic","consent":"cons_fake"}`, wantMissing: "--audio-sample"},
		{name: "name missing in body", stdin: `{"type":"prompt","prompt":"Synthetic narrator"}`, wantMissing: "--name"},
		{name: "piped prompt", stdin: `{"name":"Synthetic","type":"prompt","prompt":"Synthetic narrator"}`, wantType: "prompt"},
		{name: "redirected YAML body", bodyFile: bodyFile, wantType: "prompt"},
		{name: "sample flags default", flags: []string{"--name", "Synthetic", "--consent", "cons_fake", "--audio-sample", sample}, wantSample: true},
		{name: "piped sample", stdin: "name: Synthetic\ntype: audio_sample\nconsent: cons_fake\naudio_sample: " + sample + "\n", wantType: "audio_sample", wantSample: true},
		{name: "flag switches piped prompt to sample", stdin: `{"name":"Synthetic","type":"prompt","prompt":"Synthetic narrator"}`, flags: []string{"--type", "audio_sample"}, wantMissing: "--audio-sample, --consent"},
		{name: "flag switches piped sample to prompt", stdin: `{"name":"Synthetic","type":"audio_sample"}`, flags: []string{"--type", "prompt", "--prompt", "Synthetic narrator"}, wantType: "prompt"},
		{name: "type from file missing prompt", flags: []string{"--name", "Synthetic", "--type", "@" + typeFile}, wantMissing: "--prompt"},
		{name: "type from file replay", flags: []string{"--name", "Synthetic", "--type", "@" + typeFile, "--prompt", "Synthetic narrator"}, wantType: "prompt"},
		{name: "prompt rejects consent flag", flags: []string{"--name", "Synthetic", "--type", "prompt", "--prompt", "Synthetic narrator", "--consent", "cons_fake"}, wantConflict: "prompt: --consent"},
		{name: "prompt rejects sample flag", flags: []string{"--name", "Synthetic", "--type", "prompt", "--prompt", "Synthetic narrator", "--audio-sample", sample}, wantConflict: "prompt: --audio-sample"},
		{name: "default sample rejects prompt flag", flags: []string{"--name", "Synthetic", "--consent", "cons_fake", "--audio-sample", sample, "--prompt", "synthetic-private-prompt"}, wantConflict: "audio_sample: --prompt"},
		{name: "explicit sample rejects model flag", flags: []string{"--name", "Synthetic", "--type", "audio_sample", "--consent", "cons_fake", "--audio-sample", sample, "--model", "synthetic-private-model"}, wantConflict: "audio_sample: --model"},
		{name: "default sample rejects script flag", flags: []string{"--name", "Synthetic", "--consent", "cons_fake", "--audio-sample", sample, "--script-hint", "synthetic-private-script"}, wantConflict: "audio_sample: --script-hint"},
		{name: "override to prompt rejects piped sample fields", stdin: "name: Synthetic\ntype: audio_sample\nconsent: cons_fake\naudio_sample: " + sample + "\n", flags: []string{"--type", "prompt", "--prompt", "Synthetic narrator"}, wantConflict: "prompt: --audio-sample, --consent"},
		{name: "override to sample rejects piped prompt fields", stdin: `{"name":"Synthetic","type":"prompt","prompt":"synthetic-private-prompt","model":"synthetic-private-model","script_hint":"synthetic-private-script"}`, flags: []string{"--type", "audio_sample", "--consent", "cons_fake", "--audio-sample", sample}, wantConflict: "audio_sample: --prompt, --model, --script-hint"},
		{name: "piped null inactive field rejected", stdin: `{"name":"Synthetic","type":"prompt","prompt":"Synthetic narrator","consent":null}`, wantConflict: "prompt: --consent"},
		{name: "clean override to sample", stdin: `{"name":"Synthetic","type":"prompt"}`, flags: []string{"--type", "audio_sample", "--consent", "cons_fake", "--audio-sample", sample}, wantType: "audio_sample", wantSample: true},
		{name: "unknown flag type", flags: []string{"--name", "Synthetic", "--type", "synthetic-private-unknown"}, invalidType: true},
		{name: "empty explicit type", flags: []string{"--name", "Synthetic", "--type", "", "--consent", "cons_fake", "--audio-sample", sample}, invalidType: true},
		{name: "JSON null type", stdin: `{"name":"Synthetic","type":null,"consent":"cons_fake"}`, flags: []string{"--audio-sample", sample}, invalidType: true},
		{name: "JSON numeric type", stdin: `{"name":"Synthetic","type":17,"prompt":"synthetic-private-prompt"}`, invalidType: true},
		{name: "YAML boolean type", stdin: "name: Synthetic\ntype: true\nprompt: synthetic-private-prompt\n", invalidType: true},
		{name: "JSON array type", stdin: `{"name":"Synthetic","type":["synthetic-private-unknown"]}`, invalidType: true},
		{name: "YAML mapping type", stdin: "name: Synthetic\ntype: {kind: synthetic-private-unknown}\n", invalidType: true},
		{name: "valid flag overrides invalid piped type", stdin: `{"name":"Synthetic","type":["synthetic-private-unknown"]}`, flags: []string{"--type", "prompt", "--prompt", "Synthetic narrator"}, wantType: "prompt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.wantConflict != "" || tc.invalidType {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic","type":"prompt"}`)
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
				} else if tc.wantMissing == "" {
					require.Equal(t, "Synthetic narrator", r.FormValue("prompt"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic","type":"prompt"}`)
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
				require.Contains(t, string(output), "--type must be audio_sample or prompt")
				require.NotContains(t, string(output), "synthetic-private-")
			} else if tc.wantConflict != "" {
				require.Error(t, err, "inactive variant fields accepted: %s", output)
				require.Zero(t, requests.Load(), "conflicting fields must not reach HTTP")
				require.Contains(t, string(output), "Options not supported for voice type "+tc.wantConflict+".")
				require.NotContains(t, string(output), "synthetic-private-")
			} else {
				require.NoError(t, err, "%s", output)
				require.EqualValues(t, 1, requests.Load())
			}
		})
	}
}
