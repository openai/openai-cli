package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func imageGenerationPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 230, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{B: 200, A: 127})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func imageGenerationResponse(payload []byte) string {
	return `{"created":17,"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(payload) + `"}]}`
}

func imageGenerationEnv(server *httptest.Server, home string) []string {
	return []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-image-saving-test", "HOME=" + home, "USERPROFILE=" + home, "FORCE_COLOR=0"}
}

func runImageGeneration(t *testing.T, server *httptest.Server, home, stdin string, args ...string) mainDispatchResult {
	t.Helper()
	var input *os.File
	if stdin != "" {
		path := filepath.Join(t.TempDir(), "request.json")
		if err := os.WriteFile(path, []byte(stdin), 0o600); err != nil {
			t.Fatal(err)
		}
		var err error
		input, err = os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
	}
	return runMainDispatchWithStdin(t, "bash", imageGenerationEnv(server, home), input, append([]string{"openai"}, args...)...)
}

func imageGenerationFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected nested output directory: %q", entry.Name())
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func assertImageGenerationFiles(t *testing.T, dir, stdout string, count int, payload []byte) []string {
	t.Helper()
	paths := imageGenerationFiles(t, dir)
	if len(paths) != count {
		t.Fatalf("saved %d files, want %d; output=%q", len(paths), count, stdout)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("saved image changed: path=%q size=%d error=%v", path, len(data), err)
		}
		if filepath.Ext(path) != ".png" || !strings.Contains(stdout, strconv.Quote(path)) {
			t.Fatalf("missing actual format or saved path: path=%q output=%q", path, stdout)
		}
	}
	return paths
}

func TestMainImageGenerationDefaultsAndRequestOverrides(t *testing.T) {
	payload := imageGenerationPNG(t)
	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(promptFile, []byte("synthetic red fox"), 0o600); err != nil {
		t.Fatal(err)
	}
	defaults := map[string]any{"prompt": "synthetic red fox", "model": "gpt-image-2.5-sunburst", "n": float64(1), "size": "auto", "quality": "auto", "background": "auto", "moderation": "auto", "output_format": "png", "partial_images": float64(0), "stream": false}
	for _, tc := range []struct {
		name, stdin string
		args        []string
		want        map[string]any
	}{
		{"defaults", "", []string{"--prompt", "synthetic red fox"}, defaults},
		{"explicit model retains API defaults", "", []string{"--prompt", "synthetic red fox", "--model", "gpt-image-2.5-flare-2026-09-08"}, map[string]any{"prompt": "synthetic red fox", "model": "gpt-image-2.5-flare-2026-09-08"}},
		{"dall-e base64", "", []string{"--prompt", "synthetic red fox", "--model", "dall-e-3"}, map[string]any{"prompt": "synthetic red fox", "model": "dall-e-3", "response_format": "b64_json"}},
		{"explicit b64 format retains API model", "", []string{"--prompt", "synthetic red fox", "--response-format", "b64_json"}, map[string]any{"prompt": "synthetic red fox", "response_format": "b64_json"}},
		{"count and settings", "", []string{"--prompt", "synthetic red fox", "--count", "2", "--quality", "low", "--size", "1536x1024", "--background", "transparent", "--moderation", "low", "--output-format", "webp", "--output-compression", "37"}, map[string]any{"prompt": "synthetic red fox", "model": "gpt-image-2.5-sunburst", "n": float64(2), "size": "1536x1024", "quality": "low", "background": "transparent", "moderation": "low", "output_format": "webp", "output_compression": float64(37), "partial_images": float64(0), "stream": false}},
		{"piped JSON with nulls and flag override", `{"prompt":"stdin prompt","model":null,"quality":null,"n":null,"size":null,"output_format":null,"background":null,"moderation":null,"partial_images":null,"stream":null,"metadata":{"custom":true}}`, []string{"--prompt", "synthetic red fox"}, map[string]any{"prompt": "synthetic red fox", "model": nil, "quality": nil, "n": nil, "size": nil, "output_format": nil, "background": nil, "moderation": nil, "partial_images": nil, "stream": nil, "metadata": map[string]any{"custom": true}}},
		{"piped YAML model", "prompt: synthetic red fox\nmodel: dall-e-2\nn: 2\nquality: standard\n", nil, map[string]any{"prompt": "synthetic red fox", "model": "dall-e-2", "n": float64(2), "quality": "standard", "response_format": "b64_json"}},
		{"prompt file merged with stdin", `{"prompt":"stdin prompt","model":"gpt-image-2","quality":"high"}`, []string{"--prompt", "@" + promptFile, "--quality", "low"}, map[string]any{"prompt": "synthetic red fox", "model": "gpt-image-2", "quality": "low"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/images/generations" {
					t.Errorf("unexpected image request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode image request: %v", err)
				}
				select {
				case requests <- body:
				default:
					t.Error("unexpected duplicate image generation request")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, imageGenerationResponse(payload))
			}))
			defer server.Close()
			home := t.TempDir()
			got := runImageGeneration(t, server, home, tc.stdin, append([]string{"images", "generate"}, tc.args...)...)
			select {
			case request := <-requests:
				if !reflect.DeepEqual(request, tc.want) {
					t.Fatalf("request=%#v, want %#v", request, tc.want)
				}
			default:
				t.Fatal("missing image generation request")
			}
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("image generation failed: %+v", got)
			}
			assertImageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"), got.stdout, 1, payload)
			if strings.Contains(got.stdout, base64.StdEncoding.EncodeToString(payload)) {
				t.Fatalf("saved image printed its encoded content: %q", got.stdout)
			}
		})
	}
}

