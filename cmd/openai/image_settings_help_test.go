package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainImageSettingsHelpScope(t *testing.T) {
	for _, operation := range []string{"generate", "edit", "create-variation"} {
		t.Run(operation, func(t *testing.T) {
			env := []string{"OPENAI_BASE_URL=not a URL", "OPENAI_API_KEY="}
			short := runMainDispatchWithEnv(t, "bash", env, "openai", "images", operation, "--help")
			require.Zero(t, short.code, short.stderr)
			require.Empty(t, short.stderr)
			require.LessOrEqual(t, len(strings.Split(strings.TrimSpace(short.stdout), "\n")), 8)
			require.Contains(t, short.stdout, "~/Downloads/gpt-images/")
			require.NotContains(t, short.stdout, "--name")
			require.Contains(t, short.stdout, "Full help: openai help --all images "+operation)
			full := runMainDispatchWithEnv(t, "bash", env, "openai", "help", "--all", "images", operation)
			require.Zero(t, full.code, full.stderr)
			require.Empty(t, full.stderr)
			_, description, found := strings.Cut(full.stdout, "\nDESCRIPTION:\n")
			require.True(t, found, "full help must include the command description")
			description, _, found = strings.Cut(description, "\nOPTIONS:")
			require.True(t, found, "full help must retain the separate option reference")
			require.LessOrEqual(t, len(strings.Fields(description)), 330, "keep the introduction concise; details belong in the option reference")
			text := strings.Join(strings.Fields(full.stdout), " ")
			for _, want := range []string{"--name", "folder", "must already exist", "-2, -3", "--format json returns API data", "no saving or CLI image defaults", "--inline off hides previews once", "openai images inline off remembers it", "--inline overrides the preference", "Pipes and CI never show previews"} {
				require.Contains(t, text, want)
			}
			if operation == "create-variation" {
				for _, unsupported := range []string{"--quality", "--partial-images", "--moderation", "--output-format", "--output-compression", "--background", "--stream"} {
					require.NotContains(t, full.stdout, unsupported)
				}
				require.Contains(t, text, "Variations support dall-e-2 only")
			} else {
				for _, want := range []string{"With both --model and --response-format omitted", "including nulls", "Setting model or response-format leaves other omitted settings to the API", "requests up to that many previews and enables streaming when saving", "Only one final image is saved", "--format json --stream true", "--max-items or use -1"} {
					require.Contains(t, text, want)
				}
				if operation == "edit" {
					require.NotContains(t, full.stdout, "\n   --moderation ")
				}
			}
		})
	}
}

// These are commands printed in full help, run through the real entrypoint.
// The server checks their requests and returns synthetic images; it does not
// establish live model access or validate an image model's visual output.
func TestMainImageSettingsCompleteExamples(t *testing.T) {
	for _, tc := range []struct {
		operation string
		args      []string
	}{
		{"generate", []string{"--prompt", "A tiny cat", "--output-dir", "~/Downloads", "--name", "cat"}},
		{"generate", []string{"--prompt", "A tiny cat", "--count", "2"}},
		{"generate", []string{"--prompt", "A leaf", "--background", "transparent", "--output-format", "webp"}},
		{"edit", []string{"--image", "photo.png", "--prompt", "Make the sky purple", "--name", "purple-sky"}},
		{"edit", []string{"--image", "photo.png", "--prompt", "Remove the background", "--background", "transparent", "--output-format", "webp"}},
		{"create-variation", []string{"--image", "photo.png", "--name", "variation"}},
		{"create-variation", []string{"--image", "photo.png", "--count", "2", "--size", "512x512"}},
	} {
		t.Run(tc.operation+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
			args := append([]string(nil), tc.args...)
			var example strings.Builder
			example.WriteString("openai ")
			fmt.Fprintf(&example, "images %s", tc.operation)
			settings := map[string]any{}
			for i := 0; i < len(args); i += 2 {
				flag, value := args[i], args[i+1]
				fmt.Fprintf(&example, " %s ", flag)
				if strings.Contains(value, " ") || value == "photo.png" || strings.HasPrefix(value, "~/") {
					example.WriteString(strconv.Quote(value))
				} else {
					example.WriteString(value)
				}
				settings[strings.TrimPrefix(flag, "--")] = value
			}
			help := runMainDispatch(t, "bash", "openai", "help", "--all", "images", tc.operation)
			require.Zero(t, help.code, help.stderr)
			require.Contains(t, help.stdout, example.String(), "the exact example must remain copyable")
			home := t.TempDir()
			directory := filepath.Join(home, "Downloads")
			require.NoError(t, os.Mkdir(directory, 0700))
			var source bytes.Buffer
			require.NoError(t, png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 256, 256))))
			sourcePath := filepath.Join(home, "photo.png")
			require.NoError(t, os.WriteFile(sourcePath, source.Bytes(), 0600))
			payload, extension := source.Bytes(), ".png"
			if settings["output-format"] == "webp" {
				// Same locally encoded synthetic 2x2 WebP as the preview tests.
				var err error
				payload, err = base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvAUAAAAcQ/Y/+ByKi/wEA")
				require.NoError(t, err)
				extension = ".webp"
			}
			count := 1
			if settings["count"] == "2" {
				count = 2
			}
			want := map[string]any{}
			if tc.operation == "create-variation" {
				want["model"], want["response_format"] = "dall-e-2", "b64_json"
			} else {
				want = map[string]any{"model": "gpt-image-2.5-sunburst", "n": float64(1), "size": "auto", "quality": "auto", "background": "auto", "output_format": "png", "partial_images": float64(0), "stream": false}
				if tc.operation == "generate" {
					want["moderation"] = "auto"
				}
			}
			for name, value := range settings {
				switch name {
				case "image", "name", "output-dir":
					continue
				case "count":
					name, value = "n", float64(count)
				}
				want[strings.ReplaceAll(name, "-", "_")] = value
			}
			requests := make(chan bool, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.operation == "generate" {
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, want, body)
				} else {
					body := readImageUpload(t, r)
					expected := map[string][]string{}
					for name, value := range want {
						expected[name] = []string{fmt.Sprint(value)}
					}
					require.Equal(t, expected, body.values)
					field := "image[]"
					if tc.operation == "create-variation" {
						field = "image"
					}
					require.Equal(t, [][]byte{source.Bytes()}, body.files[field])
				}
				requests <- true
				encoded := base64.StdEncoding.EncodeToString(payload)
				data := make([]map[string]string, count)
				for i := range data {
					data[i] = map[string]string{"b64_json": encoded}
				}
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": data}))
			}))
			defer server.Close()
			args = append([]string{"images", tc.operation}, tc.args...)
			for i, arg := range args {
				if arg == "photo.png" {
					args[i] = sourcePath
				}
			}
			result := runImageGeneration(t, server, home, "", args...)
			require.Zero(t, result.code, result.stderr)
			require.Empty(t, result.stderr)
			select {
			case <-requests:
			default:
				t.Fatal("example did not reach the API fixture")
			}
			original, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			require.Equal(t, source.Bytes(), original)
			if settings["output-dir"] == nil {
				directory = filepath.Join(directory, "gpt-images")
			}
			files := imageGenerationFiles(t, directory)
			require.Len(t, files, count)
			for _, file := range files {
				saved, err := os.ReadFile(file)
				require.NoError(t, err)
				require.Equal(t, payload, saved)
				require.Equal(t, extension, filepath.Ext(file))
				require.Contains(t, result.stdout, strconv.Quote(file))
				if stem, ok := settings["name"].(string); ok {
					require.Equal(t, stem+extension, filepath.Base(file))
				}
			}
		})
	}
}
