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
			require.Contains(t, short.stdout, "OPTIONS:")
			require.NotContains(t, short.stdout, "Full help:")
			if operation != "create-variation" {
				require.Contains(t, short.stdout, "~/Downloads/gpt-images/")
			}
			if operation != "create-variation" {
				require.Contains(t, short.stdout, "--name TEXT")
				require.Contains(t, short.stdout, "--output-dir DIRECTORY")
				if operation == "generate" {
					require.Contains(t, short.stdout, "choose settings in an interactive terminal")
				}
			}

			full := runMainDispatchWithEnv(t, "bash", env, "openai", "help", "images", operation)
			require.Zero(t, full.code, full.stderr)
			require.Empty(t, full.stderr)
			require.Equal(t, short.stdout, full.stdout)
			_, description, found := strings.Cut(full.stdout, "\nDESCRIPTION:\n")
			require.True(t, found, "full help must include the command description")
			description, _, found = strings.Cut(description, "\nEXAMPLES:")
			require.True(t, found, "full help must retain the separate option reference")
			require.LessOrEqual(t, len(strings.Fields(description)), 65, "keep the introduction concise; details belong in the option reference")
			require.Equal(t, 1, strings.Count(full.stdout, "--prompt \""), "lead with one useful example")
			text := strings.Join(strings.Fields(full.stdout), " ")
			for _, want := range []string{"--name", "-2, -3", "Uses your saved preference unless set", "Pipes and CI never show previews"} {
				require.Contains(t, text, want)
			}
			if operation == "create-variation" {
				for _, unsupported := range []string{"--quality", "--partial-images", "--moderation", "--output-format", "--output-compression", "--background", "--stream"} {
					require.NotContains(t, full.stdout, unsupported)
				}
				require.Contains(t, text, "retired and no longer available")
				require.Contains(t, text, "The options below describe the legacy variations contract")
			} else {
				for _, want := range []string{"existing folder", "--format json", "without saving", "Saving default when model and response-format are omitted", "Explicit models retain API defaults", "Positive values enable streaming when saving", "When saving, only the final image is kept", "Use --format json for API events", "--max-items or use -1"} {
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
		helpTopic, operation string
		args                 []string
	}{
		{"generate", "generate", []string{"--prompt", "A tiny cat", "--name", "cat"}},
		{"edit", "edit", []string{"--image", "photo.png", "--prompt", "Make the sky purple", "--name", "purple-sky"}},
		{"create-variation", "edit", []string{"--image", "photo.png", "--prompt", "Create a variation of this image", "--name", "variation"}},
	} {
		t.Run(tc.helpTopic+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
			args := append([]string(nil), tc.args...)
			var example strings.Builder
			example.WriteString("openai ")
			fmt.Fprintf(&example, "images %s", tc.operation)
			settings := map[string]any{}
			for i := 0; i < len(args); i += 2 {
				flag, value := args[i], args[i+1]
				fmt.Fprintf(&example, " %s ", flag)
				if strings.Contains(value, " ") || value == "photo.png" {
					example.WriteString(strconv.Quote(value))
				} else {
					example.WriteString(value)
				}
				settings[strings.TrimPrefix(flag, "--")] = value
			}
			help := runMainDispatch(t, "bash", "openai", "help", "images", tc.helpTopic)
			require.Zero(t, help.code, help.stderr)
			require.Contains(t, help.stdout, example.String(), "the exact example must remain copyable")
			home := t.TempDir()
			directory := filepath.Join(home, "Downloads")
			require.NoError(t, os.Mkdir(directory, 0700))
			var source bytes.Buffer
			require.NoError(t, png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 256, 256))))
			sourcePath := filepath.Join(home, "photo.png")
			require.NoError(t, os.WriteFile(sourcePath, source.Bytes(), 0600))
			want := map[string]any{"model": "gpt-image-2.5-sunburst", "n": float64(1), "size": "auto", "quality": "auto", "background": "auto", "output_format": "png", "partial_images": float64(0), "stream": false}
			if tc.operation == "generate" {
				want["moderation"] = "auto"
			}
			for name, value := range settings {
				switch name {
				case "image", "name":
					continue
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
					require.Equal(t, [][]byte{source.Bytes()}, body.files["image[]"])
				}
				requests <- true
				encoded := base64.StdEncoding.EncodeToString(source.Bytes())
				data := []map[string]string{{"b64_json": encoded}}
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
			directory = filepath.Join(directory, "gpt-images")
			files := imageGenerationFiles(t, directory)
			require.Len(t, files, 1)
			for _, file := range files {
				saved, err := os.ReadFile(file)
				require.NoError(t, err)
				require.Equal(t, source.Bytes(), saved)
				require.Equal(t, ".png", filepath.Ext(file))
				require.Contains(t, result.stdout, strconv.Quote(file))
				if stem, ok := settings["name"].(string); ok {
					require.Equal(t, stem+".png", filepath.Base(file))
				}
			}
		})
	}
}
