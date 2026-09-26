// demo-api serves fixed synthetic responses for local CLI recordings.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"
)

const syntheticImagePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: demo-api ADDRESS_FILE")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(serve), ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		shutdownDone <- server.Shutdown(shutdown)
	}()
	if err := os.WriteFile(os.Args[1], []byte("http://"+listener.Addr().String()+"/v1"), 0600); err != nil {
		panic(err)
	}
	err = server.Serve(listener)
	cancel()
	shutdownErr := <-shutdownDone
	if err != nil && err != http.ErrServerClosed {
		panic(err)
	}
	if shutdownErr != nil {
		panic(fmt.Errorf("shut down demo API: %w", shutdownErr))
	}
}

func serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	model := func(id string) map[string]any {
		return map[string]any{"id": id, "object": "model", "created": 1704067200, "owned_by": "demo-project"}
	}
	files := []map[string]any{
		{"id": "file_training", "object": "file", "created_at": 1704067200, "filename": "training.jsonl", "purpose": "fine-tune", "bytes": 8192, "status": "processed"},
		{"id": "file_reference", "object": "file", "created_at": 1704067100, "filename": "reference.pdf", "purpose": "assistants", "bytes": 4096, "status": "processed"},
	}
	var response any
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/files":
		response = map[string]any{"object": "list", "data": files, "has_more": false, "first_id": "file_training", "last_id": "file_reference"}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/files/file_training":
		response = files[0]
	case r.Method == http.MethodPost && r.URL.Path == "/v1/images/generations":
		serveImageGeneration(w, r)
		return
	case r.Method == http.MethodPost && (r.URL.Path == "/v1/images/edits" || r.URL.Path == "/v1/images/variations"):
		serveImageUpload(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		response = map[string]any{"object": "list", "data": []any{model("demo-chat"), model("demo-image")}}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models/demo-auth":
		w.WriteHeader(http.StatusUnauthorized)
		response = map[string]any{"error": map[string]any{"message": "The demo API key is invalid.", "type": "invalid_request_error", "param": nil, "code": "invalid_api_key"}}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models/demo-missing":
		w.WriteHeader(http.StatusNotFound)
		response = map[string]any{"error": map[string]any{"message": "The requested demo model does not exist.", "type": "invalid_request_error", "param": "model", "code": "model_not_found"}}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/models/"):
		response = model(strings.TrimPrefix(r.URL.Path, "/v1/models/"))
	default:
		w.WriteHeader(http.StatusNotFound)
		response = map[string]any{"error": map[string]any{"message": "No synthetic fixture for this route.", "type": "invalid_request_error", "code": "fixture_not_found"}}
	}
	_ = json.NewEncoder(w).Encode(response)
}

