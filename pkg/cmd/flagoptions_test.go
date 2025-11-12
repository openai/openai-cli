package cmd

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsUTF8TextFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content  []byte
		expected bool
	}{
		{[]byte("Hello, world!"), true},
		{[]byte(`{"key": "value"}`), true},
		{[]byte(`<?xml version="1.0"?><root/>`), true},
		{[]byte(`function test() {}`), true},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, false}, // JPEG header
		{[]byte{0x00, 0x01, 0xFF, 0xFE}, false}, // binary
		{[]byte("Hello \xFF\xFE"), false},       // invalid UTF-8
		{[]byte("Hello ☺️"), true},              // emoji
		{[]byte{}, true},                        // empty
	}

	for _, tt := range tests {
		require.Equal(t, tt.expected, isUTF8TextFile(tt.content))
	}
}

func TestEmbedFiles(t *testing.T) {
	t.Parallel()

	// Create temporary directory for test files
	tmpDir := t.TempDir()

	// Create test files
	configContent := "host=localhost\nport=8080"
	templateContent := "<html><body>Hello</body></html>"
	dataContent := `{"key": "value"}`

	writeTestFile(t, tmpDir, "config.txt", configContent)
	writeTestFile(t, tmpDir, "template.html", templateContent)
	writeTestFile(t, tmpDir, "data.json", dataContent)
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}
	writeTestFile(t, tmpDir, "image.jpg", string(jpegHeader))

	tests := []struct {
		name    string
		input   any
		want    any
		wantErr bool
	}{
		{
			name: "map[string]any with file references",
			input: map[string]any{
				"config":   "@" + filepath.Join(tmpDir, "config.txt"),
				"template": "@file://" + filepath.Join(tmpDir, "template.html"),
				"count":    42,
			},
			want: map[string]any{
				"config":   configContent,
				"template": templateContent,
				"count":    42,
			},
			wantErr: false,
		},
		{
			name: "map[string]string with file references",
			input: map[string]any{
				"config": "@" + filepath.Join(tmpDir, "config.txt"),
				"name":   "test",
			},
			want: map[string]any{
				"config": configContent,
				"name":   "test",
			},
			wantErr: false,
		},
		{
			name: "[]any with file references",
			input: []any{
				"@" + filepath.Join(tmpDir, "config.txt"),
				42,
				true,
				"@file://" + filepath.Join(tmpDir, "data.json"),
			},
			want: []any{
				configContent,
				42,
				true,
				dataContent,
			},
			wantErr: false,
		},
		{
			name: "[]string with file references",
			input: []any{
				"@" + filepath.Join(tmpDir, "config.txt"),
				"normal string",
			},
			want: []any{
				configContent,
				"normal string",
			},
			wantErr: false,
		},
		{
			name: "nested structures",
			input: map[string]any{
				"outer": map[string]any{
					"inner": []any{
						"@" + filepath.Join(tmpDir, "config.txt"),
						map[string]any{
							"data": "@" + filepath.Join(tmpDir, "data.json"),
						},
					},
				},
			},
			want: map[string]any{
				"outer": map[string]any{
					"inner": []any{
						configContent,
						map[string]any{
							"data": dataContent,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "base64 encoding",
			input: map[string]any{
				"encoded": "@data://" + filepath.Join(tmpDir, "config.txt"),
				"image":   "@" + filepath.Join(tmpDir, "image.jpg"),
			},
			want: map[string]any{
				"encoded": base64.StdEncoding.EncodeToString([]byte(configContent)),
				"image":   base64.StdEncoding.EncodeToString(jpegHeader),
			},
			wantErr: false,
		},
		{
			name: "non-existent file with @ prefix",
			input: map[string]any{
				"missing": "@file.txt",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "non-file-like thing with @ prefix",
			input: map[string]any{
				"username":        "@user",
				"favorite_symbol": "@",
			},
			want: map[string]any{
				"username":        "@user",
				"favorite_symbol": "@",
			},
			wantErr: false,
		},
		{
			name: "non-existent file with @file:// prefix (error)",
			input: map[string]any{
				"missing": "@file:///nonexistent/file.txt",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "escaping",
			input: map[string]any{
				"simple":      "\\@file.txt",
				"file":        "\\@file://file.txt",
				"data":        "\\@data://file.txt",
				"keep_escape": "user\\@example.com",
			},
			want: map[string]any{
				"simple":      "@file.txt",
				"file":        "@file://file.txt",
				"data":        "@data://file.txt",
				"keep_escape": "user\\@example.com",
			},
			wantErr: false,
		},
		{
			name: "primitive types",
			input: map[string]any{
				"int":    123,
				"float":  45.67,
				"bool":   true,
				"null":   nil,
				"string": "no prefix",
				"email":  "user@example.com",
			},
			want: map[string]any{
				"int":    123,
				"float":  45.67,
				"bool":   true,
				"null":   nil,
				"string": "no prefix",
				"email":  "user@example.com",
			},
			wantErr: false,
		},
		{
			name:    "[]int values unchanged",
			input:   []int{1, 2, 3, 4, 5},
			want:    []any{1, 2, 3, 4, 5},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" text", func(t *testing.T) {
			t.Parallel()

			got, err := embedFiles(tt.input, EmbedText, nil)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})

		t.Run(tt.name+" io.Reader", func(t *testing.T) {
			t.Parallel()

			_, err := embedFiles(tt.input, EmbedIOReader, nil)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestEmbedFilesStdin(t *testing.T) {
	t.Parallel()

	t.Run("FilePathValueDash", func(t *testing.T) {
		t.Parallel()

		stdin := &onceStdinReader{stdinReader: strings.NewReader("stdin content")}

		withEmbedded, err := embedFiles(map[string]any{"file": FilePathValue("-")}, EmbedText, stdin)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"file": "stdin content"}, withEmbedded)
	})

	t.Run("FilePathValueDevStdin", func(t *testing.T) {
		t.Parallel()

		stdin := &onceStdinReader{stdinReader: strings.NewReader("stdin content")}

		withEmbedded, err := embedFiles(map[string]any{"file": FilePathValue("/dev/stdin")}, EmbedText, stdin)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"file": "stdin content"}, withEmbedded)
	})

	t.Run("MultipleFilePathValueDashesError", func(t *testing.T) {
		t.Parallel()

		stdin := &onceStdinReader{stdinReader: strings.NewReader("stdin content")}

		_, err := embedFiles(map[string]any{
			"file1": FilePathValue("-"),
			"file2": FilePathValue("-"),
		}, EmbedText, stdin)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already been read")
	})

	t.Run("FilePathValueDashUnavailableStdin", func(t *testing.T) {
		t.Parallel()

		stdin := &onceStdinReader{failureReason: "stdin is already being used for the request body"}

		_, err := embedFiles(map[string]any{"file": FilePathValue("-")}, EmbedText, stdin)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot read from stdin")
		require.Contains(t, err.Error(), "request body")
	})

	t.Run("AtDashEmbedText", func(t *testing.T) {
		t.Parallel()

		stdin := &onceStdinReader{stdinReader: strings.NewReader("piped content")}

		withEmbedded, err := embedFiles(map[string]any{"data": "@-"}, EmbedText, stdin)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"data": "piped content"}, withEmbedded)
	})

	t.Run("AtDashEmbedIOReader", func(t *testing.T) {
		t.Parallel()

		stdin := &onceStdinReader{stdinReader: strings.NewReader("piped content")}

		withEmbedded, err := embedFiles(map[string]any{"data": "@-"}, EmbedIOReader, stdin)
		require.NoError(t, err)

		withEmbeddedMap := withEmbedded.(map[string]any)
		r := withEmbeddedMap["data"].(io.ReadCloser)

		content, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, "piped content", string(content))
	})

	t.Run("FilePathValueRealFile", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		writeTestFile(t, tmpDir, "test.txt", "file content")

		stdin := &onceStdinReader{stdinReader: strings.NewReader("unused stdin")}

		withEmbedded, err := embedFiles(map[string]any{"file": FilePathValue(filepath.Join(tmpDir, "test.txt"))}, EmbedText, stdin)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"file": "file content"}, withEmbedded)
	})
}

