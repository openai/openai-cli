package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/mocktest"
	"github.com/stretchr/testify/require"
)

// Exercise the executable against synthetic HTTP responses, including the cursor
// selected from the preceding page rather than a response-level cursor field.
func TestAudioVoiceConsentsLifecycleContract(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	mocktest.CopyCLI(t, binary)
	recording := []byte("RIFF\x00\xffsynthetic consent")
	filename := filepath.Join(directory, "consent.wav")
	require.NoError(t, os.WriteFile(filename, recording, 0600))
	type request struct {
		method, path, contentType string
		query                     url.Values
		body                      []byte
	}
	requests := make(chan request, 16)
	const cursor = "cons_1+ /?&"
	consent := func(id string) map[string]any {
		return map[string]any{"id": id, "object": "audio.voice_consent", "name": "Owned", "language": "en-US", "created_at": 1734220800}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read synthetic request: %v", readErr)
			w.WriteHeader(500)
			return
		}
		select {
		case requests <- request{r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.URL.Query(), body}:
		default:
			t.Error("unexpected excess requests")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any = consent("cons_1")
		switch {
		case strings.HasSuffix(r.URL.Path, "/missing"):
			w.WriteHeader(http.StatusNotFound)
			result = map[string]any{"error": map[string]string{"message": "Missing consent", "type": "invalid_request_error"}}
		case r.Method == http.MethodDelete:
			result = map[string]any{"id": "cons_1", "object": "audio.voice_consent", "deleted": true}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/audio/voice_consents":
			id := cursor
			if r.URL.Query().Get("after") != "" {
				id = "cons_2"
			}
			result = map[string]any{"object": "list", "data": []any{consent(id)}, "has_more": id == cursor}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cons_1"):
			c := consent("cons_1")
			c["name"] = "Renamed"
			result = c
		case r.URL.Path == "/v1/audio/voices":
			result = map[string]any{"id": "voice_1", "object": "audio.voice", "name": "Owned", "type": "audio_sample"}
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("write synthetic response: %v", err)
		}
	}))
	defer server.Close()
	run := func(success bool, args ...string) string {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		flags := []string{"--base-url", server.URL + "/v1", "--api-key", "sk-synthetic", "--format", "json"}
		command := exec.CommandContext(ctx, binary, append(flags, args...)...)
		// Exclude ambient credentials, mTLS, endpoint/proxy overrides, and
		// shell/configuration settings. Authentication is explicitly synthetic.
		command.Env = []string{
			"HOME=" + directory,
			"USERPROFILE=" + directory,
			"OPENAI_CONFIG_HOME=" + directory,
			"FORCE_COLOR=0",
		}
		out, err := command.CombinedOutput()
		if success {
			require.NoError(t, err, "%s", out)
		} else {
			require.Error(t, err, "%s", out)
		}
		return string(out)
	}
	next := func(method, path string) request {
		t.Helper()
		select {
		case got := <-requests:
			require.Equal(t, method, got.method)
			require.Equal(t, path, got.path)
			return got
		default:
			t.Fatal("command sent no request")
			return request{}
		}
	}
	require.Contains(t, run(true, "audio:voice-consents", "create", "--language", "en-US", "--name", "Owned", "--recording", filename), "cons_1")
	created := next("POST", "/v1/audio/voice_consents")
	mediaType, params, err := mime.ParseMediaType(created.contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(strings.NewReader(string(created.body)), params["boundary"])
	parts := map[string][]byte{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		value, err := io.ReadAll(part)
		require.NoError(t, err)
		parts[part.FormName()] = value
		if part.FormName() == "recording" {
			require.Equal(t, "consent.wav", part.FileName())
			require.Contains(t, []string{"audio/wav", "audio/x-wav"}, part.Header.Get("Content-Type"))
		}
	}
	require.Equal(t, map[string][]byte{"recording": recording, "name": []byte("Owned"), "language": []byte("en-US")}, parts)
	require.Contains(t, run(true, "audio:voice-consents", "retrieve", "cons_1"), "cons_1")
	next("GET", "/v1/audio/voice_consents/cons_1")
	require.Contains(t, run(true, "audio:voice-consents", "update", "cons_1", "--name", "Renamed"), "Renamed")
	require.JSONEq(t, `{"name":"Renamed"}`, string(next("POST", "/v1/audio/voice_consents/cons_1").body))
	require.Contains(t, run(true, "audio:voice-consents", "delete", "cons_1"), `"deleted": true`)
	next("DELETE", "/v1/audio/voice_consents/cons_1")
	require.Contains(t, run(true, "audio:voice-consents", "list", "--limit", "1"), "cons_2")
	require.Equal(t, url.Values{"limit": {"1"}}, next("GET", "/v1/audio/voice_consents").query)
	require.Equal(t, url.Values{"limit": {"1"}, "after": {cursor}}, next("GET", "/v1/audio/voice_consents").query)
	require.Contains(t, run(false, "audio:voice-consents", "retrieve", "missing"), "Missing consent")
	next("GET", "/v1/audio/voice_consents/missing")
	require.Contains(t, run(true, "audio:voices", "create", "--type", "audio_sample", "--name", "Owned", "--consent", "cons_1", "--audio-sample", filename), "voice_1")
	next("POST", "/v1/audio/voices")
	require.Empty(t, requests, "unexpected extra request or retry")
}