func TestMainImageGenerationExplicitOutputBypassesSaving(t *testing.T) {
	payload := imageGenerationPNG(t)
	encoded := base64.StdEncoding.EncodeToString(payload)
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode image request: %v", err)
		}
		select {
		case requests <- body:
		default:
			t.Error("unexpected duplicate image generation request")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, imageGenerationResponse(payload))
	}))
	defer server.Close()
	for _, flags := range [][]string{
		{"--format", "json"}, {"--format", "JsOnL"}, {"--format", "yaml"}, {"--format", "raw"}, {"--format", "pretty"}, {"--format", "explore"},
		{"--transform", "data.0.b64_json"}, {"--raw-output"}, {"--format", "text", "--transform", "data.0.b64_json"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			home := t.TempDir()
			got := runImageGeneration(t, server, home, "", append(flags, "images", "generate", "--prompt", "synthetic prompt")...)
			var request map[string]any
			select {
			case request = <-requests:
			default:
				t.Fatalf("missing image generation request: %+v", got)
			}
			want := encoded
			if len(flags) == 2 && flags[0] == "--format" && flags[1] == "pretty" {
				// The existing pretty table intentionally truncates long cells.
				want = encoded[:24]
				if !strings.Contains(got.stdout, "b64_json") {
					t.Fatalf("pretty output lost the API field: %+v", got)
				}
			}
			if got.code != 0 || !strings.Contains(got.stdout, want) {
				t.Fatalf("explicit output lost API data: %+v", got)
			}
			if !reflect.DeepEqual(request, map[string]any{"prompt": "synthetic prompt"}) {
				t.Fatalf("explicit output acquired saving defaults: %#v", request)
			}
			if files := imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")); len(files) != 0 {
				t.Fatalf("explicit output saved files: %q", files)
			}
		})
	}
}

func TestMainImageGenerationURLResponseNeverFetches(t *testing.T) {
	var downloads atomic.Int32
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		t.Errorf("image URL was fetched; authorization present=%v", r.Header.Get("Authorization") != "")
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer imageServer.Close()
	url := imageServer.URL + "/private-image?signature=synthetic-url-secret"
	response, err := json.Marshal(map[string]any{"created": 17, "data": []any{map[string]any{"url": url}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(response)
	}))
	defer server.Close()
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-url=%v", explicit), func(t *testing.T) {
			home := t.TempDir()
			args := []string{"images", "generate", "--prompt", "synthetic prompt"}
			if explicit {
				args = append(args, "--response-format", "url")
			}
			got := runImageGeneration(t, server, home, "", args...)
			if explicit && got.code != 0 {
				t.Fatalf("explicit URL output failed: %+v", got)
			}
			if !explicit && got.code == 0 {
				t.Fatalf("unexpected URL response reported saving success: %+v", got)
			}
			if !explicit && strings.Contains(got.stdout+got.stderr, "synthetic-url-secret") {
				t.Fatalf("saving failure exposed signed URL: %+v", got)
			}
			if files := imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")); len(files) != 0 || downloads.Load() != 0 {
				t.Fatalf("URL response caused side effects: files=%q requests=%d", files, downloads.Load())
			}
		})
	}
}

