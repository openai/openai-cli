package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type imageUploadRequest struct {
	path   string
	values map[string][]string
	files  map[string][][]byte
}

func readImageUpload(t *testing.T, r *http.Request) imageUploadRequest {
	t.Helper()
	got := imageUploadRequest{path: r.URL.Path, values: map[string][]string{}, files: map[string][][]byte{}}
	reader, err := r.MultipartReader()
	require.NoError(t, err)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		if part.FileName() == "" {
			got.values[part.FormName()] = append(got.values[part.FormName()], string(data))
		} else {
			got.files[part.FormName()] = append(got.files[part.FormName()], data)
		}
	}
	return got
}

func imageUploadSource(t *testing.T) (string, []byte) {
	t.Helper()
	data := imageGenerationPNG(t)
	source := filepath.Join(t.TempDir(), "source.png")
	require.NoError(t, os.WriteFile(source, data, 0600))
	return source, data
}

func imageUploadArgs(operation, source string) []string {
	args := []string{"images", operation, "--image", source}
	if operation == "edit" {
		args = append(args, "--prompt", "Make the sky purple")
	}
	return args
}

func TestMainImageUploadSavingDefaultsAndSources(t *testing.T) {
	for _, operation := range []string{"edit", "create-variation"} {
		t.Run(operation, func(t *testing.T) {
			source, payload := imageUploadSource(t)
			requests := make(chan imageUploadRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- readImageUpload(t, r)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, imageGenerationResponse(payload))
			}))
			defer server.Close()
			home := t.TempDir()
			args := imageUploadArgs(operation, source)
			if operation == "edit" {
				args = append(args, "--image", source, "--mask", source)
			}
			result := runImageGeneration(t, server, home, "", args...)
			require.Zero(t, result.code, result.stderr)
			require.Empty(t, result.stderr)
			captured := <-requests
			if operation == "edit" {
				require.Equal(t, "/images/edits", captured.path)
				require.Equal(t, [][]byte{payload, payload}, captured.files["image[]"])
				require.Equal(t, [][]byte{payload}, captured.files["mask"])
				require.Equal(t, map[string][]string{"prompt": {"Make the sky purple"}, "model": {"gpt-image-2.5-sunburst"}, "n": {"1"}, "size": {"auto"}, "quality": {"auto"}, "background": {"auto"}, "output_format": {"png"}, "partial_images": {"0"}, "stream": {"false"}}, captured.values)
			} else {
				require.Equal(t, "/images/variations", captured.path)
				require.Equal(t, [][]byte{payload}, captured.files["image"])
				require.Equal(t, map[string][]string{"model": {"dall-e-2"}, "response_format": {"b64_json"}}, captured.values)
			}
			paths := assertImageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"), result.stdout, 1, payload)
			stem := "make-the-sky-purple.png"
			if operation == "create-variation" {
				stem = "image-variation.png"
			}
			require.Equal(t, stem, filepath.Base(paths[0]))
			original, err := os.ReadFile(source)
			require.NoError(t, err)
			require.Equal(t, payload, original)
		})
	}
}

