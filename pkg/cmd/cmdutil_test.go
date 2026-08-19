package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/openai/openai-cli/internal/jsonview"
)

func TestStreamOutput(t *testing.T) {
	t.Setenv("PAGER", "cat")
	err := streamOutput("stream test", func(w *os.File) error {
		_, writeErr := w.WriteString("Hello world\n")
		return writeErr
	})
	if err != nil {
		t.Errorf("streamOutput failed: %v", err)
	}
}

func TestWriteBinaryResponse(t *testing.T) {
	t.Run("write to explicit file", func(t *testing.T) {
		tmpDir := t.TempDir()
		outfile := tmpDir + "/output.txt"
		body := []byte("test content")
		resp := &http.Response{
			Body: io.NopCloser(bytes.NewReader(body)),
		}

		msg, err := writeBinaryResponse(resp, os.Stdout, outfile)

		require.NoError(t, err)
		assert.Contains(t, msg, outfile)

		content, err := os.ReadFile(outfile)
		require.NoError(t, err)
		assert.Equal(t, body, content)
		assertDownloadPermissions(t, outfile, 0600)
	})

	t.Run("overwrite existing file without changing permissions", func(t *testing.T) {
		for _, permissions := range []os.FileMode{0600, 0640, 0644} {
			t.Run(fmt.Sprintf("%04o", permissions), func(t *testing.T) {
				outfile := filepath.Join(t.TempDir(), "output.txt")
				require.NoError(t, os.WriteFile(outfile, []byte("existing content to truncate"), permissions))
				require.NoError(t, os.Chmod(outfile, permissions))

				body := []byte("new")
				resp := &http.Response{Body: io.NopCloser(bytes.NewReader(body))}
				msg, err := writeBinaryResponse(resp, os.Stdout, outfile)

				require.NoError(t, err)
				assert.Contains(t, msg, outfile)
				content, err := os.ReadFile(outfile)
				require.NoError(t, err)
				assert.Equal(t, body, content, "existing output should be overwritten and truncated")
				assertDownloadPermissions(t, outfile, permissions)
			})
		}
	})

	t.Run("write to stdout", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		body := []byte("stdout content")
		resp := &http.Response{
			Body: io.NopCloser(bytes.NewReader(body)),
		}
		msg, err := writeBinaryResponse(resp, &buf, "-")

		require.NoError(t, err)
		assert.Empty(t, msg)
		assert.Equal(t, body, buf.Bytes())
	})
}

