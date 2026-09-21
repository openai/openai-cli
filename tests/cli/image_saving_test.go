package cli_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This process test also runs as a standalone cross-compiled test executable.
func TestMainImageUploadsSaveAndKeepOriginals(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"created":123,"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(picture.Bytes()))
	for _, operation := range []string{"edit", "create-variation"} {
		t.Run(operation, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.png")
			if err := os.WriteFile(source, picture.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			type request struct {
				path, model, format string
				uploads             int
			}
			requests := make(chan request, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					http.Error(w, "invalid synthetic upload", 400)
					return
				}
				defer r.MultipartForm.RemoveAll()
				got := request{path: r.URL.Path, model: r.FormValue("model"), format: r.FormValue("response_format")}
				for _, uploads := range r.MultipartForm.File {
					for _, upload := range uploads {
						file, err := upload.Open()
						if err != nil {
							t.Error(err)
							continue
						}
						data, err := io.ReadAll(file)
						file.Close()
						if err != nil || !bytes.Equal(data, picture.Bytes()) {
							t.Error("uploaded source changed")
						}
						got.uploads++
					}
				}
				requests <- got
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, payload)
			}))
			defer server.Close()
			args := []string{"images", operation, "--image", source}
			wantModel, wantPath := "dall-e-2", "/images/variations"
			if operation == "edit" {
				args = append(args, "--prompt", "A purple sky")
				wantModel, wantPath = "gpt-image-2.5-sunburst", "/images/edits"
			}
			home := t.TempDir()
			got := runReadableMain(t, server.URL, []string{"HOME=" + home, "USERPROFILE=" + home}, args...)
			assertReadableSuccess(t, got, "Saved image:")
			captured := <-requests
			if captured.path != wantPath || captured.model != wantModel || captured.uploads != 1 || (operation == "create-variation" && captured.format != "b64_json") {
				t.Fatalf("unexpected multipart request: %+v", captured)
			}
			folder := filepath.Join(home, "Downloads", "gpt-images")
			files, err := os.ReadDir(folder)
			if err != nil || len(files) != 1 {
				t.Fatalf("saved files: %v (%v)", files, err)
			}
			saved, err := os.ReadFile(filepath.Join(folder, files[0].Name()))
			if err != nil || !bytes.Equal(saved, picture.Bytes()) {
				t.Fatal("saved image differs from response")
			}
			if operation == "edit" && files[0].Name() != "purple-sky.png" {
				t.Fatalf("unexpected prompt filename: %s", files[0].Name())
			}
			original, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(original, picture.Bytes()) {
				t.Fatal("source image changed")
			}

			home = t.TempDir()
			got = runReadableMain(t, server.URL, []string{"HOME=" + home, "USERPROFILE=" + home}, append([]string{"--format", "json"}, args...)...)
			assertReadableProcessSuccess(t, got)
			assertReadableJSONValues(t, got.stdout, payload)
			<-requests
			if _, err := os.Stat(filepath.Join(home, "Downloads")); !os.IsNotExist(err) || strings.Contains(got.stdout, "Saved image:") {
				t.Fatal("explicit JSON unexpectedly saved an image")
			}
		})
	}
}
