package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainImageUploadQuotedCountPreservesMultipart(t *testing.T) {
	for _, operation := range []string{"edit", "create-variation"} {
		for _, input := range []struct{ name, body, value string }{
			{"JSON", `{"n":"2"}`, "2"},
			{"YAML", "n: '2'\n", "2"},
			{"whitespace", `{"n":" 2 \n"}`, " 2 \n"},
		} {
			for _, apiOutput := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/API=%v", operation, input.name, apiOutput), func(t *testing.T) {
					source, payload := imageUploadSource(t)
					requests := make(chan imageUploadRequest, 1)
					encoded := base64.StdEncoding.EncodeToString(payload)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests <- readImageUpload(t, r)
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"data":[{"b64_json":%q},{"b64_json":%q}]}`, encoded, encoded)
					}))
					defer server.Close()
					home := t.TempDir()
					args := imageUploadArgs(operation, source)
					if apiOutput {
						args = append([]string{"--format", "json"}, args...)
					}
					got := runImageGeneration(t, server, home, input.body, args...)
					require.Zero(t, got.code, got.stderr)
					require.Empty(t, got.stderr)
					captured := <-requests
					require.Equal(t, []string{input.value}, captured.values["n"])
					fileField := "image"
					if operation == "edit" {
						fileField = "image[]"
					}
					require.Equal(t, [][]byte{payload}, captured.files[fileField])
					outputDir := filepath.Join(home, "Downloads", "gpt-images")
					if apiOutput {
						require.Contains(t, got.stdout, encoded)
						require.NotContains(t, got.stdout, "Saved image:")
						require.Empty(t, imageGenerationFiles(t, outputDir))
						require.NotContains(t, captured.values, "model")
						require.NotContains(t, captured.values, "response_format")
					} else {
						assertImageGenerationFiles(t, outputDir, got.stdout, 2, payload)
					}
					original, err := os.ReadFile(source)
					require.NoError(t, err)
					require.Equal(t, payload, original)
				})
			}
		}
	}
}

func TestMainImageEditQuotedStreamScalars(t *testing.T) {
	for _, input := range []struct {
		name, body, stream, partials string
		streaming                    bool
	}{
		{"JSON", `{"stream":"true","partial_images":"2"}`, "true", "2", true},
		{"YAML", "stream: 'true'\npartial_images: '2'\n", "true", "2", true},
		{"false with whitespace", `{"stream":" false \n","partial_images":" 0 "}`, " false \n", " 0 ", false},
	} {
		for _, apiOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/API=%v", input.name, apiOutput), func(t *testing.T) {
				source, payload := imageUploadSource(t)
				requests := make(chan imageUploadRequest, 1)
				encoded := base64.StdEncoding.EncodeToString(payload)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests <- readImageUpload(t, r)
					if input.streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: {\"type\":\"image_edit.partial_image\",\"b64_json\":\"synthetic-partial\"}\n\n")
						fmt.Fprintf(w, "data: {\"type\":\"image_edit.completed\",\"b64_json\":%q}\n\n", encoded)
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, imageGenerationResponse(payload))
					}
				}))
				defer server.Close()
				home := t.TempDir()
				args := imageUploadArgs("edit", source)
				if apiOutput {
					args = append([]string{"--format", "json"}, args...)
				}
				got := runImageGeneration(t, server, home, input.body, args...)
				require.Zero(t, got.code, got.stderr)
				require.Empty(t, got.stderr)
				captured := <-requests
				require.Equal(t, []string{input.stream}, captured.values["stream"])
				require.Equal(t, []string{input.partials}, captured.values["partial_images"])
				require.Equal(t, [][]byte{payload}, captured.files["image[]"])
				outputDir := filepath.Join(home, "Downloads", "gpt-images")
				if apiOutput {
					require.Contains(t, got.stdout, encoded)
					require.NotContains(t, got.stdout, "Saved image:")
					require.Empty(t, imageGenerationFiles(t, outputDir))
					require.NotContains(t, captured.values, "model")
					if input.streaming {
						require.Contains(t, got.stdout, "synthetic-partial")
					}
				} else {
					assertImageGenerationFiles(t, outputDir, got.stdout, 1, payload)
					require.NotContains(t, got.stdout, "synthetic-partial")
				}
				original, err := os.ReadFile(source)
				require.NoError(t, err)
				require.Equal(t, payload, original)
			})
		}
	}
}

func TestMainImageUploadMalformedScalarTextFailsBeforeRequest(t *testing.T) {
	source, payload := imageUploadSource(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	for _, input := range []struct{ name, operation, body string }{
		{"invalid count", "create-variation", `{"n":"synthetic-invalid"}`},
		{"fractional count", "edit", `{"n":"1.5"}`},
		{"zero count", "create-variation", `{"n":"0"}`},
		{"array count", "edit", `{"n":"[2]"}`},
		{"invalid stream", "edit", `{"stream":"synthetic-invalid"}`},
		{"too many partials", "edit", `{"partial_images":"4"}`},
		{"boolean partials", "edit", `{"partial_images":"true"}`},
	} {
		for _, apiOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/API=%v", input.name, apiOutput), func(t *testing.T) {
				home := t.TempDir()
				args := imageUploadArgs(input.operation, source)
				if apiOutput {
					args = append([]string{"--format", "json"}, args...)
				}
				got := runImageGeneration(t, server, home, input.body, args...)
				require.NotZero(t, got.code)
				require.Empty(t, got.stdout)
				require.NotEmpty(t, got.stderr)
				require.NotContains(t, got.stderr, "synthetic-invalid")
				require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
			})
		}
	}
	require.Zero(t, requests.Load())
	original, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, payload, original)
}

func TestMainImageUploadNullAndScalarLookingStringsKeepMultipartValues(t *testing.T) {
	for _, input := range []struct{ name, body, value string }{
		{"native null", `{"n":null,"partial_images":null,"stream":null,"prompt":"123","model":"false"}`, ""},
		{"text null", `{"n":"null","partial_images":"null","stream":"null","prompt":"123","model":"false"}`, "null"},
	} {
		t.Run(input.name, func(t *testing.T) {
			source, payload := imageUploadSource(t)
			requests := make(chan imageUploadRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- readImageUpload(t, r)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, imageGenerationResponse(payload))
			}))
			defer server.Close()
			home := t.TempDir()
			got := runImageGeneration(t, server, home, input.body, "--format", "json", "images", "edit", "--image", source)
			require.Zero(t, got.code, got.stderr)
			require.Empty(t, got.stderr)
			captured := <-requests
			require.Equal(t, map[string][]string{"n": {input.value}, "partial_images": {input.value}, "stream": {input.value}, "prompt": {"123"}, "model": {"false"}}, captured.values)
			require.Equal(t, [][]byte{payload}, captured.files["image[]"])
			require.Contains(t, got.stdout, base64.StdEncoding.EncodeToString(payload))
			require.Empty(t, imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")))
			original, err := os.ReadFile(source)
			require.NoError(t, err)
			require.Equal(t, payload, original)
		})
	}
}