// TestEmbedFilesUploadMetadata verifies that EmbedIOReader mode wraps file readers with filename and
// content-type metadata so the multipart encoder populates `Content-Disposition` and `Content-Type` headers.
func TestEmbedFilesUploadMetadata(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	writeTestFile(t, tmpDir, "hello.txt", "hi")
	writeTestFile(t, tmpDir, "page.html", "<html/>")
	writeTestFile(t, tmpDir, "blob.bin", "\x00\x01")

	cases := []struct {
		basename        string
		wantContentType string
	}{
		{"hello.txt", "text/plain; charset=utf-8"},
		{"page.html", "text/html; charset=utf-8"},
		{"blob.bin", "application/octet-stream"},
	}

	for _, tc := range cases {
		t.Run("AtPrefix_"+tc.basename, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(tmpDir, tc.basename)
			withEmbedded, err := embedFiles(map[string]any{"file": "@" + path}, EmbedIOReader, nil)
			require.NoError(t, err)

			upload, ok := withEmbedded.(map[string]any)["file"].(fileUpload)
			require.True(t, ok, "expected fileUpload, got %T", withEmbedded.(map[string]any)["file"])
			require.Equal(t, tc.basename, upload.Filename())
			require.Equal(t, upload.ContentType(), tc.wantContentType)
			require.NoError(t, upload.Close())
		})

		t.Run("FilePathValue_"+tc.basename, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(tmpDir, tc.basename)
			withEmbedded, err := embedFiles(map[string]any{"file": FilePathValue(path)}, EmbedIOReader, nil)
			require.NoError(t, err)

			upload, ok := withEmbedded.(map[string]any)["file"].(fileUpload)
			require.True(t, ok, "expected fileUpload, got %T", withEmbedded.(map[string]any)["file"])
			require.Equal(t, tc.basename, upload.Filename())
			require.Equal(t, upload.ContentType(), tc.wantContentType)
			require.NoError(t, upload.Close())
		})
	}
}

func writeTestFile(t *testing.T, dir, filename, content string) {
	t.Helper()

	path := filepath.Join(dir, filename)

	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err, "failed to write test file %s", path)
}