func TestMainImageUploadOverridesAndInputForms(t *testing.T) {
	source, payload := imageUploadSource(t)
	scalar := func(name, contents string) string {
		path := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
		return "@" + path
	}
	requests := make(chan imageUploadRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- readImageUpload(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, imageGenerationResponse(payload))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, operation, stdin string
		flags                  []string
		want                   map[string][]string
		wantFiles              map[string][][]byte
	}{
		{name: "explicit edit model", operation: "edit", flags: []string{"--model", "future-image-model"}, want: map[string][]string{"prompt": {"Make the sky purple"}, "model": {"future-image-model"}}},
		{name: "dall-e edit", operation: "edit", flags: []string{"--model", "dall-e-2"}, want: map[string][]string{"prompt": {"Make the sky purple"}, "model": {"dall-e-2"}, "response_format": {"b64_json"}}},
		{name: "explicit nulls", operation: "edit", stdin: `{"model":null,"n":null,"size":null,"quality":null,"background":null,"output_format":null,"stream":null,"partial_images":null,"response_format":null}`, want: map[string][]string{"prompt": {"Make the sky purple"}, "model": {""}, "n": {""}, "size": {""}, "quality": {""}, "background": {""}, "output_format": {""}, "stream": {""}, "partial_images": {""}, "response_format": {""}}},
		{name: "variation nulls", operation: "create-variation", stdin: `{"model":null,"response_format":null}`, want: map[string][]string{"model": {""}, "response_format": {""}}},
		{name: "YAML override", operation: "edit", stdin: "model: future-image-model\nn: 2\nquality: high\n", flags: []string{"--count", "1"}, want: map[string][]string{"prompt": {"Make the sky purple"}, "model": {"future-image-model"}, "n": {"1"}, "quality": {"high"}}},
		{name: "scalar file replay", operation: "edit", flags: []string{"--model", scalar("model.txt", "dall-e-2"), "--prompt", scalar("prompt.txt", "synthetic purple sky\r\n")}, want: map[string][]string{"response_format": {"b64_json"}}, wantFiles: map[string][][]byte{"model": {[]byte("dall-e-2")}, "prompt": {[]byte("synthetic purple sky\r\n")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			got := runImageGeneration(t, server, home, tc.stdin, append(imageUploadArgs(tc.operation, source), tc.flags...)...)
			require.Zero(t, got.code, got.stderr)
			captured := <-requests
			require.Equal(t, tc.want, captured.values)
			for key, want := range tc.wantFiles {
				require.Equal(t, want, captured.files[key])
			}
			assertImageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"), got.stdout, 1, payload)
		})
	}
}

func TestMainImageUploadExplicitFormatsAndURLBypassSaving(t *testing.T) {
	source, payload := imageUploadSource(t)
	requests := make(chan imageUploadRequest, 1)
	var downloads atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloads.Add(1); http.Error(w, "must not fetch", 500) }))
	defer remote.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := readImageUpload(t, r)
		requests <- captured
		w.Header().Set("Content-Type", "application/json")
		if len(captured.values["response_format"]) > 0 && captured.values["response_format"][0] == "url" {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"url": remote.URL + "/image?synthetic=secret"}}})
			return
		}
		io.WriteString(w, imageGenerationResponse(payload))
	}))
	defer server.Close()
	for _, operation := range []string{"edit", "create-variation"} {
		for _, flags := range [][]string{{"--format", "json"}, {"--format", "yaml"}, {"--format", "jsonl"}, {"--transform", "data.0.b64_json", "--raw-output"}, {"--raw-output"}} {
			t.Run(operation+strings.Join(flags, " "), func(t *testing.T) {
				home := t.TempDir()
				args := append(append([]string{}, flags...), imageUploadArgs(operation, source)...)
				got := runImageGeneration(t, server, home, "", args...)
				require.Zero(t, got.code, got.stderr)
				require.Contains(t, got.stdout, base64.StdEncoding.EncodeToString(payload))
				captured := <-requests
				require.NotContains(t, captured.values, "model")
				require.NotContains(t, captured.values, "response_format")
				require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
			})
		}
		home := t.TempDir()
		got := runImageGeneration(t, server, home, "", append(imageUploadArgs(operation, source), "--response-format", "url")...)
		require.Zero(t, got.code, got.stderr)
		<-requests
		require.NotContains(t, got.stdout, "Saved image:")
		require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
	}
	require.Zero(t, downloads.Load())
}

