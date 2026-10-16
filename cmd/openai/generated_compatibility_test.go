package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
)

// These tests characterize the public CLI before relocating generated-command
// patches. Keep expectations independent of the custom helpers.
func TestMainCertificateCompatibility(t *testing.T) {
	file := filepath.Join(t.TempDir(), "content.txt")
	require.NoError(t, os.WriteFile(file, []byte("synthetic file content"), 0600))
	for _, tc := range []struct {
		name, stdin, failure string
		flags                []string
		want                 map[string]any
	}{
		{name: "certificate", flags: []string{"--certificate", "synthetic certificate"}, want: map[string]any{"certificate": "synthetic certificate"}},
		{name: "both fields", flags: []string{"--certificate", "synthetic certificate", "--content", "synthetic content"}, want: map[string]any{"certificate": "synthetic certificate", "content": "synthetic content"}},
		{name: "hidden flag does not replace required flag", flags: []string{"--content", "synthetic content"}, failure: "certificate"},
		{name: "missing required flag", failure: "certificate"},
		{name: "missing content argument", flags: []string{"--certificate", "synthetic certificate", "--content"}, failure: "content"},
		{name: "repeated content", flags: []string{"--certificate", "synthetic certificate", "--content", "first", "--content", "last"}, want: map[string]any{"certificate": "synthetic certificate", "content": "last"}},
		{name: "file content", flags: []string{"--certificate", "synthetic certificate", "--content", "@" + file}, want: map[string]any{"certificate": "synthetic certificate", "content": "synthetic file content"}},
		{name: "double at content is literal", flags: []string{"--certificate", "synthetic certificate", "--content", "@@literal"}, want: map[string]any{"certificate": "synthetic certificate", "content": "@@literal"}},
		{name: "escaped file content", flags: []string{"--certificate", "synthetic certificate", "--content", `\@literal`}, want: map[string]any{"certificate": "synthetic certificate", "content": "@literal"}},
		{name: "JSON stdin", stdin: `{"certificate":"stdin certificate","content":"stdin content"}`, want: map[string]any{"certificate": "stdin certificate", "content": "stdin content"}},
		{name: "YAML stdin with explicit content", stdin: "certificate: stdin certificate\ncontent: stdin content\n", flags: []string{"--content", "explicit content"}, want: map[string]any{"certificate": "stdin certificate", "content": "explicit content"}},
		{name: "stdin content is literal", stdin: `{"certificate":"stdin certificate","content":"@not-a-file"}`, want: map[string]any{"certificate": "stdin certificate", "content": "@not-a-file"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/organization/certificates" {
					t.Errorf("unexpected route: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer fake-admin-key" {
					t.Error("certificate command did not use the synthetic admin key")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"cert_synthetic","object":"certificate","name":"synthetic"}`)
			}))
			defer server.Close()
			args := []string{"openai", "--base-url", server.URL, "--api-key", "fake-project-key", "--admin-api-key", "fake-admin-key", "--format", "json", "admin:organization:certificates", "create"}
			var stdin *os.File
			if tc.stdin != "" {
				path := filepath.Join(t.TempDir(), "stdin.json")
				require.NoError(t, os.WriteFile(path, []byte(tc.stdin), 0600))
				var err error
				stdin, err = os.Open(path)
				require.NoError(t, err)
				defer stdin.Close()
			}
			got := runMainDispatchWithStdin(t, "bash", nil, stdin, append(args, tc.flags...)...)
			if tc.failure != "" {
				require.Equal(t, 1, got.code)
				require.Contains(t, got.stderr, tc.failure)
				require.Empty(t, requests, "invalid input must not send a request")
				return
			}
			require.Equal(t, 0, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.JSONEq(t, `{"id":"cert_synthetic","object":"certificate","name":"synthetic"}`, got.stdout)
			require.Len(t, requests, 1)
			require.Equal(t, tc.want, <-requests)
		})
	}
}

func TestMainGeneratedCompatibilityHelpAndCompletion(t *testing.T) {
	const fidelityHelp = "Control how much effort the model will exert to match the style and features, especially facial features, of input images. Supports `high` and `low` on `gpt-image-1` and `gpt-image-1.5`; `gpt-image-1-mini` supports only `low`. For `gpt-image-2`, omit this parameter. Defaults to `low` on supported models."
	edit := commands.Command.Command("images").Command("edit")
	found := false
	for _, flag := range edit.Flags {
		if flag, ok := flag.(*requestflag.Flag[*string]); ok && flag.Name == "input-fidelity" {
			found = true
			require.Equal(t, fidelityHelp, flag.Usage)
		}
	}
	require.True(t, found, "input-fidelity must remain available")
	help := runMainDispatch(t, "bash", "openai", "help", "--all", "images", "edit")
	require.Equal(t, 0, help.code)
	require.Empty(t, help.stderr)
	require.Contains(t, strings.Join(strings.Fields(help.stdout), " "), strings.ReplaceAll(fidelityHelp, "`", ""))
	certificateHelp := runMainDispatch(t, "bash", "openai", "help", "--all", "admin:organization:certificates", "create")
	require.Equal(t, 0, certificateHelp.code)
	require.Empty(t, certificateHelp.stderr)
	require.Contains(t, certificateHelp.stdout, "--certificate")
	require.NotContains(t, certificateHelp.stdout, "--content")
	for _, tc := range []struct {
		path []string
		want string
	}{
		{[]string{"admin:organization:certificates", "create", "--cont"}, ""},
		{[]string{"images", "edit", "--input-f"}, "--input-fidelity\n"},
	} {
		got := runMainDispatch(t, "bash", append([]string{"openai", "__complete", "--"}, tc.path...)...)
		require.Equal(t, mainDispatchResult{0, tc.want, ""}, got)
	}
}

func TestMainImageFidelityCompatibility(t *testing.T) {
	for _, value := range []string{"", "high", "low"} {
		t.Run("fidelity="+value, func(t *testing.T) {
			requests := make(chan imageUploadRequest, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- readImageUpload(t, r)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"created":1,"data":[]}`)
			}))
			defer server.Close()
			source, content := imageUploadSource(t)
			args := []string{"openai", "--base-url", server.URL, "--api-key", "fake-image-key", "--format", "json", "images", "edit", "--image", source, "--prompt", "synthetic prompt", "--model", "gpt-image-1"}
			if value != "" {
				args = append(args, "--input-fidelity", value)
			}
			got := runMainDispatch(t, "bash", args...)
			require.Equal(t, 0, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.JSONEq(t, `{"created":1,"data":[]}`, got.stdout)
			require.Len(t, requests, 1)
			request := <-requests
			require.Equal(t, "/images/edits", request.path)
			want := map[string][]string{"model": {"gpt-image-1"}, "prompt": {"synthetic prompt"}}
			if value != "" {
				want["input_fidelity"] = []string{value}
			}
			require.Equal(t, want, request.values)
			require.Equal(t, map[string][][]byte{"image[]": {content}}, request.files)
		})
	}
}

func TestMainVoiceCompatibilityRoutes(t *testing.T) {
	for _, route := range [][]string{{"audio:voices", "create"}, {"audio", "voices", "create"}} {
		t.Run(strings.Join(route, " "), func(t *testing.T) {
			requests := make(chan imageUploadRequest, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fake-voice-key" {
					t.Error("voice creation must retain POST and ordinary API-key selection")
				}
				requests <- readImageUpload(t, r)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic","type":"audio_sample"}`)
			}))
			defer server.Close()
			sample := filepath.Join(t.TempDir(), "sample.wav")
			require.NoError(t, os.WriteFile(sample, []byte("synthetic audio"), 0600))
			args := []string{"openai", "--base-url", server.URL, "--api-key", "fake-voice-key", "--admin-api-key", "fake-admin-key", "--format", "json"}
			args = append(args, route...)
			args = append(args, "--name", "Synthetic", "--consent", "cons_synthetic", "--audio-sample", sample, "--type", "audio_sample")
			got := runMainDispatch(t, "bash", args...)
			require.Equal(t, 0, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.JSONEq(t, `{"id":"voice_synthetic","name":"Synthetic","type":"audio_sample"}`, got.stdout)
			require.Len(t, requests, 1)
			request := <-requests
			require.Equal(t, "/audio/voices", request.path)
			require.Equal(t, map[string][]string{"name": {"Synthetic"}, "consent": {"cons_synthetic"}, "type": {"audio_sample"}}, request.values)
			require.Equal(t, map[string][][]byte{"audio_sample": {[]byte("synthetic audio")}}, request.files)
		})
	}
}
