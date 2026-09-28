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
		operation, example string
		args               []string
	}{
		{"generate", `openai images generate --prompt "A tiny orange robot"`, []string{"--prompt", "A tiny orange robot"}},
		{"edit", `openai images edit --image "photo.png" --prompt "Make the sky purple"`, []string{"--image", "photo.png", "--prompt", "Make the sky purple"}},
		{"create-variation", `openai images create-variation --image "photo.png"`, []string{"--image", "photo.png"}},
	} {
		t.Run(tc.example, func(t *testing.T) {
			help := runMainDispatch(t, "bash", "openai", "images", tc.operation, "--help")
			if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, tc.example) {
				t.Fatalf("incomplete short help: %+v", help)
			}
			if tc.operation != "generate" && !strings.Contains(help.stdout, "your image's path") {
				t.Fatalf("source path placeholder is unexplained: %s", help.stdout)
			}
			// Variations require a square PNG under 4 MB. Use a valid source
			// even though the local fixture does not enforce model restrictions.
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
			if strings.Count(got.stdout, "  openai images "+operation+" ") != 1 {
				t.Errorf("expected one complete example: %s", got.stdout)
			}
			if !strings.Contains(got.stdout, "Full help: openai help --all images "+operation) {
				t.Errorf("full help is not discoverable: %s", got.stdout)
			}
		})
	}
}