func TestCreateDownloadFile(t *testing.T) {
	t.Run("creates file with filename from header", func(t *testing.T) {
		t.Chdir(t.TempDir())

		resp := &http.Response{
			Header: http.Header{
				"Content-Disposition": []string{`attachment; filename="test.txt"`},
			},
		}
		file, err := createDownloadFile(resp, []byte("test content"))
		require.NoError(t, err)
		defer file.Close()
		assert.Equal(t, "test.txt", filepath.Base(file.Name()))
		assertDownloadPermissions(t, file.Name(), 0600)
		_, err = file.WriteString("original content")
		require.NoError(t, err)

		// Create a second file with the same name to ensure it doesn't clobber the first
		resp2 := &http.Response{
			Header: http.Header{
				"Content-Disposition": []string{`attachment; filename="test.txt"`},
			},
		}
		file2, err := createDownloadFile(resp2, []byte("second content"))
		require.NoError(t, err)
		defer file2.Close()
		assert.NotEqual(t, file.Name(), file2.Name(), "second file should have a different name")
		assert.Contains(t, filepath.Base(file2.Name()), "test")
		assertDownloadPermissions(t, file2.Name(), 0600)
		original, err := os.ReadFile(file.Name())
		require.NoError(t, err)
		assert.Equal(t, "original content", string(original))
	})

	t.Run("creates temp file when no header", func(t *testing.T) {
		t.Chdir(t.TempDir())

		resp := &http.Response{Header: http.Header{}}
		file, err := createDownloadFile(resp, []byte("test content"))
		require.NoError(t, err)
		defer file.Close()
		assert.Contains(t, filepath.Base(file.Name()), "file-")
		assertDownloadPermissions(t, file.Name(), 0600)
	})

	t.Run("prevents directory traversal", func(t *testing.T) {
		t.Chdir(t.TempDir())

		resp := &http.Response{
			Header: http.Header{
				"Content-Disposition": []string{`attachment; filename="../../../etc/passwd"`},
			},
		}
		file, err := createDownloadFile(resp, []byte("test content"))
		require.NoError(t, err)
		defer file.Close()
		assert.Equal(t, "passwd", filepath.Base(file.Name()))
		assertDownloadPermissions(t, file.Name(), 0600)
	})

	t.Run("prevents encoded directory traversal", func(t *testing.T) {
		root := t.TempDir()
		downloadDir := filepath.Join(root, "downloads")
		require.NoError(t, os.Mkdir(downloadDir, 0700))
		t.Chdir(downloadDir)

		outside := filepath.Join(root, "outside.txt")
		require.NoError(t, os.WriteFile(outside, []byte("untouched"), 0600))
		resp := &http.Response{
			Header: http.Header{
				"Content-Disposition": []string{"attachment; filename*=UTF-8''..%2Foutside.txt"},
			},
		}

		file, err := createDownloadFile(resp, []byte("download content"))
		require.NoError(t, err)
		defer file.Close()
		assert.Equal(t, "outside.txt", filepath.Base(file.Name()))
		assertDownloadPermissions(t, file.Name(), 0600)
		original, err := os.ReadFile(outside)
		require.NoError(t, err)
		assert.Equal(t, "untouched", string(original))
	})

	t.Run("does not follow an existing symlink", func(t *testing.T) {
		root := t.TempDir()
		downloadDir := filepath.Join(root, "downloads")
		require.NoError(t, os.Mkdir(downloadDir, 0700))
		t.Chdir(downloadDir)

		target := filepath.Join(root, "target.txt")
		require.NoError(t, os.WriteFile(target, []byte("untouched"), 0600))
		if err := os.Symlink(target, "download.txt"); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("creating symlinks on Windows requires an available privilege: %v", err)
			}
			require.NoError(t, err)
		}

		resp := &http.Response{
			Header: http.Header{
				"Content-Disposition": []string{`attachment; filename="download.txt"`},
			},
		}
		file, err := createDownloadFile(resp, []byte("download content"))
		require.NoError(t, err)
		defer file.Close()
		assert.NotEqual(t, "download.txt", filepath.Base(file.Name()))
		assertDownloadPermissions(t, file.Name(), 0600)
		original, err := os.ReadFile(target)
		require.NoError(t, err)
		assert.Equal(t, "untouched", string(original))
	})

	t.Run("concurrent downloads use unique private files", func(t *testing.T) {
		t.Chdir(t.TempDir())

		resp := &http.Response{
			Header: http.Header{
				"Content-Disposition": []string{`attachment; filename="download.txt"`},
			},
		}
		body := []byte("download content")
		type result struct {
			filename string
			err      error
		}
		const downloadCount = 16
		results := make(chan result, downloadCount)

		for range downloadCount {
			go func() {
				file, err := createDownloadFile(resp, body)
				if err != nil {
					results <- result{err: err}
					return
				}
				filename := file.Name()
				if _, err := file.Write(body); err != nil {
					file.Close()
					results <- result{err: fmt.Errorf("write %s: %w", filename, err)}
					return
				}
				if err := file.Close(); err != nil {
					results <- result{err: fmt.Errorf("close %s: %w", filename, err)}
					return
				}
				results <- result{filename: filename}
			}()
		}

		filenames := make(map[string]bool, downloadCount)
		for range downloadCount {
			result := <-results
			require.NoError(t, result.err)
			assert.False(t, filenames[result.filename], "download filename %q was reused", result.filename)
			filenames[result.filename] = true
			assertDownloadPermissions(t, result.filename, 0600)
			content, err := os.ReadFile(result.filename)
			require.NoError(t, err)
			assert.Equal(t, body, content)
		}
		assert.Len(t, filenames, downloadCount)
		assert.True(t, filenames["download.txt"], "one download should use the suggested filename")
	})
}

func assertDownloadPermissions(t *testing.T, filename string, expected os.FileMode) {
	t.Helper()

	info, err := os.Stat(filename)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, expected, info.Mode().Perm(), "unexpected permissions for %q", filename)
	}
}