func TestMainImageGenerationRejectsInvalidOptionsBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	for _, flags := range [][]string{
		{"--format", "json", "--name", "synthetic"},
		{"--format", "raw", "--output-dir", t.TempDir()},
		{"--transform", "data", "--name", "synthetic"},
		{"--raw-output", "--name", "synthetic"},
		{"--response-format", "url", "--name", "synthetic"},
		{"--format", "json", "--partial-images", "2"},
		{"--stream=true", "--max-items", "0"},
		{"--stream=true", "--max-items", "1"},
		{"--partial-images", "2", "--max-items", "1"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			got := runImageGeneration(t, server, t.TempDir(), "", append([]string{"images", "generate", "--prompt", "synthetic prompt"}, flags...)...)
			if got.code == 0 || got.stdout != "" || requests.Load() != 0 {
				t.Fatalf("conflicting save options reached API: result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainImageGenerationMultipleImagesAndCollision(t *testing.T) {
	payload := imageGenerationPNG(t)
	encoded := base64.StdEncoding.EncodeToString(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"b64_json":%q},{"b64_json":%q}]}`, encoded, encoded)
	}))
	defer server.Close()
	home, dir := t.TempDir(), t.TempDir()
	var output strings.Builder
	for range 2 {
		got := runImageGeneration(t, server, home, "", "images", "generate", "--prompt", "synthetic prompt", "--output-dir", dir, "--name", "fox.png", "--count", "2")
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("multiple image saving failed: %+v", got)
		}
		output.WriteString(got.stdout)
	}
	assertImageGenerationFiles(t, dir, output.String(), 4, payload)
	if files := imageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images")); len(files) != 0 {
		t.Fatalf("custom directory still wrote to default: %q", files)
	}
}

func TestMainImageGenerationFilenamesAndDirectoryValidation(t *testing.T) {
	payload := imageGenerationPNG(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, imageGenerationResponse(payload))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, prompt, filename string
		flags                  []string
	}{
		{"prompt traversal and Unicode", "The ../../雪\\fox", "雪-fox.png", nil},
		{"explicit Unicode and extension", "synthetic prompt", "合成.png", []string{"--name", "合成.jpeg"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			got := runImageGeneration(t, server, t.TempDir(), "", append([]string{"images", "generate", "--prompt", tc.prompt, "--output-dir", dir}, tc.flags...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("valid image filename failed: %+v", got)
			}
			paths := assertImageGenerationFiles(t, dir, got.stdout, 1, payload)
			if filepath.Base(paths[0]) != tc.filename {
				t.Fatalf("filename=%q, want %q", filepath.Base(paths[0]), tc.filename)
			}
		})
	}
	for _, name := range []string{"../outside", "..", `nested\file`, "CON", "escape\x1bname", ""} {
		t.Run("invalid name="+name, func(t *testing.T) {
			before := requests.Load()
			dir := t.TempDir()
			got := runImageGeneration(t, server, t.TempDir(), "", "images", "generate", "--prompt", "synthetic prompt", "--output-dir", dir, "--name", name)
			if got.code == 0 || got.stdout != "" || requests.Load() != before || len(imageGenerationFiles(t, dir)) != 0 {
				t.Fatalf("invalid name reached API or filesystem: %+v", got)
			}
		})
	}
	t.Run("missing custom directory", func(t *testing.T) {
		before := requests.Load()
		dir := filepath.Join(t.TempDir(), "missing")
		got := runImageGeneration(t, server, t.TempDir(), "", "images", "generate", "--prompt", "synthetic prompt", "--output-dir", dir)
		if got.code == 0 || requests.Load() != before {
			t.Fatalf("missing directory reached API: %+v", got)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("custom directory was created: %v", err)
		}
	})
}

func TestMainImageGenerationRejectsNamesWithoutSuffixRoomBeforeRequest(t *testing.T) {
	stem := strings.Repeat("x", 250)
	encoded := base64.StdEncoding.EncodeToString(imageGenerationPNG(t))
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing image=%t", existing), func(t *testing.T) {
			dir := t.TempDir()
			probeName := func(name string) error {
				path := filepath.Join(dir, name)
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					return err
				}
				closeErr := file.Close()
				removeErr := os.Remove(path)
				if closeErr != nil || removeErr != nil {
					t.Fatalf("filename probe cleanup failed: close=%v remove=%v", closeErr, removeErr)
				}
				return nil
			}
			if err := probeName(stem + ".jpeg"); err != nil {
				t.Skipf("filesystem cannot create the ordinary 255-byte filename needed for this regression: %v", err)
			}
			if err := probeName(stem + "-9223372036854775807.jpeg"); err == nil {
				t.Skip("filesystem supports this name with the longest collision suffix; the filename-limit regression does not apply")
			}

			originalPath := filepath.Join(dir, stem+".png")
			original := []byte("synthetic existing image must be preserved")
			if existing {
				if err := os.WriteFile(originalPath, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":[{"b64_json":%q},{"b64_json":%q}]}`, encoded, encoded)
			}))
			defer server.Close()
			got := runImageGeneration(t, server, t.TempDir(), "", "images", "generate", "--prompt", "synthetic prompt", "--output-dir", dir, "--name", stem, "--count", "2")
			if got.code == 0 || got.stdout != "" || got.stderr == "" {
				t.Errorf("name without suffix room was not rejected before saving: %+v", got)
			}
			if count := requests.Load(); count != 0 {
				t.Errorf("name without suffix room reached the API: %d requests", count)
			}
			files := imageGenerationFiles(t, dir)
			if existing {
				if len(files) != 1 || files[0] != originalPath {
					t.Errorf("rejected name created new files beside the existing image: %q", files)
				}
				data, err := os.ReadFile(originalPath)
				if err != nil || !bytes.Equal(data, original) {
					t.Errorf("existing image changed: data=%q error=%v", data, err)
				}
			} else if len(files) != 0 {
				t.Errorf("rejected name created files: %q", files)
			}
		})
	}
}

