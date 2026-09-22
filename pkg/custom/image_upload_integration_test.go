package custom

import (
	"bytes"
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
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the generated multipart actions through the public CLI. Uploads use
// the normal streaming encoder; only scalar defaults and presentation change.
func TestImagesUploadSavingIntegration(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 180, A: 255})
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, picture))
	encoded := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	response := fmt.Sprintf(`{"created":123,"data":[{"b64_json":%q}]}`, encoded)
	type captured struct {
		path   string
		values map[string][]string
		files  map[string][][]byte
	}
	for _, operation := range []string{"edit", "create-variation"} {
		t.Run(operation, func(t *testing.T) {
			home := t.TempDir()
			source := filepath.Join(home, "source.png")
			require.NoError(t, os.WriteFile(source, imageBytes.Bytes(), 0600))
			requests := make(chan captured, 20)
			var count atomic.Int32
			var stream atomic.Bool
			var batch atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					http.Error(w, "invalid synthetic upload", 400)
					return
				}
				defer r.MultipartForm.RemoveAll()
				got := captured{path: r.URL.Path, values: r.MultipartForm.Value, files: make(map[string][][]byte)}
				for name, uploads := range r.MultipartForm.File {
					for _, upload := range uploads {
						file, err := upload.Open()
						if err != nil {
							http.Error(w, "synthetic file missing", 400)
							return
						}
						data, err := io.ReadAll(file)
						file.Close()
						if err != nil {
							http.Error(w, "synthetic file unreadable", 400)
							return
						}
						got.files[name] = append(got.files[name], data)
					}
				}
				requests <- got
				if stream.Load() {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"image_edit.partial_image\",\"partial_image_index\":0,\"b64_json\":%q}\n\n", encoded)
					fmt.Fprintf(w, "data: {\"type\":\"image_edit.completed\",\"b64_json\":%q}\n\n", encoded)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if batch.Load() {
					fmt.Fprintf(w, `{"created":123,"data":[{"b64_json":%q},{"b64_json":%q}]}`, encoded, encoded)
					return
				}
				io.WriteString(w, response)
			}))
			defer server.Close()
			run := func(stdin string, root, flags []string) (string, error) {
				args := append([]string{"--base-url", server.URL}, root...)
				args = append(args, "images", operation)
				args = append(args, flags...)
				cmd := exec.CommandContext(t.Context(), binary, args...)
				cmd.Stdin = strings.NewReader(stdin)
				cmd.Dir = home
				for _, entry := range os.Environ() {
					key, _, _ := strings.Cut(entry, "=")
					key = strings.ToUpper(key)
					if strings.HasPrefix(key, "OPENAI_") || key == "HOME" || key == "USERPROFILE" || key == "HTTP_PROXY" || key == "HTTPS_PROXY" || key == "ALL_PROXY" || key == "NO_PROXY" {
						continue
					}
					cmd.Env = append(cmd.Env, entry)
				}
				cmd.Env = append(cmd.Env, "HOME="+home, "USERPROFILE="+home, "OPENAI_API_KEY=synthetic-upload-key", "NO_PROXY=127.0.0.1,localhost", "CI=true", "NO_COLOR=1")
				output, err := cmd.CombinedOutput()
				require.NotContains(t, string(output), "\x1b")
				return string(output), err
			}
			flags := []string{"--image", source}
			if operation == "edit" {
				flags = append(flags, "--prompt", "A purple sky")
			}
			savedPaths := func(output string) []string {
				var paths []string
				for _, line := range strings.Split(output, "\n") {
					if quoted, ok := strings.CutPrefix(line, "Saved image: "); ok {
						path, err := strconv.Unquote(quoted)
						require.NoError(t, err)
						data, err := os.ReadFile(path)
						require.NoError(t, err)
						require.Equal(t, imageBytes.Bytes(), data)
						paths = append(paths, path)
					}
				}
				require.NotEmpty(t, paths, output)
				require.NotContains(t, output, encoded)
				require.NotContains(t, output, "b64_json")
				return paths
			}
			checkInput := func() captured {
				got := <-requests
				suffix := "edits"
				fileField := "image[]"
				if operation == "create-variation" {
					suffix = "variations"
					fileField = "image"
				}
				require.Equal(t, "/images/"+suffix, got.path)
				require.NotEmpty(t, got.files[fileField])
				require.Equal(t, imageBytes.Bytes(), got.files[fileField][0])
				for _, field := range []string{"inline", "name", "output-dir", "output_dir", "open", "no-preview", "count"} {
					require.NotContains(t, got.values, field)
				}
				original, err := os.ReadFile(source)
				require.NoError(t, err)
				require.Equal(t, imageBytes.Bytes(), original)
				return got
			}
			t.Run("short and full help stay discoverable", func(t *testing.T) {
				before := count.Load()
				output, err := run("", nil, []string{"--help"})
				require.NoError(t, err, output)
				require.Contains(t, output, "Saves a new image")
				require.Contains(t, output, "help --all images "+operation)
				require.Less(t, len(strings.Split(output, "\n")), 25)
				full, err := run("", []string{"help", "--all"}, nil)
				require.NoError(t, err, full)
				for _, flag := range []string{"--image", "--model", "--size", "--response-format", "--output-dir", "--count"} {
					require.Contains(t, full, flag)
				}
				if operation == "create-variation" {
					require.NotContains(t, full, "CLI saving preset: "+defaultSavedImageModel)
				}
				require.Equal(t, before, count.Load())
			})
			output, err := run("", nil, flags)
			require.NoError(t, err, output)
			paths := savedPaths(output)
			require.Len(t, paths, 1)
			stem := "purple-sky.png"
			if operation == "create-variation" {
				stem = "image-variation.png"
			}
			require.Equal(t, filepath.Join(home, "Downloads", "gpt-images", stem), paths[0])
			got := checkInput()
			if operation == "edit" {
				require.Equal(t, []string{defaultSavedImageModel}, got.values["model"])
				require.Equal(t, []string{"png"}, got.values["output_format"])
				require.Equal(t, []string{"auto"}, got.values["size"])
				require.Equal(t, []string{"false"}, got.values["stream"])
				require.NotContains(t, got.values, "moderation")
			} else {
				require.Equal(t, []string{"dall-e-2"}, got.values["model"])
				require.Equal(t, []string{"b64_json"}, got.values["response_format"])
				require.NotContains(t, got.values, "quality")
				require.NotContains(t, got.values, "output_format")
				require.NotContains(t, got.values, "stream")
			}

			t.Run("collision and batch preserve source", func(t *testing.T) {
				batch.Store(true)
				defer batch.Store(false)
				args := append(append([]string{}, flags...), "--name", "source", "--output-dir", home, "--count", "2")
				output, err := run("", nil, args)
				require.NoError(t, err, output)
				paths := savedPaths(output)
				require.Len(t, paths, 2)
				require.Equal(t, filepath.Join(home, "source-2.png"), paths[0])
				require.Equal(t, filepath.Join(home, "source-3.png"), paths[1])
				got := checkInput()
				require.Equal(t, []string{"2"}, got.values["n"])
			})
			t.Run("JSON and extraction bypass saving", func(t *testing.T) {
				before, err := os.ReadDir(filepath.Join(home, "Downloads", "gpt-images"))
				require.NoError(t, err)
				for _, root := range [][]string{{"--format", "JSON"}, {"--transform", "data.0.b64_json", "--raw-output"}, {"--format", "raw"}} {
					output, err := run("", root, flags)
					require.NoError(t, err, output)
					require.NotContains(t, output, "Saved image:")
					require.Contains(t, output, encoded)
					got := checkInput()
					require.NotContains(t, got.values, "model")
					require.NotContains(t, got.values, "response_format")
				}
				after, err := os.ReadDir(filepath.Join(home, "Downloads", "gpt-images"))
				require.NoError(t, err)
				require.Len(t, after, len(before))
			})
			t.Run("output conflicts rejected before API", func(t *testing.T) {
				before := count.Load()
				output, err := run("", []string{"--format", "json"}, append(append([]string{}, flags...), "--name", "result"))
				require.Error(t, err)
				require.Contains(t, output, "cannot be combined")
				require.Equal(t, before, count.Load())
			})
			t.Run("piped settings and original upload", func(t *testing.T) {
				body := map[string]any{"image": source, "model": "dall-e-2", "n": 1}
				if operation == "edit" {
					body["image"] = []string{source}
					body["prompt"] = "A blue sky"
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				output, err := run(string(raw), nil, nil)
				require.NoError(t, err, output)
				savedPaths(output)
				got := checkInput()
				require.Equal(t, []string{"dall-e-2"}, got.values["model"])
				require.Equal(t, []string{"b64_json"}, got.values["response_format"])
			})
			t.Run("scalar file references keep bytes and output mode", func(t *testing.T) {
				modelFile := filepath.Join(home, "model.txt")
				formatFile := filepath.Join(home, "format.txt")
				require.NoError(t, os.WriteFile(modelFile, []byte("dall-e-2"), 0600))
				require.NoError(t, os.WriteFile(formatFile, []byte("url"), 0600))
				body := map[string]any{"image": source, "model": "@" + modelFile}
				if operation == "edit" {
					body["image"] = []string{source}
					body["prompt"] = "A purple sky"
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				output, err := run(string(raw), nil, nil)
				require.NoError(t, err, output)
				savedPaths(output)
				got := checkInput()
				require.NotEmpty(t, got.files["model"], "values: %#v; files: %#v", got.values, got.files)
				require.Equal(t, []byte("dall-e-2"), got.files["model"][0])
				require.Equal(t, []string{"b64_json"}, got.values["response_format"])
				before, err := os.ReadDir(filepath.Join(home, "Downloads", "gpt-images"))
				require.NoError(t, err)
				body["response_format"] = "@" + formatFile
				raw, err = json.Marshal(body)
				require.NoError(t, err)
				output, err = run(string(raw), nil, nil)
				require.NoError(t, err, output)
				require.NotContains(t, output, "Saved image:")
				got = checkInput()
				require.Equal(t, []byte("url"), got.files["response_format"][0])
				after, err := os.ReadDir(filepath.Join(home, "Downloads", "gpt-images"))
				require.NoError(t, err)
				require.Len(t, after, len(before))
				if operation == "edit" {
					promptFile := filepath.Join(home, "instructions.txt")
					require.NoError(t, os.WriteFile(promptFile, []byte("A lavender sky\n"), 0600))
					output, err = run("", nil, []string{"--image", source, "--prompt", "@" + promptFile})
					require.NoError(t, err, output)
					paths := savedPaths(output)
					require.Equal(t, "lavender-sky.png", filepath.Base(paths[0]))
					got = checkInput()
					require.Equal(t, []byte("A lavender sky\n"), got.files["prompt"][0])
				}
			})
			if operation == "edit" {
				t.Run("multiple sources and mask", func(t *testing.T) {
					output, err := run("", nil, append(append([]string{}, flags...), "--image", source, "--mask", source))
					require.NoError(t, err, output)
					savedPaths(output)
					got := checkInput()
					require.Len(t, got.files["image[]"], 2)
					require.Equal(t, imageBytes.Bytes(), got.files["mask"][0])
				})
				t.Run("streamed editing saves final only", func(t *testing.T) {
					stream.Store(true)
					defer stream.Store(false)
					output, err := run("", nil, append(append([]string{}, flags...), "--partial-images", "2"))
					require.NoError(t, err, output)
					require.Len(t, savedPaths(output), 1)
					got := checkInput()
					require.Equal(t, []string{"true"}, got.values["stream"])
					require.Equal(t, []string{"2"}, got.values["partial_images"])
					output, err = run("", []string{"--format", "json"}, append(append([]string{}, flags...), "--stream", "true"))
					require.NoError(t, err, output)
					require.NotContains(t, output, "Saved image:")
					require.Contains(t, output, "image_edit.completed")
					require.Contains(t, output, encoded)
					checkInput()
				})
			}
		})
	}
}