func TestMainImageUploadBatchCollisionAndPartialFailure(t *testing.T) {
	for _, operation := range []string{"edit", "create-variation"} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s partial=%v", operation, partial), func(t *testing.T) {
				source, payload := imageUploadSource(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					captured := readImageUpload(t, r)
					require.Equal(t, []string{"2"}, captured.values["n"])
					encoded := base64.StdEncoding.EncodeToString(payload)
					second := encoded
					if partial {
						second = "synthetic-malformed-data"
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"data":[{"b64_json":%q},{"b64_json":%q}]}`, encoded, second)
				}))
				defer server.Close()
				dir := filepath.Dir(source)
				got := runImageGeneration(t, server, t.TempDir(), "", append(imageUploadArgs(operation, source), "--output-dir", dir, "--name", "source", "--count", "2")...)
				if partial {
					require.NotZero(t, got.code)
					require.Contains(t, got.stderr, "Saved 1 image(s)")
					require.NotContains(t, got.stderr, "synthetic-malformed-data")
				} else {
					require.Zero(t, got.code, got.stderr)
				}
				data, err := os.ReadFile(source)
				require.NoError(t, err)
				require.Equal(t, payload, data)
				count := 3
				if partial {
					count = 2
				}
				require.Len(t, imageGenerationFiles(t, dir), count)
				data, err = os.ReadFile(filepath.Join(dir, "source-2.png"))
				require.NoError(t, err)
				require.Equal(t, payload, data)
				require.Contains(t, got.stdout, "source-2.png")
			})
		}
	}
}

func TestMainImageEditStreamSavesFinalAndCloses(t *testing.T) {
	source, payload := imageUploadSource(t)
	for _, tc := range []struct {
		name, stdin string
		flags       []string
	}{
		{name: "explicit", flags: []string{"--stream", "true", "--max-items", "-1"}},
		{name: "partials", flags: []string{"--partial-images", "2"}},
		{name: "stdin", stdin: `{"stream":true,"partial_images":2}`},
		{name: "YAML", stdin: "stream: true\npartial_images: 2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed := make(chan struct{})
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured := readImageUpload(t, r)
				require.Equal(t, []string{"true"}, captured.values["stream"])
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"type\":\"image_edit.partial_image\",\"b64_json\":\"ignored malformed partial\"}\n\n")
				fmt.Fprintf(w, "data: {\"type\":\"image_edit.completed\",\"b64_json\":%q}\n\n", base64.StdEncoding.EncodeToString(payload))
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					close(closed)
				case <-stop:
				}
			}))
			defer server.Close()
			defer close(stop)
			home := t.TempDir()
			got := runImageGeneration(t, server, home, tc.stdin, append(imageUploadArgs("edit", source), tc.flags...)...)
			require.Zero(t, got.code, got.stderr)
			assertImageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"), got.stdout, 1, payload)
			require.NotContains(t, got.stdout, "ignored malformed partial")
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("edit stream remained open after final image")
			}
		})
	}
}

func TestMainImageEditStreamExplicitDataAndFailures(t *testing.T) {
	source, payload := imageUploadSource(t)
	for _, event := range []string{
		`{"type":"image_edit.partial_image","b64_json":"synthetic-private-data"}`,
		`{"type":"image_edit.failed","error":{"message":"synthetic-private-data"}}`,
		`{"type":"image_edit.completed","b64_json":"synthetic-private-data"}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			readImageUpload(t, r)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\n", event)
		}))
		home := t.TempDir()
		got := runImageGeneration(t, server, home, "", append(imageUploadArgs("edit", source), "--stream", "true")...)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		require.NotContains(t, got.stderr, "synthetic-private-data")
		require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
		server.Close()
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := readImageUpload(t, r)
		require.NotContains(t, captured.values, "model")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"image_edit.completed\",\"b64_json\":%q}\n\n", encoded)
	}))
	defer server.Close()
	home := t.TempDir()
	got := runImageGeneration(t, server, home, `{"stream":true}`, append([]string{"--format", "json"}, imageUploadArgs("edit", source)...)...)
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "image_edit.completed")
	require.Contains(t, got.stdout, encoded)
	require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
}

func TestMainImageUploadInvalidSettingsFailBeforeRequest(t *testing.T) {
	source, _ := imageUploadSource(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", 500)
	}))
	defer server.Close()
	for _, tc := range []struct {
		operation, stdin string
		flags            []string
	}{
		{operation: "edit", flags: []string{"unexpected-argument"}},
		{operation: "edit", flags: []string{"--name", "../unsafe"}},
		{operation: "edit", flags: []string{"--output-dir", filepath.Join(t.TempDir(), "missing")}},
		{operation: "edit", flags: []string{"--stream", "true", "--max-items", "1"}},
		{operation: "edit", flags: []string{"--stream", "true", "--max-items", "0"}},
		{operation: "edit", flags: []string{"--stream", "true", "--count", "2"}},
		{operation: "edit", flags: []string{"--partial-images", "1", "--stream", "false"}},
		{operation: "edit", stdin: `{"stream":"synthetic-private-data"}`},
		{operation: "create-variation", stdin: `{"stream":true}`},
		{operation: "create-variation", stdin: `{"partial_images":1}`},
		{operation: "create-variation", flags: []string{"--name", "a/b"}},
	} {
		t.Run(tc.operation+strings.Join(tc.flags, " ")+tc.stdin, func(t *testing.T) {
			got := runImageGeneration(t, server, t.TempDir(), tc.stdin, append(imageUploadArgs(tc.operation, source), tc.flags...)...)
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			require.NotEmpty(t, got.stderr)
			require.NotContains(t, got.stderr, "synthetic-private-data")
		})
	}
	require.Zero(t, requests.Load())
}

func TestMainImageUploadHelpIsOffline(t *testing.T) {
	for _, operation := range []string{"edit", "create-variation"} {
		for _, args := range [][]string{{"images", operation, "--help"}, {"help", "--all", "images", operation}} {
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=not a URL"}, append([]string{"openai"}, args...)...)
			require.Zero(t, got.code, got.stderr)
			for _, flag := range []string{"--name", "--output-dir", "--count"} {
				require.Contains(t, got.stdout, flag)
			}
			require.Contains(t, strings.ToLower(got.stdout), "save")
		}
	}
}