func TestMainImageGenerationPartialFailureKeepsSavedImage(t *testing.T) {
	payload := imageGenerationPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"b64_json":%q},{"b64_json":"invalid-synthetic-base64"}]}`, base64.StdEncoding.EncodeToString(payload))
	}))
	defer server.Close()
	dir := t.TempDir()
	got := runImageGeneration(t, server, t.TempDir(), "", "images", "generate", "--prompt", "synthetic private prompt", "--output-dir", dir)
	if got.code == 0 || got.stderr == "" {
		t.Fatalf("partial failure reported success: %+v", got)
	}
	assertImageGenerationFiles(t, dir, got.stdout, 1, payload)
	for _, secret := range []string{"synthetic private prompt", "invalid-synthetic-base64", base64.StdEncoding.EncodeToString(payload)} {
		if strings.Contains(got.stderr, secret) {
			t.Fatalf("failure exposed request or image data: %q", got.stderr)
		}
	}
}

func TestMainImageGenerationStreamSavesOnlyFinalAndClosesResponse(t *testing.T) {
	payload := imageGenerationPNG(t)
	for _, flags := range [][]string{{"--stream=true"}, {"--partial-images", "2"}, {"--stream=true", "--max-items", "-1"}, {"--partial-images", "2", "--max-items", "-1"}, {"--stream=true", "--max-items", "-2"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			closed := make(chan struct{})
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "event: image_generation.partial_image\ndata: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":\"invalid-partial-is-ignored\"}\n\n")
				fmt.Fprintf(w, "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":%q}\n\n", base64.StdEncoding.EncodeToString(payload))
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					close(closed)
				case <-stop:
				}
			}))
			defer server.Close()
			defer close(stop)
			dir := t.TempDir()
			args := append([]string{"images", "generate", "--prompt", "synthetic prompt", "--output-dir", dir}, flags...)
			got := runImageGeneration(t, server, t.TempDir(), "", args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("final stream saving failed: %+v", got)
			}
			assertImageGenerationFiles(t, dir, got.stdout, 1, payload)
			if strings.Contains(got.stdout, "partial") || strings.Contains(got.stdout, "b64_json") {
				t.Fatalf("stream preview leaked into output: %q", got.stdout)
			}
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("completed stream did not close the HTTP response")
			}
		})
	}
}

func TestMainImageGenerationLargePayloads(t *testing.T) {
	// The JSON case exceeds 64 MiB after base64 encoding. High memory use is
	// intentional; do not shrink these regression probes to accommodate new caps.
	// Trailing bytes are preserved image bytes, not a reason to cap API payloads.
	large := append(imageGenerationPNG(t), make([]byte, 49<<20)...)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			payload := large
			if stream {
				// Retain the SDK's longstanding 32 MiB SSE line limit, including
				// the base64 expansion and JSON envelope.
				payload = payload[:(24<<20)-1024]
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"")
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"data":[{"b64_json":"`)
				}
				encoder := base64.NewEncoder(base64.StdEncoding, w)
				encoder.Write(payload)
				encoder.Close()
				if stream {
					io.WriteString(w, "\"}\n\n")
				} else {
					io.WriteString(w, `"}]}`)
				}
			}))
			defer server.Close()
			dir := t.TempDir()
			got := runImageGeneration(t, server, t.TempDir(), "", "images", "generate", "--prompt", "synthetic large image", "--output-dir", dir, fmt.Sprintf("--stream=%v", stream))
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("large image failed: %+v", got)
			}
			assertImageGenerationFiles(t, dir, got.stdout, 1, payload)
		})
	}
}