func TestValidateBaseURL(t *testing.T) {
	t.Parallel()

	t.Run("ValidHTTPS", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, ValidateBaseURL("https://api.example.com", "--base-url"))
	})

	t.Run("ValidHTTP", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, ValidateBaseURL("http://localhost:8080", "--base-url"))
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, ValidateBaseURL("", "MY_BASE_URL"))
	})

	t.Run("MissingScheme", func(t *testing.T) {
		t.Parallel()

		err := ValidateBaseURL("localhost:8080", "MY_BASE_URL")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MY_BASE_URL")
		assert.Contains(t, err.Error(), "missing a scheme")
	})

	t.Run("HostOnly", func(t *testing.T) {
		t.Parallel()

		err := ValidateBaseURL("api.example.com", "--base-url")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--base-url")
	})
}

func TestFormatJSON(t *testing.T) {
	t.Parallel()

	t.Run("RawWithTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123","name":"test"}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "raw", Stdout: os.Stdout, Transform: "id"})
		require.NoError(t, err)
		require.Equal(t, `"abc123"`+"\n", string(formatted))
	})

	t.Run("RawWithoutTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123","name":"test"}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "raw", Stdout: os.Stdout})
		require.NoError(t, err)
		require.Equal(t, `{"id":"abc123","name":"test"}`+"\n", string(formatted))
	})

	t.Run("RawWithNestedTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"data":{"items":[1,2,3]}}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "raw", Stdout: os.Stdout, Transform: "data.items"})
		require.NoError(t, err)
		require.Equal(t, "[1,2,3]\n", string(formatted))
	})

	t.Run("RawWithNonexistentTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123"}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "raw", Stdout: os.Stdout, Transform: "missing"})
		require.NoError(t, err)
		// Transform path doesn't exist, so original result is returned
		require.Equal(t, `{"id":"abc123"}`+"\n", string(formatted))
	})

	t.Run("RawOutputString", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123","name":"test"}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "json", Stdout: os.Stdout, Transform: "id", RawOutput: true})
		require.NoError(t, err)
		require.Equal(t, "abc123\n", string(formatted))
	})

	t.Run("RawOutputNonString", func(t *testing.T) {
		t.Parallel()

		// --raw-output has no effect on non-string values
		res := gjson.Parse(`{"count":42}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "raw", Stdout: os.Stdout, Transform: "count", RawOutput: true})
		require.NoError(t, err)
		require.Equal(t, "42\n", string(formatted))
	})

	t.Run("RawOutputObject", func(t *testing.T) {
		t.Parallel()

		// --raw-output has no effect on objects
		res := gjson.Parse(`{"nested":{"a":1}}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "raw", Stdout: os.Stdout, Transform: "nested", RawOutput: true})
		require.NoError(t, err)
		require.Equal(t, `{"a":1}`+"\n", string(formatted))
	})

	t.Run("PrettyEscapesDecodedTerminalControlSequences", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"message":"\u001b]52;c;ZGF0YQ==\u0007"}`)
		formatted, err := formatJSON(res, ShowJSONOpts{Format: "pretty", Stdout: os.Stdout})
		require.NoError(t, err)

		out := string(formatted)
		require.NotContains(t, out, "\x1b]52")
		require.NotContains(t, out, "\a")
		require.Contains(t, out, `\u001b]52;c;ZGF0YQ==\u0007`)
	})
}

func TestShowJSONIterator(t *testing.T) {
	t.Parallel()

	t.Run("RawMultipleItems", func(t *testing.T) {
		t.Parallel()

		iter := &sliceIterator[map[string]any]{items: []map[string]any{
			{"id": "abc", "name": "first"},
			{"id": "def", "name": "second"},
		}}
		captured := captureShowJSONIterator(t, iter, "raw", "", -1)
		assert.Equal(t, `{"id":"abc","name":"first"}`+"\n"+`{"id":"def","name":"second"}`+"\n", captured)
	})

	t.Run("RawWithTransform", func(t *testing.T) {
		t.Parallel()

		iter := &sliceIterator[map[string]any]{items: []map[string]any{
			{"id": "abc", "name": "first"},
			{"id": "def", "name": "second"},
		}}
		captured := captureShowJSONIterator(t, iter, "raw", "id", -1)
		assert.Equal(t, `"abc"`+"\n"+`"def"`+"\n", captured)
	})

	t.Run("LimitItems", func(t *testing.T) {
		t.Parallel()

		iter := &sliceIterator[map[string]any]{items: []map[string]any{
			{"id": "abc"},
			{"id": "def"},
			{"id": "ghi"},
		}}
		captured := captureShowJSONIterator(t, iter, "raw", "", 2)
		assert.Equal(t, `{"id":"abc"}`+"\n"+`{"id":"def"}`+"\n", captured)
	})
}

func TestExploreFallback(t *testing.T) {
	t.Parallel()

	t.Run("ShowJSONFallsBackToJsonOnNonTTY", func(t *testing.T) {
		t.Parallel()

		// os.Pipe() produces a *os.File that isn't a terminal, so explore should fall back.
		r, w, err := os.Pipe()
		require.NoError(t, err)
		defer r.Close()

		var stderr bytes.Buffer
		res := gjson.Parse(`{"id":"abc"}`)
		err = ShowJSON(res, ShowJSONOpts{
			Format: "explore",
			Stderr: &stderr,
			Stdout: w,
			Title:  "test",
		})
		w.Close()
		require.NoError(t, err)

		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		assert.Contains(t, buf.String(), `"id"`)
		assert.Contains(t, buf.String(), `"abc"`)
	})

	t.Run("ShowJSONIteratorFallsBackToJsonOnNonTTY", func(t *testing.T) {
		t.Parallel()

		iter := &sliceIterator[map[string]any]{items: []map[string]any{
			{"id": "abc"},
		}}
		captured := captureShowJSONIterator(t, iter, "explore", "", -1)
		assert.Contains(t, captured, `"id"`)
		assert.Contains(t, captured, `"abc"`)
	})

	t.Run("ShowJSONWarnsWhenExplicitFormatOnNonTTY", func(t *testing.T) {
		t.Parallel()

		r, w, err := os.Pipe()
		require.NoError(t, err)
		defer r.Close()

		var stderr bytes.Buffer
		res := gjson.Parse(`{"id":"abc"}`)
		err = ShowJSON(res, ShowJSONOpts{
			ExplicitFormat: true,
			Format:         "explore",
			Stderr:         &stderr,
			Stdout:         w,
			Title:          "test",
		})
		w.Close()
		require.NoError(t, err)

		assert.Equal(t, warningExploreNotSupported, stderr.String())
	})

	t.Run("ShowJSONSilentWhenDefaultFormatOnNonTTY", func(t *testing.T) {
		t.Parallel()

		r, w, err := os.Pipe()
		require.NoError(t, err)
		defer r.Close()

		var stderr bytes.Buffer
		res := gjson.Parse(`{"id":"abc"}`)
		err = ShowJSON(res, ShowJSONOpts{
			Format: "explore",
			Stderr: &stderr,
			Stdout: w,
			Title:  "test",
		})
		w.Close()
		require.NoError(t, err)

		assert.Empty(t, stderr.String(), "no warning expected when format was not explicit")
	})
}

// sliceIterator is a simple iterator over a slice for testing.
type sliceIterator[T any] struct {
	index int
	items []T
}

func (it *sliceIterator[T]) Next() bool {
	it.index++
	return it.index <= len(it.items)
}

func (it *sliceIterator[T]) Current() T {
	return it.items[it.index-1]
}

func (it *sliceIterator[T]) Err() error {
	return nil
}

var _ jsonview.Iterator[any] = (*sliceIterator[any])(nil)

// captureShowJSONIterator runs ShowJSONIterator and captures the output written to a file.
func captureShowJSONIterator[T any](t *testing.T, iter jsonview.Iterator[T], format, transform string, itemsToDisplay int64) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()

	err = ShowJSONIterator(iter, itemsToDisplay, ShowJSONOpts{
		Format:    format,
		Stderr:    io.Discard,
		Stdout:    w,
		Title:     "test",
		Transform: transform,
	})
	w.Close()
	require.NoError(t, err)

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

func TestFormatJSONRawOutputEscapesInteractiveTerminalControls(t *testing.T) {
	t.Parallel()

	terminal := os.Stdout
	if !isTerminal(terminal) {
		var err error
		terminal, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
		if err != nil {
			t.Skipf("terminal unavailable: %v", err)
		}
		t.Cleanup(func() { require.NoError(t, terminal.Close()) })
		if !isTerminal(terminal) {
			t.Skip("pseudo-terminal master does not expose terminal attributes on this platform")
		}
	}
	require.True(t, isTerminal(terminal), "test must exercise the actual terminal boundary")

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "OSC52Clipboard", value: "\x1b]52;c;ZGF0YQ==\a", want: `\u001b]52;c;ZGF0YQ==\u0007`},
		{name: "BEL", value: "before\aafter", want: `before\u0007after`},
		{name: "ESC", value: "\x1b[31mspoofed\x1b[0m", want: `\u001b[31mspoofed\u001b[0m`},
		{name: "C1CSI", value: "\u009b31mspoofed", want: `\u009b31mspoofed`},
		{name: "MaliciousTitle", value: "\x1b]2;spoofed title\a", want: `\u001b]2;spoofed title\u0007`},
		{name: "WhitespaceControls", value: "first\nsecond\r\t\b\f", want: `first\nsecond\r\t\b\f`},
		{name: "DEL", value: "before\x7fafter", want: `before\u007fafter`},
		{name: "Unicode", value: "safe ☕ text", want: "safe ☕ text"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			for _, transformed := range []bool{false, true} {
				name := "DirectString"
				if transformed {
					name = "TransformedString"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					res, transform := rawOutputResult(t, test.value, transformed)
					formatted, err := formatJSON(res, ShowJSONOpts{
						Format:    "json",
						RawOutput: true,
						Stdout:    terminal,
						Transform: transform,
					})
					require.NoError(t, err)
					require.Equal(t, test.want+"\n", string(formatted))
				})
			}
		})
	}
}

func TestFormatJSONRawOutputPreservesNonTerminalBytes(t *testing.T) {
	t.Parallel()

	value := "\x1b]52;c;ZGF0YQ==\a\u009b31m\nnext\r\t\b\f\x7f☕"

	destinations := []struct {
		name string
		open func(t *testing.T) *os.File
	}{
		{
			name: "Pipe",
			open: func(t *testing.T) *os.File {
				r, w, err := os.Pipe()
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, r.Close()) })
				t.Cleanup(func() { require.NoError(t, w.Close()) })
				return w
			},
		},
		{
			name: "File",
			open: func(t *testing.T) *os.File {
				file, err := os.CreateTemp(t.TempDir(), "raw-output-*")
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, file.Close()) })
				return file
			},
		},
	}

	for _, destination := range destinations {
		t.Run(destination.name, func(t *testing.T) {
			t.Parallel()

			stdout := destination.open(t)
			require.False(t, isTerminal(stdout))

			for _, transformed := range []bool{false, true} {
				name := "DirectString"
				if transformed {
					name = "TransformedString"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					res, transform := rawOutputResult(t, value, transformed)
					formatted, err := formatJSON(res, ShowJSONOpts{
						Format:    "json",
						RawOutput: true,
						Stdout:    stdout,
						Transform: transform,
					})
					require.NoError(t, err)
					require.Equal(t, append([]byte(value), '\n'), formatted)
				})
			}
		})
	}
}

func TestShowJSONRawOutputPreservesPipelineBytes(t *testing.T) {
	t.Parallel()

	value := "\x1b]52;c;ZGF0YQ==\a\u009b31m\nnext"
	res, transform := rawOutputResult(t, value, true)
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	require.NoError(t, ShowJSON(res, ShowJSONOpts{
		Format:    "json",
		RawOutput: true,
		Stdout:    w,
		Transform: transform,
	}))
	require.NoError(t, w.Close())

	output, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, append([]byte(value), '\n'), output)
}

func TestShowJSONIteratorRawOutputPreservesPipelineBytes(t *testing.T) {
	t.Parallel()

	first := "\x1b]52;c;ZGF0YQ==\a"
	second := "\u009b31mspoofed\x1b]2;title\a"
	iter := &sliceIterator[map[string]any]{items: []map[string]any{
		{"message": first},
		{"message": second},
	}}
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	require.NoError(t, ShowJSONIterator(iter, -1, ShowJSONOpts{
		Format:    "json",
		RawOutput: true,
		Stdout:    w,
		Transform: "message",
	}))
	require.NoError(t, w.Close())

	output, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, []byte(first+"\n"+second+"\n"), output)
}

func TestFormatJSONForOutputUsesFinalDestinationForColors(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("color destination regression requires an interactive stdout terminal")
	}

	readPipe, writePipe, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, readPipe.Close()) })
	t.Cleanup(func() { require.NoError(t, writePipe.Close()) })

	tests := []struct {
		name        string
		stdout      *os.File
		destination *os.File
		forceColor  string
		wantColor   bool
	}{
		{name: "terminal destination behind pager", stdout: writePipe, destination: os.Stdout, wantColor: true},
		{name: "nonterminal final destination", stdout: os.Stdout, destination: writePipe, wantColor: false},
		{name: "forced color for nonterminal", stdout: writePipe, destination: writePipe, forceColor: "1", wantColor: true},
		{name: "disabled color for terminal", stdout: writePipe, destination: os.Stdout, forceColor: "0", wantColor: false},
	}

	for _, format := range []string{"json", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Setenv("FORCE_COLOR", tt.forceColor)

					formatted, err := formatJSONForOutput(gjson.Parse(`{"message":"safe"}`), ShowJSONOpts{
						Format: format,
						Stdout: tt.stdout,
					}, tt.destination)
					require.NoError(t, err)
					require.Equal(t, tt.wantColor, bytes.Contains(formatted, []byte("\x1b[")))
				})
			}
		})
	}
}

func TestShowJSONIteratorRawOutputEscapesPaginatedTerminalControls(t *testing.T) {
	outputPath := configureCapturePager(t)
	t.Setenv("FORCE_COLOR", "0")

	value := "\x1b]52;c;ZGF0YQ==\a\u009b31m"
	items := make([]map[string]any, 64)
	for i := range items {
		items[i] = map[string]any{"message": value}
	}
	iter := &sliceIterator[map[string]any]{items: items}

	require.NoError(t, ShowJSONIterator(iter, -1, ShowJSONOpts{
		Format:    "json",
		RawOutput: true,
		Stdout:    os.Stdout,
		Transform: "message",
	}))

	output, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat(`\u001b]52;c;ZGF0YQ==\u0007\u009b31m`+"\n", len(items)), string(output))
}

func TestShowJSONIteratorPreservesPaginatedTerminalColors(t *testing.T) {
	for _, format := range []string{"json", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			outputPath := configureCapturePager(t)
			t.Setenv("FORCE_COLOR", "auto")

			items := make([]map[string]any, 64)
			for i := range items {
				items[i] = map[string]any{"message": "safe"}
			}
			opts := ShowJSONOpts{Format: format, Stdout: os.Stdout}
			expected, err := formatJSONForOutput(gjson.Parse(`{"message":"safe"}`), opts, os.Stdout)
			require.NoError(t, err)
			require.Contains(t, string(expected), "\x1b[")

			iter := &sliceIterator[map[string]any]{items: items}
			require.NoError(t, ShowJSONIterator(iter, -1, opts))

			output, err := os.ReadFile(outputPath)
			require.NoError(t, err)
			require.Equal(t, bytes.Repeat(expected, len(items)), output)
		})
	}
}

func configureCapturePager(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("pager capture fixture requires a POSIX shell unavailable on Windows")
	}
	if !isTerminal(os.Stdout) {
		t.Skip("pager regression requires an interactive stdout terminal")
	}

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "pager-output")
	pagerPath := filepath.Join(tempDir, "capture-pager")
	pager := "#!/bin/sh\ncat > \"$OPENAI_CLI_TEST_PAGER_OUTPUT\"\n"
	require.NoError(t, os.WriteFile(pagerPath, []byte(pager), 0700))
	t.Setenv("PAGER", pagerPath)
	t.Setenv("OPENAI_CLI_TEST_PAGER_OUTPUT", outputPath)

	return outputPath
}

func rawOutputResult(t *testing.T, value string, transformed bool) (gjson.Result, string) {
	t.Helper()

	var input any = value
	transform := ""
	if transformed {
		input = map[string]string{"message": value}
		transform = "message"
	}

	encoded, err := json.Marshal(input)
	require.NoError(t, err)
	return gjson.ParseBytes(encoded), transform
}