// Only the known synthetic request is accepted or logged. The same one-pixel
// PNG is returned to both binaries regardless of their generation defaults.
func serveImageGeneration(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	promptOnly := map[string]any{"prompt": "A tiny orange robot"}
	preset := map[string]any{
		"prompt": "A tiny orange robot", "model": "gpt-image-2.5-sunburst",
		"n": float64(1), "size": "auto", "quality": "auto", "output_format": "png",
		"background": "auto", "moderation": "auto", "partial_images": float64(0), "stream": false,
	}
	streamPreset := make(map[string]any, len(preset))
	progressPreset := make(map[string]any, len(preset))
	batchPreset := make(map[string]any, len(preset))
	for key, value := range preset {
		streamPreset[key], batchPreset[key] = value, value
		progressPreset[key] = value
	}
	streamPreset["stream"] = true
	progressPreset["stream"], progressPreset["partial_images"] = true, float64(2)
	batchPreset["n"] = float64(2)
	if r.Header.Get("Authorization") != "Bearer synthetic-demo-key" ||
		json.NewDecoder(r.Body).Decode(&request) != nil ||
		(!reflect.DeepEqual(request, promptOnly) && !reflect.DeepEqual(request, preset) &&
			!reflect.DeepEqual(request, streamPreset) && !reflect.DeepEqual(request, batchPreset) && !reflect.DeepEqual(request, progressPreset)) {
		http.Error(w, `{"error":{"message":"Expected the fixed synthetic image demo request.","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return
	}
	if path := os.Getenv("DEMO_IMAGE_REQUEST_LOG"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			http.Error(w, `{"error":{"message":"Cannot record the synthetic request.","type":"server_error"}}`, http.StatusInternalServerError)
			return
		}
		writeErr := json.NewEncoder(file).Encode(request)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			http.Error(w, `{"error":{"message":"Cannot record the synthetic request.","type":"server_error"}}`, http.StatusInternalServerError)
			return
		}
	}
	image := syntheticImagePNG
	if os.Getenv("DEMO_IMAGE_PREVIEW_FIXTURE") == "robot" {
		image = syntheticRobotPNG()
	}
	if reflect.DeepEqual(request, progressPreset) {
		w.Header().Set("Content-Type", "text/event-stream")
		for stage := 0; stage < 3; stage++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
			kind := "partial_image"
			if stage == 2 {
				kind = "completed"
			}
			fmt.Fprintf(w, "data: {\"type\":%q,\"partial_image_index\":%d,\"b64_json\":%q}\n\n", "image_generation."+kind, stage, syntheticProgressPNG(stage))
			w.(http.Flusher).Flush()
		}
		return
	}
	if reflect.DeepEqual(request, streamPreset) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":%q}\n\n", image)
		return
	}
	if reflect.DeepEqual(request, batchPreset) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
			map[string]any{"b64_json": image}, map[string]any{"b64_json": "invalid-synthetic-base64"},
		}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"created": 1704067200,
		"data": []any{map[string]any{
			"b64_json": image,
		}},
	})
}

// Short, wide pictures keep both partials and the final visible in one frame.
// Each stage adds deterministic detail to a locally drawn robot.
func syntheticProgressPNG(stage int) string {
	picture := image.NewRGBA(image.Rect(0, 0, 64, 12))
	fill := func(rect image.Rectangle, c color.RGBA) {
		draw.Draw(picture, rect, &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	fill(picture.Bounds(), color.RGBA{32, 48, 72, 255})
	fill(image.Rect(0, 10, 64, 12), color.RGBA{64, 88, 88, 255})
	orange := color.RGBA{160, 112, 64, 255}
	if stage > 0 {
		orange = color.RGBA{255, 152, 32, 255}
	}
	fill(image.Rect(27, 1, 37, 6), orange)
	fill(image.Rect(29, 7, 35, 10), orange)
	if stage > 0 {
		fill(image.Rect(28, 2, 36, 5), color.RGBA{8, 16, 24, 255})
		fill(image.Rect(26, 7, 28, 10), orange)
		fill(image.Rect(36, 7, 38, 10), orange)
	}
	if stage > 1 {
		fill(image.Rect(29, 3, 31, 4), color.RGBA{255, 255, 224, 255})
		fill(image.Rect(33, 3, 35, 4), color.RGBA{255, 255, 224, 255})
		fill(image.Rect(29, 10, 31, 12), orange)
		fill(image.Rect(33, 10, 35, 12), orange)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes())
}

// A deterministic test picture makes color fallback visible in recordings.
// This is drawn locally, never generated by an API or copied from user files.
func syntheticRobotPNG() string {
	picture := image.NewRGBA(image.Rect(0, 0, 64, 40))
	fill := func(rect image.Rectangle, c color.RGBA) {
		draw.Draw(picture, rect, &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	fill(picture.Bounds(), color.RGBA{32, 48, 72, 255})
	fill(image.Rect(0, 34, 64, 40), color.RGBA{64, 88, 88, 255})
	orange := color.RGBA{255, 152, 32, 255}
	fill(image.Rect(31, 3, 33, 9), orange)
	fill(image.Rect(22, 8, 42, 22), orange)
	fill(image.Rect(24, 11, 40, 19), color.RGBA{8, 16, 24, 255})
	fill(image.Rect(27, 13, 30, 16), color.RGBA{255, 255, 224, 255})
	fill(image.Rect(34, 13, 37, 16), color.RGBA{255, 255, 224, 255})
	fill(image.Rect(26, 23, 38, 31), orange)
	fill(image.Rect(21, 24, 24, 31), orange)
	fill(image.Rect(40, 24, 43, 31), orange)
	fill(image.Rect(26, 32, 30, 36), orange)
	fill(image.Rect(34, 32, 38, 36), orange)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes())
}

// Upload recordings accept only the fixed synthetic PNG and known scalar
// settings. Log hashes after validation, never arbitrary uploaded contents.
func serveImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer synthetic-demo-key" || r.ParseMultipartForm(1<<20) != nil {
		http.Error(w, "Expected the fixed synthetic multipart request.", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	values := r.MultipartForm.Value
	edit := r.URL.Path == "/v1/images/edits"
	before := map[string][]string{}
	after := map[string][]string{"model": {"dall-e-2"}, "response_format": {"b64_json"}}
	wantFiles := map[string]string{"image": "photo.png"}
	if edit {
		before = map[string][]string{"prompt": {"Make the sky purple"}}
		after = map[string][]string{
			"prompt": {"Make the sky purple"}, "model": {"gpt-image-2.5-sunburst"},
			"n": {"1"}, "size": {"auto"}, "quality": {"auto"}, "background": {"auto"},
			"output_format": {"png"}, "partial_images": {"0"}, "stream": {"false"},
		}
		wantFiles = map[string]string{"image[]": "photo.png", "mask": "mask.png"}
	}
	if (!reflect.DeepEqual(values, before) && !reflect.DeepEqual(values, after)) || len(r.MultipartForm.File) != len(wantFiles) {
		http.Error(w, "Unexpected synthetic image settings.", http.StatusBadRequest)
		return
	}
	png, _ := base64.StdEncoding.DecodeString(syntheticImagePNG)
	files := map[string]map[string]string{}
	for field, name := range wantFiles {
		uploads := r.MultipartForm.File[field]
		if len(uploads) != 1 || uploads[0].Filename != name {
			http.Error(w, "Unexpected synthetic upload filename.", http.StatusBadRequest)
			return
		}
		file, err := uploads[0].Open()
		if err != nil {
			http.Error(w, "Could not open synthetic upload.", http.StatusBadRequest)
			return
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(data, png) {
			http.Error(w, "Synthetic upload bytes changed.", http.StatusBadRequest)
			return
		}
		files[field] = map[string]string{"name": name, "sha256": fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	if path := os.Getenv("DEMO_IMAGE_UPLOAD_LOG"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			http.Error(w, "Could not record synthetic upload.", http.StatusInternalServerError)
			return
		}
		writeErr := json.NewEncoder(file).Encode(map[string]any{"path": r.URL.Path, "values": values, "files": files})
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			http.Error(w, "Could not record synthetic upload.", http.StatusInternalServerError)
			return
		}
	}
	image := map[string]string{"b64_json": syntheticImagePNG}
	if !edit && reflect.DeepEqual(values, before) {
		image = map[string]string{"url": "https://example.invalid/synthetic-variation.png"}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"created": 1704067200, "data": []any{image}})
}
