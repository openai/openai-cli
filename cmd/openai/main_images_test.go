package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainImagesGeneratePreservesExplicitAndPipedOutput(t *testing.T) {
	var downloads, generations atomic.Int32
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		http.Error(w, "unexpected image download", http.StatusInternalServerError)
	}))
	defer imageServer.Close()
	imageURL := imageServer.URL + "/generated.png?signature=synthetic-token"
	response := fmt.Sprintf(`{"created":123,"data":[{"url":%q},{"b64_json":"synthetic-base64"}]}`, imageURL)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generations.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/images/generations" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-test-key" {
			t.Error("request did not use the synthetic API key")
		}
		var body struct{ Prompt string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt != "synthetic image" {
			t.Error("image generation request did not preserve its prompt")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	for _, test := range []struct {
		name  string
		flags []string
		want  string
		exact bool
	}{
		{name: "default piped output", want: response},
		{name: "explicit JSON", flags: []string{"--format", "json"}, want: response},
		{name: "explicit auto", flags: []string{"--format", "auto"}, want: response},
		{name: "explicit raw", flags: []string{"--format", "raw"}, want: response + "\n", exact: true},
		{name: "extraction", flags: []string{"--transform", "data.0.url"}, want: strconv.Quote(imageURL)},
		{name: "raw extraction", flags: []string{"--transform", "data.0.url", "--raw-output"}, want: imageURL + "\n", exact: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := generations.Load()
			args := []string{"openai", "--base-url", server.URL, "--api-key", "synthetic-test-key"}
			args = append(args, test.flags...)
			args = append(args, "images", "generate", "--prompt", "synthetic image")
			got := runMainDispatchWithEnv(t, "", []string{
				"FORCE_COLOR=0", "TERM=xterm-kitty", "TERM_PROGRAM=kitty", "TMUX=",
			}, args...)
			require.Equal(t, 0, got.code, "stderr: %s", got.stderr)
			require.Empty(t, got.stderr)
			if test.exact {
				require.Equal(t, test.want, got.stdout)
			} else {
				require.JSONEq(t, test.want, got.stdout)
			}
			require.Equal(t, before+1, generations.Load())
			require.Zero(t, downloads.Load(), "preserving JSON must not download images")
		})
	}
}