func TestMainImageGenerationIncompleteStreamsDoNotReportSuccess(t *testing.T) {
	for _, event := range []string{
		`{"type":"image_generation.partial_image","partial_image_index":0,"b64_json":"synthetic-private-image-data"}`,
		`{"type":"image_generation.failed","error":{"message":"synthetic-private-api-prose"}}`,
		`{"type":"image_generation.completed","b64_json":"synthetic-private-image-data"}`,
	} {
		t.Run(event, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", event)
			}))
			defer server.Close()
			dir := t.TempDir()
			got := runImageGeneration(t, server, t.TempDir(), "", "images", "generate", "--prompt", "synthetic private prompt", "--output-dir", dir, "--stream=true")
			if got.code == 0 || got.stdout != "" || got.stderr == "" || len(imageGenerationFiles(t, dir)) != 0 {
				t.Fatalf("incomplete stream reported success or left an image: %+v", got)
			}
			for _, secret := range []string{"synthetic-private-image-data", "synthetic-private-api-prose", "synthetic private prompt"} {
				if strings.Contains(got.stderr, secret) {
					t.Fatalf("stream error exposed response data: %q", got.stderr)
				}
			}
		})
	}
}

func TestMainImageGenerationClosedStdoutKeepsSavedImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("closed stdout pipe behavior requires native Windows coverage")
	}
	payload := imageGenerationPNG(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		partialFailure bool
		format         string
	}{{false, "text"}, {true, "text"}, {false, "json"}, {true, "json"}} {
		partialFailure := tc.partialFailure
		t.Run(fmt.Sprintf("partial-failure=%v/format=%s", partialFailure, tc.format), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if partialFailure {
					fmt.Fprintf(w, `{"data":[{"b64_json":%q},{"b64_json":"invalid-private-base64"}]}`, base64.StdEncoding.EncodeToString(payload))
				} else {
					io.WriteString(w, imageGenerationResponse(payload))
				}
			}))
			defer server.Close()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			reader.Close()
			defer writer.Close()
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "images", "generate", "--prompt", "synthetic image", "--output-dir", dir, "--format-error", tc.format)
			child.Env = append(imageGenerationEnv(server, t.TempDir()), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = writer, &stderr
			if err := child.Run(); err == nil || stderr.Len() == 0 {
				t.Fatalf("closed output lost save/reporting failure: %v; stderr=%q", err, stderr.String())
			}
			if ctx.Err() != nil || strings.Contains(stderr.String(), "invalid-private-base64") || strings.Contains(stderr.String(), base64.StdEncoding.EncodeToString(payload)) {
				t.Fatalf("closed output stalled or exposed image data: %q", stderr.String())
			}
			diagnostic := stderr.String()
			if !strings.Contains(diagnostic, "Check the output folder before generating again") || strings.Contains(diagnostic, "listed files") {
				t.Fatalf("missing or misleading saved-file recovery advice: %q", diagnostic)
			}
			if partialFailure && (!strings.Contains(diagnostic, "could not save the entire response") || !strings.Contains(diagnostic, "print all saved paths")) {
				t.Fatalf("one batch/output failure hid the other: %q", diagnostic)
			}
			paths := imageGenerationFiles(t, dir)
			if len(paths) != 1 {
				t.Fatalf("closed output lost saved file: %q", paths)
			}
			data, err := os.ReadFile(paths[0])
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("closed output changed saved image: size=%d error=%v", len(data), err)
			}
		})
	}
}
