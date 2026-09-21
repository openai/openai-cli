package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagepreview"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
)

type imageStreamTestBody struct {
	io.Reader
	closed bool
}

func (body *imageStreamTestBody) Close() error {
	body.closed = true
	return nil
}

func imageStreamTestSSE(text string) (*ssestream.Stream[openai.ImageGenStreamEventUnion], *imageStreamTestBody) {
	body := &imageStreamTestBody{Reader: strings.NewReader(text)}
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
	return ssestream.NewStream[openai.ImageGenStreamEventUnion](ssestream.NewDecoder(response), nil), body
}

func imageStreamTestPNG(t *testing.T, shade color.RGBA) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(x, y, shade)
		}
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	return pngBytes.Bytes(), base64.StdEncoding.EncodeToString(pngBytes.Bytes())
}

func imageStreamTestEvent(t *testing.T, kind, encoded string, index int) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"type": kind, "b64_json": encoded, "partial_image_index": index})
	if err != nil {
		t.Fatal(err)
	}
	return "event: " + kind + "\ndata: " + string(data) + "\n\n"
}

func imageStreamTestTemporaryRoot(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	t.Setenv("TMP", directory)
	t.Setenv("TEMP", directory)
	return directory
}

func TestImageStreamSavesFinalAndBoundsProgress(t *testing.T) {
	temporary := imageStreamTestTemporaryRoot(t)
	_, partial := imageStreamTestPNG(t, color.RGBA{R: 200, A: 255})
	finalBytes, final := imageStreamTestPNG(t, color.RGBA{G: 200, A: 255})
	var events strings.Builder
	for _, index := range []int{-1, 0, 0, 1, 2, 3, 100, 2} {
		events.WriteString(imageStreamTestEvent(t, "image_generation.partial_image", partial, index))
	}
	events.WriteString(imageStreamTestEvent(t, "image_generation.completed", final, 0))
	// Completion is authoritative even if a connection produces more data.
	events.WriteString("data: {\"error\":\"synthetic-private-prompt\"}\n\n")
	stream, body := imageStreamTestSSE(events.String())
	destination := t.TempDir()
	plan := &imageOutputPlan{directory: destination, name: "robot.png", partialImages: 3, preview: imagepreview.Kitty}
	var output bytes.Buffer
	if err := plan.saveStream(t.Context(), stream, &output); err != nil {
		t.Fatal(err)
	}
	if !body.closed {
		t.Fatal("completed stream was not closed")
	}
	if count := strings.Count(output.String(), "Progress preview "); count != 3 {
		t.Fatalf("progress count = %d, want 3 bounded unique previews", count)
	}
	for _, want := range []string{"Progress preview 1 of 3:", "Progress preview 3 of 3:", "Saved image:"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(output.String(), "synthetic-private-prompt") {
		t.Fatal("output exposed backend error payload")
	}
	got, err := os.ReadFile(filepath.Join(destination, "robot.png"))
	if err != nil || !bytes.Equal(got, finalBytes) {
		t.Fatalf("saved final image differs: %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 1 {
		t.Fatalf("partial images leaked into destination: %v, %v", entries, err)
	}
	assertImageStreamTemporaryEmpty(t, temporary)
}

func TestImageStreamPreviewFailureDoesNotLoseFinal(t *testing.T) {
	temporary := imageStreamTestTemporaryRoot(t)
	finalBytes, final := imageStreamTestPNG(t, color.RGBA{B: 200, A: 255})
	events := imageStreamTestEvent(t, "image_generation.partial_image", "synthetic-private-prompt\x1b]0;title\a", 0)
	events += imageStreamTestEvent(t, "image_generation.partial_image", "invalid-again", 1)
	events += imageStreamTestEvent(t, "image_generation.completed", final, 0)
	stream, _ := imageStreamTestSSE(events)
	destination := t.TempDir()
	plan := &imageOutputPlan{directory: destination, name: "robot", partialImages: 2, preview: imagepreview.Kitty}
	var output bytes.Buffer
	if err := plan.saveStream(t.Context(), stream, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "Progress preview unavailable") != 1 || !strings.Contains(output.String(), "Saved image:") {
		t.Fatalf("expected one preview warning and successful final save: %q", output.String())
	}
	if strings.Contains(output.String(), "synthetic-private-prompt") {
		t.Fatal("malformed partial leaked its contents")
	}
	got, err := os.ReadFile(filepath.Join(destination, "robot.png"))
	if err != nil || !bytes.Equal(got, finalBytes) {
		t.Fatalf("final image not retained: %v", err)
	}
	assertImageStreamTemporaryEmpty(t, temporary)
}

func TestImageStreamWithoutInlineOnlySavesFinal(t *testing.T) {
	temporary := imageStreamTestTemporaryRoot(t)
	_, encoded := imageStreamTestPNG(t, color.RGBA{R: 200, A: 255})
	events := imageStreamTestEvent(t, "image_generation.partial_image", encoded, 0) + imageStreamTestEvent(t, "image_generation.completed", encoded, 0)
	stream, _ := imageStreamTestSSE(events)
	plan := &imageOutputPlan{directory: t.TempDir(), name: "robot", partialImages: 3}
	var output bytes.Buffer
	if err := plan.saveStream(t.Context(), stream, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "Saved image:") || strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "Progress preview") {
		t.Fatalf("inline-off output changed: %q", output.String())
	}
	assertImageStreamTemporaryEmpty(t, temporary)
}

func TestImageStreamRequiresFinalAndDoesNotLeakErrors(t *testing.T) {
	_, encoded := imageStreamTestPNG(t, color.RGBA{R: 200, A: 255})
	for _, events := range []string{
		"",
		imageStreamTestEvent(t, "image_generation.partial_image", encoded, 0),
		"data: {\"error\":\"synthetic-private-prompt\"}\n\n",
		"data: synthetic-private-prompt\n\n",
	} {
		t.Run(fmt.Sprint(len(events)), func(t *testing.T) {
			temporary := imageStreamTestTemporaryRoot(t)
			stream, body := imageStreamTestSSE(events)
			plan := &imageOutputPlan{directory: t.TempDir(), partialImages: 1, preview: imagepreview.Kitty}
			var output bytes.Buffer
			err := plan.saveStream(t.Context(), stream, &output)
			if err == nil || !strings.Contains(err.Error(), "final image") || !strings.Contains(err.Error(), "API usage") {
				t.Fatalf("incomplete stream did not return recovery guidance: %v", err)
			}
			if strings.Contains(err.Error()+output.String(), "synthetic-private-prompt") || strings.Contains(output.String(), "Saved image:") {
				t.Fatalf("incomplete stream exposed payload or claimed success: %q / %q", err, output.String())
			}
			if !body.closed {
				t.Fatal("failed stream was not closed")
			}
			assertImageStreamTemporaryEmpty(t, temporary)
		})
	}
}

func TestImageStreamPreservesTypedAPIErrorAndCancellation(t *testing.T) {
	apierr := &openai.Error{StatusCode: 401}
	stream := ssestream.NewStream[openai.ImageGenStreamEventUnion](nil, apierr)
	plan := &imageOutputPlan{directory: t.TempDir()}
	if err := plan.saveStream(t.Context(), stream, io.Discard); err != apierr {
		t.Fatalf("initial API error type lost: %T", err)
	}
	_, encoded := imageStreamTestPNG(t, color.RGBA{R: 200, A: 255})
	for _, kind := range []string{"image_generation.partial_image", "image_generation.completed"} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		stream, body := imageStreamTestSSE(imageStreamTestEvent(t, kind, encoded, 0))
		var output bytes.Buffer
		if err := plan.saveStream(ctx, stream, &output); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled %s = %v", kind, err)
		}
		if output.Len() != 0 || !body.closed {
			t.Fatalf("canceled stream produced output or was not closed: %q", output.String())
		}
	}
}

type imageStreamCancelWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (writer *imageStreamCancelWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("Progress preview")) {
		writer.cancel()
	}
	return writer.Buffer.Write(data)
}

func TestImageStreamCancellationDuringPreviewCleansTemporaryFiles(t *testing.T) {
	temporary := imageStreamTestTemporaryRoot(t)
	_, encoded := imageStreamTestPNG(t, color.RGBA{R: 200, A: 255})
	events := imageStreamTestEvent(t, "image_generation.partial_image", encoded, 0) + imageStreamTestEvent(t, "image_generation.completed", encoded, 0)
	stream, body := imageStreamTestSSE(events)
	destination := t.TempDir()
	plan := &imageOutputPlan{directory: destination, partialImages: 1, preview: imagepreview.Kitty}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := &imageStreamCancelWriter{cancel: cancel}
	if err := plan.saveStream(ctx, stream, output); !errors.Is(err, context.Canceled) {
		t.Fatalf("preview cancellation = %v", err)
	}
	if !body.closed || strings.Contains(output.String(), "Saved image:") {
		t.Fatalf("canceled stream was not closed or falsely reported final success: %q", output.String())
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 0 {
		t.Fatalf("final saved after cancellation: %v, %v", entries, err)
	}
	assertImageStreamTemporaryEmpty(t, temporary)
}

func assertImageStreamTemporaryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary progress images remain: %v, %v", entries, err)
	}
}
