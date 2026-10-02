package main

import (
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainHelpCompleteImageExamples(t *testing.T) {
	payload := imageGenerationPNG(t)
	for _, tc := range []struct {
		helpTopic, operation, example string
		args                          []string
	}{
		{"generate", "generate", `openai images generate --prompt "A tiny orange robot"`, []string{"--prompt", "A tiny orange robot"}},
		{"edit", "edit", `openai images edit --image "photo.png" --prompt "Make the sky purple"`, []string{"--image", "photo.png", "--prompt", "Make the sky purple"}},
		{"create-variation", "edit", `openai images edit --image "photo.png" --prompt "Create a variation of this image"`, []string{"--image", "photo.png", "--prompt", "Create a variation of this image"}},
	} {
		t.Run(tc.example, func(t *testing.T) {
			help := runMainDispatch(t, "bash", "openai", "images", tc.helpTopic, "--help")
			if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, tc.example) {
				t.Fatalf("incomplete short help: %+v", help)
			}
			if tc.operation != "generate" && !strings.Contains(help.stdout, "your image's path") {
				t.Fatalf("source path placeholder is unexplained: %s", help.stdout)
			}
			source := filepath.Join(t.TempDir(), "photo.png")
			file, err := os.Create(source)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, image.NewNRGBA(image.Rect(0, 0, 256, 256)))
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("source image: %v, %v", err, closeErr)
			}
			args := append([]string{"images", tc.operation}, tc.args...)
			for i, arg := range args {
				if arg == "photo.png" {
					args[i] = source
				}
			}
			requests := make(chan int, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := 1
				if tc.operation == "generate" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["prompt"] != "A tiny orange robot" {
						t.Errorf("example prompt changed: %v, %v", body, err)
					}
					count = int(body["n"].(float64))
				} else {
					got := readImageUpload(t, r)
					if len(got.files) != 1 {
						t.Errorf("source image missing from example: %v", got.files)
					}
				}
				requests <- count
				data := make([]map[string]string, count)
				for i := range data {
					data[i] = map[string]string{"b64_json": base64.StdEncoding.EncodeToString(payload)}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"created": 17, "data": data})
			}))
			defer server.Close()
			home := t.TempDir()
			result := runImageGeneration(t, server, home, "", args...)
			if result.code != 0 || result.stderr != "" {
				t.Fatalf("documented example failed: %+v", result)
			}
			count := <-requests
			assertImageGenerationFiles(t, filepath.Join(home, "Downloads", "gpt-images"), result.stdout, count, payload)
		})
	}
}

func TestMainHelpImagePagesStayBrief(t *testing.T) {
	for _, operation := range []string{"generate", "edit", "create-variation", "preview"} {
		t.Run(operation, func(t *testing.T) {
			got := runMainDispatch(t, "bash", "openai", "images", operation, "--help")
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("short help failed: %+v", got)
			}
			if lines := len(strings.Split(strings.TrimSpace(got.stdout), "\n")); lines > 8 {
				t.Errorf("short help uses %d lines; want one example and at most 8 lines", lines)
			}
			exampleOperation := operation
			if operation == "create-variation" {
				exampleOperation = "edit"
			}
			if strings.Count(got.stdout, "  openai images "+exampleOperation+" ") != 1 {
				t.Errorf("expected one complete example: %s", got.stdout)
			}
			if !strings.Contains(got.stdout, "Full help: openai help --all images "+operation) {
				t.Errorf("full help is not discoverable: %s", got.stdout)
			}
		})
	}
}

func TestMainVariationHelpRetirementGuidance(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "images", "create-variation", "--help"},
		{"openai", "help", "images", "create-variation"},
		{"openai", "images", "help", "create-variation"},
		{"openai", "help", "--all", "images", "create-variation"},
		{"openai", "help", "images", "create-variation", "--all"},
		{"openai", "images", "help", "--all", "create-variation"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			result := runMainDispatchWithEnv(t, "bash",
				[]string{"OPENAI_BASE_URL=not a URL", "OPENAI_API_KEY="}, args...)
			if result.code != 0 || result.stderr != "" {
				t.Fatalf("variation help must not attempt a request: %+v", result)
			}
			text := strings.Join(strings.Fields(result.stdout), " ")
			for _, want := range []string{
				"retired and no longer available",
				`openai images edit --image "photo.png" --prompt "Create a variation of this image"`,
			} {
				if !strings.Contains(text, want) {
					t.Errorf("variation help missing %q:\n%s", want, result.stdout)
				}
			}
			for _, stale := range []string{
				"images create-variation --image",
				"Supports dall-e-2 only",
				"Saving default when model and response-format are omitted",
				"CLI saving requests b64_json for DALL-E",
			} {
				if strings.Contains(text, stale) {
					t.Errorf("variation help still advertises %q:\n%s", stale, result.stdout)
				}
			}
		})
	}
}
