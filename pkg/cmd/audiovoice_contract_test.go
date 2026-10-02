package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// Prompt creation uses the same multipart SDK endpoint as audio samples, but
// must transmit text fields without requiring a consent ID or an uploaded file.
func TestAudioVoicesCreatePromptContract(t *testing.T) {
	type voiceRequest struct {
		method, path string
		fields       map[string][]string
		fileCount    int
	}
	received := make(chan voiceRequest, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parsing voice multipart request: %v", err)
			http.Error(w, "invalid multipart request", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		received <- voiceRequest{r.Method, r.URL.Path, r.MultipartForm.Value, len(r.MultipartForm.File)}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic narrator","type":"prompt"}`)
	}))
	t.Cleanup(server.Close)

	// Trust only the httptest certificate for this test's HTTPS endpoint.
	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	create := runtimeTestCommand("audio:voices", "create")
	command := &cli.Command{
		Name: "openai",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "api-key"},
			&cli.StringFlag{Name: "format", Value: "json"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
		Commands: []*cli.Command{{Name: "audio:voices", Commands: []*cli.Command{&create}}},
	}
	require.NoError(t, command.Run(t.Context(), []string{
		"openai", "--base-url", server.URL + "/", "--api-key", "synthetic-test-key",
		"audio:voices", "create", "--type", "prompt", "--name", "Synthetic narrator",
		"--prompt", "A calm narrator with clear delivery", "--model", "2026-10-01",
		"--script-hint", "This is a long enough synthetic script for voice creation.",
	}))
	select {
	case got := <-received:
		require.Equal(t, http.MethodPost, got.method)
		require.Equal(t, "/audio/voices", got.path)
		require.Equal(t, map[string][]string{
			"type":        {"prompt"},
			"name":        {"Synthetic narrator"},
			"prompt":      {"A calm narrator with clear delivery"},
			"model":       {"2026-10-01"},
			"script_hint": {"This is a long enough synthetic script for voice creation."},
		}, got.fields)
		require.Zero(t, got.fileCount)
	default:
		t.Fatal("voice prompt command sent no request")
	}
}

func TestAudioVoicesCreateRequiresName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"prompt", []string{"--type", "prompt", "--prompt", "A calm synthetic narrator"}},
		{"audio_sample", []string{"--type", "audio_sample", "--consent", "cons_synthetic",
			"--audio-sample", mocktest.TestFile(t, "Synthetic voice sample")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "synthetic-api")
			t.Setenv("OPENAI_ADMIN_KEY", "")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic","type":"prompt"}`)
			}))
			defer server.Close()

			// A new CLI process gives every case fresh request flags, as in actual use.
			args := []string{"run", "../../cmd/openai", "--base-url", server.URL + "/", "audio:voices", "create"}
			command := exec.Command("go", append(args, tc.flags...)...)
			output, err := command.CombinedOutput()
			require.Error(t, err, "missing voice name must be rejected locally: %s", output)
			require.Contains(t, string(output), "Missing required options: --name.")
			require.Zero(t, requests.Load(), "invalid voice requests must not reach the server")
		})
	}
}
