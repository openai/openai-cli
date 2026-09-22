package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/terminalimage"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the shared output boundary with a real PTY. Kitty bytes are captured
// by script; no terminal app, font registration, or desktop settings are used.
func TestCombinedImageOutputBoundary(t *testing.T) {
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, pixel))
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		downloads.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/pixel.png" {
			t.Errorf("unexpected image request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" {
			t.Error("image download included API authentication")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(encoded.Bytes())
	}))
	defer server.Close()
	response, err := json.Marshal(map[string]any{
		"created": 123,
		"data":    []map[string]string{{"url": server.URL + "/pixel.png"}},
	})
	require.NoError(t, err)

	t.Run("ordinary writer does not render or download", func(t *testing.T) {
		var output bytes.Buffer
		require.NoError(t, ShowJSON(gjson.ParseBytes(response), ShowJSONOpts{
			Context: t.Context(), Operation: transformers.ImageGenerateOperation,
			OutputKind: OutputResponse, Format: "auto", Stdout: &output,
		}))
		require.Contains(t, output.String(), server.URL+"/pixel.png")
		require.NotContains(t, output.String(), "\x1b_G")
		require.Zero(t, downloads.Load())
	})

	for _, test := range []struct {
		mode      string
		downloads int32
		graphics  int
		saved     bool
		json      bool
	}{
		{mode: "unconfigured", downloads: 1, graphics: 1},
		{mode: "configured-url"},
		{mode: "configured-save", graphics: 1, saved: true},
		{mode: "unconfigured-json", json: true},
		{mode: "configured-json", json: true},
	} {
		t.Run(test.mode, func(t *testing.T) {
			downloads.Store(0)
			directory := t.TempDir()
			fixture := response
			if test.saved {
				// The saving workflow requests base64; legacy URL output is an
				// explicit opt-out. The old renderer also accepts base64, so an
				// accidental second invocation is caught by the transfer count.
				fixture, err = json.Marshal(map[string]any{
					"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes())}},
				})
				require.NoError(t, err)
			}
			output := runCombinedImageBoundaryProcess(t, test.mode, string(fixture), directory)
			require.Equal(t, test.downloads, downloads.Load(), output)
			// The tiny PNG fits in one Kitty chunk. A second renderer produces
			// a second transfer, even if it reuses rather than downloads data.
			require.Equal(t, test.graphics, strings.Count(output, "\x1b_G"), output)
			files, err := os.ReadDir(directory)
			require.NoError(t, err)
			if test.saved {
				require.Len(t, files, 1, output)
				require.Equal(t, "combined-pixel.png", files[0].Name())
				contents, err := os.ReadFile(filepath.Join(directory, files[0].Name()))
				require.NoError(t, err)
				require.Equal(t, encoded.Bytes(), contents)
				require.Equal(t, 1, strings.Count(output, "Saved image:"), output)
			} else {
				require.Empty(t, files, "presentation unexpectedly saved a file")
				require.NotContains(t, output, "Saved image:")
			}
			if test.graphics == 0 {
				require.Contains(t, output, server.URL+"/pixel.png")
			}
			if test.json {
				require.JSONEq(t, string(response), strings.TrimSpace(output))
			}
		})
	}
}

func TestCombinedImageOutputBoundaryProcess(t *testing.T) {
	mode := os.Getenv("OPENAI_CLI_COMBINED_BOUNDARY_MODE")
	if mode == "" {
		return
	}
	require.True(t, isTerminal(os.Stdout), "helper must run with a genuine terminal writer")
	ctx := t.Context()
	if strings.HasPrefix(mode, "configured-") {
		presentation := imagePresentation{writer: os.Stdout}
		if mode == "configured-save" {
			presentation.plan = &imageOutputPlan{
				directory: os.Getenv("OPENAI_CLI_COMBINED_BOUNDARY_DIRECTORY"),
				name:      "combined-pixel", preview: terminalimage.Kitty,
			}
			ctx = transformers.WithImageOutput(ctx)
		}
		ctx = context.WithValue(ctx, imagePresentationKey{}, presentation)
	}
	format, explicit := "auto", false
	if strings.HasSuffix(mode, "-json") {
		format, explicit = "json", true
	}
	fmt.Fprintln(os.Stdout, "COMBINED_OUTPUT_BEGIN")
	require.NoError(t, ShowJSON(gjson.Parse(os.Getenv("OPENAI_CLI_COMBINED_BOUNDARY_RESPONSE")), ShowJSONOpts{
		Context: ctx, Operation: transformers.ImageGenerateOperation,
		OutputKind: OutputResponse, Format: format, ExplicitFormat: explicit, Stdout: os.Stdout,
	}))
	fmt.Fprintln(os.Stdout, "COMBINED_OUTPUT_END")
}

func runCombinedImageBoundaryProcess(t *testing.T, mode, response, directory string) string {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("PTY boundary integration uses Unix script")
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script is unavailable for PTY integration")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	child := []string{binary, "-test.run=^TestCombinedImageOutputBoundaryProcess$", "-test.count=1"}
	args := append([]string{"-q", "/dev/null"}, child...)
	if runtime.GOOS == "linux" {
		quoted := make([]string, 0, len(child))
		for _, arg := range child {
			quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\\''")+"'")
		}
		args = []string{"-q", "-e", "-c", strings.Join(quoted, " "), "/dev/null"}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, script, args...)
	command.WaitDelay = time.Second
	command.Dir = directory
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "OPENAI_") || key == "HOME" || key == "USERPROFILE" ||
			key == "XDG_CONFIG_HOME" || key == "XDG_CACHE_HOME" ||
			key == "TERM" || key == "TERM_PROGRAM" || key == "TERM_PROGRAM_VERSION" ||
			key == "TMUX" || key == "STY" || key == "ZELLIJ" || key == "CI" ||
			key == "NO_COLOR" || key == "FORCE_COLOR" || key == "CLICOLOR" || key == "CLICOLOR_FORCE" ||
			key == "HTTP_PROXY" || key == "HTTPS_PROXY" || key == "ALL_PROXY" || key == "NO_PROXY" {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env,
		"OPENAI_CLI_COMBINED_BOUNDARY_MODE="+mode,
		"OPENAI_CLI_COMBINED_BOUNDARY_RESPONSE="+response,
		"OPENAI_CLI_COMBINED_BOUNDARY_DIRECTORY="+directory,
		"HOME="+directory, "USERPROFILE="+directory,
		"XDG_CONFIG_HOME="+directory, "XDG_CACHE_HOME="+directory,
		"TERM=xterm-kitty", "TERM_PROGRAM=kitty", "NO_COLOR=1", "FORCE_COLOR=0",
		"NO_PROXY=127.0.0.1,localhost",
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	// Package initialization can issue color queries before the helper runs.
	// Isolate this boundary's output without discarding any bytes it produced.
	_, rendered, began := strings.Cut(string(output), "COMBINED_OUTPUT_BEGIN\r\n")
	require.True(t, began, "%s", output)
	rendered, after, ended := strings.Cut(rendered, "COMBINED_OUTPUT_END\r\n")
	require.True(t, ended, "%s", output)
	require.Contains(t, after, "PASS", "%s", output)
	return rendered
}
