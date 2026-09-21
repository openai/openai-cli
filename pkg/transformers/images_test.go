package transformers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func imageValue(t *testing.T, value any) gjson.Result {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return gjson.ParseBytes(data)
}

func testImagePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, img))
	return data.Bytes()
}

func TestLoadGeneratedImageFormats(t *testing.T) {
	var jpegData bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil))
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	for name, data := range map[string][]byte{"png": testImagePNG(t), "jpeg": jpegData.Bytes(), "webp": webp} {
		t.Run(name, func(t *testing.T) {
			img, err := loadGeneratedImage(context.Background(), imageValue(t, map[string]string{
				"b64_json": base64.StdEncoding.EncodeToString(data),
			}))
			require.NoError(t, err)
			require.Positive(t, img.Bounds().Dx())
			require.Positive(t, img.Bounds().Dy())
		})
	}
}

func TestLoadGeneratedImageURLWithoutCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-api-key")
	data := testImagePNG(t)
	requests := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Api-Key"))
		require.Empty(t, r.Header.Get("Referer"))
		w.Write(data)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Empty(t, r.Header.Get("Authorization"))
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	img, err := loadGeneratedImage(context.Background(), imageValue(t, map[string]string{"url": server.URL + "/redirect?signature=synthetic-secret"}))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 2, 2), img.Bounds())
	require.Equal(t, 2, requests)
}

func TestLoadGeneratedImageFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("synthetic-secret-body"))
	}))
	defer server.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for _, item := range []map[string]string{
		{}, {"b64_json": "not-base64\x1b]52;c;payload\a"},
		{"b64_json": base64.StdEncoding.EncodeToString([]byte("not an image"))},
		{"url": "file:///synthetic-secret"},
		{"url": "https://user:synthetic-secret@example.invalid/image"},
		{"url": server.URL + "/?signature=synthetic-secret"},
		{"url": closed.URL + "/?signature=synthetic-secret"},
	} {
		_, err := loadGeneratedImage(context.Background(), imageValue(t, item))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "synthetic-secret")
		require.NotContains(t, err.Error(), "\x1b")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := loadGeneratedImage(ctx, imageValue(t, map[string]string{"url": server.URL}))
	require.ErrorIs(t, err, context.Canceled)
}

func TestGeneratedImagesTerminalRouting(t *testing.T) {
	route := Route{Operation: "(resource) images > (method) generate", OutputKind: OutputResponse}
	require.NotNil(t, SelectTerminal(route))
	for _, kind := range []OutputKind{OutputUnspecified, OutputPageItem, OutputStreamEvent} {
		route.OutputKind = kind
		require.Nil(t, SelectTerminal(route))
	}
	route.OutputKind = OutputResponse
	route.Operation = "(resource) images > (method) edit"
	require.Nil(t, SelectTerminal(route))
}

func TestImageProtocol(t *testing.T) {
	apple := "blocks"
	if runtime.GOOS == "darwin" {
		apple = "font"
	}
	for _, name := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "STY", "ZELLIJ", "CI"} {
		t.Setenv(name, "")
	}
	for _, test := range []struct{ program, terminal, tmux, want string }{
		{"iTerm.app", "xterm-256color", "", "iterm"},
		{"WezTerm", "xterm-256color", "", "iterm"},
		{"WarpTerminal", "xterm-256color", "", "iterm"},
		{"ghostty", "xterm-256color", "", "kitty"},
		{"", "xterm-kitty", "", "kitty"},
		{"Apple_Terminal", "xterm-256color", "", apple},
		{"unknown", "xterm-256color", "", "blocks"},
		{"iTerm.app", "tmux-256color", "synthetic-session", "blocks"},
		{"iTerm.app", "screen-256color", "", "blocks"},
		{"iTerm.app", "dumb", "", ""},
	} {
		t.Run(test.program+"/"+test.terminal, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", test.program)
			t.Setenv("TERM", test.terminal)
			t.Setenv("TMUX", test.tmux)
			require.Equal(t, test.want, imageProtocol())
		})
	}
}

func TestRenderGeneratedImages(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "WarpTerminal")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TMUX", "")
	file, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer file.Close()
	encoded := base64.StdEncoding.EncodeToString(testImagePNG(t))
	value := imageValue(t, map[string]any{"data": []any{
		map[string]string{"b64_json": encoded, "revised_prompt": "\x1b]52;c;payload\a"},
		map[string]string{"b64_json": encoded},
	}})
	handled, err := renderGeneratedImages(context.Background(), value, file)
	require.NoError(t, err)
	require.True(t, handled)
	output, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(output), "\x1b]1337;File="))
	require.NotContains(t, string(output), "payload")

	handled, err = renderGeneratedImages(context.Background(), gjson.Parse(`{"data":[]}`), file)
	require.NoError(t, err)
	require.False(t, handled)
	require.NoError(t, file.Close())
	handled, err = renderGeneratedImages(context.Background(), value, file)
	require.True(t, handled)
	require.ErrorIs(t, err, os.ErrClosed)
}
