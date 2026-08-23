package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestWriteBinaryResponseStreamsLargeResponses(t *testing.T) {
	const responseSize = 8 << 20

	for _, outfile := range []string{"-", "/dev/stdout", ""} {
		t.Run(fmt.Sprintf("stdout_%q", outfile), func(t *testing.T) {
			if outfile == "" && isTerminal(os.Stdout) {
				t.Skip("automatic stdout streaming requires stdout to be redirected")
			}

			body := &boundedDownloadReader{remaining: responseSize}
			stdout := &countingDownloadWriter{}
			message, err := writeBinaryResponse(&http.Response{Body: body}, stdout, outfile)
			if err != nil {
				t.Fatalf("writeBinaryResponse(%q) returned error: %v", outfile, err)
			}
			if message != "" {
				t.Errorf("writeBinaryResponse(%q) message = %q, want empty", outfile, message)
			}
			if stdout.written != responseSize {
				t.Errorf("writeBinaryResponse(%q) wrote %d bytes, want %d", outfile, stdout.written, responseSize)
			}
			if !body.closed {
				t.Errorf("writeBinaryResponse(%q) did not close the response body", outfile)
			}
		})
	}

	t.Run("explicit_file", func(t *testing.T) {
		outfile := filepath.Join(t.TempDir(), "large.bin")
		body := &boundedDownloadReader{remaining: responseSize}
		message, err := writeBinaryResponse(&http.Response{Body: body}, io.Discard, outfile)
		if err != nil {
			t.Fatalf("writeBinaryResponse(%q) returned error: %v", outfile, err)
		}
		if want := "Wrote output to: " + outfile; message != want {
			t.Errorf("writeBinaryResponse(%q) message = %q, want %q", outfile, message, want)
		}
		info, err := os.Stat(outfile)
		if err != nil {
			t.Fatalf("os.Stat(%q) returned error: %v", outfile, err)
		}
		if info.Size() != responseSize {
			t.Errorf("writeBinaryResponse(%q) created %d bytes, want %d", outfile, info.Size(), responseSize)
		}
		if !body.closed {
			t.Errorf("writeBinaryResponse(%q) did not close the response body", outfile)
		}
	})
}

func TestWriteBinaryResponseAutomaticTerminalOutput(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("automatic terminal output requires an actual terminal")
	}

	t.Chdir(t.TempDir())
	body := io.NopCloser(strings.NewReader("%PDF-1.7\nsynthetic document"))
	response := &http.Response{Body: body, Header: http.Header{}}
	var stdout bytes.Buffer
	message, err := writeBinaryResponse(response, &stdout, "")
	if err != nil {
		t.Fatalf("writeBinaryResponse(automatic terminal output) returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("writeBinaryResponse(automatic terminal output) stdout = %q, want no stdout", stdout.Bytes())
	}
	filename := strings.TrimPrefix(message, "Wrote output to: ")
	if filepath.Ext(filename) != ".pdf" {
		t.Errorf("writeBinaryResponse(automatic terminal output) filename = %q, want PDF file", filename)
	}
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) returned error: %v", filename, err)
	}
	if string(content) != "%PDF-1.7\nsynthetic document" {
		t.Errorf("writeBinaryResponse(automatic terminal output) file content = %q, want original PDF", content)
	}
}

func TestWriteBinaryResponseStopsUnboundedResponseOnWriterError(t *testing.T) {
	wantErr := errors.New("synthetic downstream failure")
	body := &boundedDownloadReader{remaining: 1 << 40}
	stdout := &countingDownloadWriter{limit: 64 << 10, err: wantErr}

	_, err := writeBinaryResponse(&http.Response{Body: body}, stdout, "-")
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeBinaryResponse(unbounded response) error = %v, want %v", err, wantErr)
	}
	if stdout.written > 96<<10 {
		t.Errorf("writeBinaryResponse(unbounded response) wrote %d bytes, want at most %d", stdout.written, 96<<10)
	}
	if !body.closed {
		t.Error("writeBinaryResponse(unbounded response) did not close the response body")
	}
}

func TestWriteBinaryResponsePreservesExplicitFileBehavior(t *testing.T) {
	t.Run("overwrites an existing file without changing its mode", func(t *testing.T) {
		outfile := filepath.Join(t.TempDir(), "existing.txt")
		if err := os.WriteFile(outfile, []byte("long original content"), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%q) returned error: %v", outfile, err)
		}
		original, err := os.Stat(outfile)
		if err != nil {
			t.Fatalf("os.Stat(%q) returned error: %v", outfile, err)
		}
		response := &http.Response{Body: io.NopCloser(strings.NewReader("new"))}
		message, err := writeBinaryResponse(response, io.Discard, outfile)
		if err != nil {
			t.Fatalf("writeBinaryResponse(%q) returned error: %v", outfile, err)
		}
		if want := "Wrote output to: " + outfile; message != want {
			t.Errorf("writeBinaryResponse(%q) message = %q, want %q", outfile, message, want)
		}
		content, err := os.ReadFile(outfile)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) returned error: %v", outfile, err)
		}
		if string(content) != "new" {
			t.Errorf("writeBinaryResponse(%q) content = %q, want %q", outfile, content, "new")
		}
		info, err := os.Stat(outfile)
		if err != nil {
			t.Fatalf("os.Stat(%q) returned error: %v", outfile, err)
		}
		if info.Mode().Perm() != original.Mode().Perm() {
			t.Errorf("writeBinaryResponse(%q) permissions = %#o, want %#o", outfile, info.Mode().Perm(), original.Mode().Perm())
		}
	})

	t.Run("closes response when destination cannot be opened", func(t *testing.T) {
		body := &boundedDownloadReader{remaining: 128}
		outfile := filepath.Join(t.TempDir(), "missing", "output.bin")
		_, err := writeBinaryResponse(&http.Response{Body: body}, io.Discard, outfile)
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("writeBinaryResponse(%q) error = %v, want missing-path error", outfile, err)
		}
		if !body.closed {
			t.Errorf("writeBinaryResponse(%q) did not close the response body", outfile)
		}
	})
}

func TestWriteBinaryResponsePreservesExistingHardLinksAndSymlinks(t *testing.T) {
	t.Run("existing hard link", func(t *testing.T) {
		directory := t.TempDir()
		outfile := filepath.Join(directory, "download.bin")
		linked := filepath.Join(directory, "linked.bin")
		if err := os.WriteFile(outfile, []byte("original"), 0o640); err != nil {
			t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
		}
		original, err := os.Stat(outfile)
		if err != nil {
			t.Fatalf("os.Stat(%q) = %v, want nil", outfile, err)
		}
		if err := os.Link(outfile, linked); err != nil {
			t.Skipf("filesystem does not support hard links: %v", err)
		}
		response := &http.Response{Body: io.NopCloser(strings.NewReader("replacement"))}
		if _, err := writeBinaryResponse(response, io.Discard, outfile); err != nil {
			t.Fatalf("writeBinaryResponse(%q) = %v, want nil", outfile, err)
		}
		updated, err := os.Stat(outfile)
		if err != nil {
			t.Fatalf("os.Stat(%q) = %v, want nil", outfile, err)
		}
		if !os.SameFile(original, updated) {
			t.Error("writeBinaryResponse(existing hard link) replaced the original inode")
		}
		content, err := os.ReadFile(linked)
		if err != nil || string(content) != "replacement" {
			t.Errorf("os.ReadFile(%q) = %q, %v, want %q, nil", linked, content, err, "replacement")
		}
	})

	t.Run("dangling symlink chain", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.bin")
		middle := filepath.Join(directory, "current.bin")
		outfile := filepath.Join(directory, "latest.bin")
		if err := os.Symlink(filepath.Base(target), middle); err != nil {
			t.Skipf("filesystem does not support symlinks: %v", err)
		}
		if err := os.Symlink(filepath.Base(middle), outfile); err != nil {
			t.Fatalf("os.Symlink(%q, %q) = %v, want nil", middle, outfile, err)
		}
		response := &http.Response{Body: io.NopCloser(strings.NewReader("replacement"))}
		if _, err := writeBinaryResponse(response, io.Discard, outfile); err != nil {
			t.Fatalf("writeBinaryResponse(dangling symlink chain) = %v, want nil", err)
		}
		content, err := os.ReadFile(target)
		if err != nil || string(content) != "replacement" {
			t.Errorf("os.ReadFile(%q) = %q, %v, want %q, nil", target, content, err, "replacement")
		}
	})
}

func TestDownloadFileCloseErrors(t *testing.T) {
	closeErr := errors.New("synthetic destination close failure")
	copyErr := errors.New("synthetic destination copy failure")

	for _, destination := range []string{"explicit destination", "automatic destination"} {
		t.Run(destination, func(t *testing.T) {
			t.Run("reports close failure after successful copy", func(t *testing.T) {
				file := &closeTrackingDownloadFile{closeErr: closeErr}
				err := copyDownloadFile(file, strings.NewReader("download content"))
				if !errors.Is(err, closeErr) {
					t.Errorf("copyDownloadFile(%s) error = %v, want %v", destination, err, closeErr)
				}
				if file.closeCalls != 1 {
					t.Errorf("copyDownloadFile(%s) closed the destination %d times, want 1", destination, file.closeCalls)
				}
				if got := file.content.String(); got != "download content" {
					t.Errorf("copyDownloadFile(%s) content = %q, want %q", destination, got, "download content")
				}
			})

			t.Run("preserves copy failure when close also fails", func(t *testing.T) {
				file := &closeTrackingDownloadFile{closeErr: closeErr, writeErr: copyErr, writeLimit: 7}
				err := copyDownloadFile(file, strings.NewReader("download content"))
				if !errors.Is(err, copyErr) {
					t.Errorf("copyDownloadFile(%s) error = %v, want earlier copy error %v", destination, err, copyErr)
				}
				if errors.Is(err, closeErr) {
					t.Errorf("copyDownloadFile(%s) exposed close error %v instead of preserving copy error %v", destination, closeErr, copyErr)
				}
				if file.closeCalls != 1 {
					t.Errorf("copyDownloadFile(%s) closed the destination %d times, want 1", destination, file.closeCalls)
				}
				if got := file.content.String(); got != "downloa" {
					t.Errorf("copyDownloadFile(%s) partial content = %q, want %q", destination, got, "downloa")
				}
			})

			t.Run("closes a successful destination exactly once", func(t *testing.T) {
				file := &closeTrackingDownloadFile{}
				if err := copyDownloadFile(file, strings.NewReader("download content")); err != nil {
					t.Errorf("copyDownloadFile(%s) returned unexpected error: %v", destination, err)
				}
				if file.closeCalls != 1 {
					t.Errorf("copyDownloadFile(%s) closed the destination %d times, want 1", destination, file.closeCalls)
				}
			})
		})
	}
}

func TestWriteBinaryResponseDirectOutputsDoNotRequireTemporaryStorage(t *testing.T) {
	t.Run("stdout", func(t *testing.T) {
		setUnavailableTempDir(t)
		response := &http.Response{Body: io.NopCloser(strings.NewReader("streamed content"))}
		var stdout bytes.Buffer
		_, err := writeBinaryResponse(response, &stdout, "-")
		if err != nil {
			t.Fatalf("writeBinaryResponse(stdout without temporary storage) returned error: %v", err)
		}
		if stdout.String() != "streamed content" {
			t.Errorf("writeBinaryResponse(stdout without temporary storage) output = %q, want %q", stdout.String(), "streamed content")
		}
	})

	t.Run("explicit file", func(t *testing.T) {
		outfile := filepath.Join(t.TempDir(), "streamed.txt")
		setUnavailableTempDir(t)
		response := &http.Response{Body: io.NopCloser(strings.NewReader("streamed content"))}
		_, err := writeBinaryResponse(response, io.Discard, outfile)
		if err != nil {
			t.Fatalf("writeBinaryResponse(file without temporary storage) returned error: %v", err)
		}
		content, readErr := os.ReadFile(outfile)
		if readErr != nil {
			t.Fatalf("os.ReadFile(%q) returned error: %v", outfile, readErr)
		}
		if string(content) != "streamed content" {
			t.Errorf("writeBinaryResponse(file without temporary storage) content = %q, want %q", content, "streamed content")
		}
	})
}

func TestWriteBinaryResponsePropagatesReadErrorsAndShortWrites(t *testing.T) {
	t.Run("stdout cancellation", func(t *testing.T) {
		body := io.NopCloser(&errorAfterDownloadReader{
			Reader: strings.NewReader("partial response"),
			err:    context.Canceled,
		})
		stdout := &countingDownloadWriter{}
		_, err := writeBinaryResponse(&http.Response{Body: body}, stdout, "-")
		if !errors.Is(err, context.Canceled) {
			t.Errorf("writeBinaryResponse(canceled stdout) error = %v, want %v", err, context.Canceled)
		}
		if stdout.written != int64(len("partial response")) {
			t.Errorf("writeBinaryResponse(canceled stdout) wrote %d bytes, want %d", stdout.written, len("partial response"))
		}
	})

	t.Run("explicit file cancellation", func(t *testing.T) {
		outfile := filepath.Join(t.TempDir(), "partial.bin")
		body := io.NopCloser(&errorAfterDownloadReader{
			Reader: strings.NewReader("partial response"),
			err:    context.Canceled,
		})
		_, err := writeBinaryResponse(&http.Response{Body: body}, io.Discard, outfile)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("writeBinaryResponse(canceled file) error = %v, want %v", err, context.Canceled)
		}
		content, readErr := os.ReadFile(outfile)
		if readErr != nil {
			t.Fatalf("os.ReadFile(%q) returned error: %v", outfile, readErr)
		}
		if string(content) != "partial response" {
			t.Errorf("writeBinaryResponse(canceled file) content = %q, want %q", content, "partial response")
		}
	})

	t.Run("stdout short write", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader("download content"))
		_, err := writeBinaryResponse(&http.Response{Body: body}, shortDownloadWriter{}, "-")
		if !errors.Is(err, io.ErrShortWrite) {
			t.Errorf("writeBinaryResponse(short stdout write) error = %v, want %v", err, io.ErrShortWrite)
		}
	})
}

func TestWriteBinaryResponseStreamsChunkedHTTPResponse(t *testing.T) {
	const chunks = 128
	chunk := bytes.Repeat([]byte("streaming-response-"), 512)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/octet-stream")
		for index := 0; index < chunks; index++ {
			if _, err := response.Write(chunk); err != nil {
				return
			}
			response.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get(chunked test server) returned error: %v", err)
	}
	if response.ContentLength != -1 {
		t.Fatalf("chunked HTTP response ContentLength = %d, want -1", response.ContentLength)
	}
	stdout := &countingDownloadWriter{}
	_, err = writeBinaryResponse(response, stdout, "-")
	if err != nil {
		t.Fatalf("writeBinaryResponse(chunked HTTP response) returned error: %v", err)
	}
	if want := int64(chunks * len(chunk)); stdout.written != want {
		t.Errorf("writeBinaryResponse(chunked HTTP response) wrote %d bytes, want %d", stdout.written, want)
	}
}

func TestWriteAutomaticBinaryResponseRejectsTruncatedShortHTTPResponses(t *testing.T) {
	framing := []struct {
		name string
		wire string
	}{
		{
			name: "fixed content length",
			wire: "HTTP/1.1 200 OK\r\nContent-Length: 40\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nshort text",
		},
		{
			name: "chunked without terminal chunk",
			wire: "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\na\r\nshort text\r\n",
		},
	}

	for _, test := range framing {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				connection, buffered, err := response.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("Hijack(%q) = %v, want nil", test.name, err)
					return
				}
				defer connection.Close()
				if _, err := buffered.WriteString(test.wire); err != nil {
					t.Errorf("WriteString(%q response) = %v, want nil", test.name, err)
					return
				}
				if err := buffered.Flush(); err != nil {
					t.Errorf("Flush(%q response) = %v, want nil", test.name, err)
				}
			}))
			t.Cleanup(server.Close)

			response, err := http.Get(server.URL)
			if err != nil {
				t.Fatalf("http.Get(%q server) = %v, want response headers", test.name, err)
			}
			t.Cleanup(func() {
				if err := response.Body.Close(); err != nil {
					t.Errorf("response.Body.Close(%q) = %v, want nil", test.name, err)
				}
			})
			var stdout bytes.Buffer
			if _, err := writeAutomaticBinaryResponse(response, &stdout); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("writeAutomaticBinaryResponse(%q truncated HTTP body) error = %v, want %v", test.name, err, io.ErrUnexpectedEOF)
			}
			if stdout.Len() != 0 {
				t.Errorf("writeAutomaticBinaryResponse(%q truncated HTTP body) stdout = %q, want no partial output", test.name, stdout.Bytes())
			}
			entries, err := os.ReadDir(".")
			if err != nil {
				t.Fatalf("os.ReadDir(.) = %v, want nil", err)
			}
			if len(entries) != 0 {
				t.Errorf("writeAutomaticBinaryResponse(%q truncated HTTP body) left entries %v, want none", test.name, entries)
			}
		})
	}
}

func TestWriteBinaryResponseHonorsHTTPContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, "first chunk")
		response.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() returned error: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("http.DefaultClient.Do() returned error: %v", err)
	}
	stdout := &cancelingDownloadWriter{cancel: cancel}
	_, err = writeBinaryResponse(response, stdout, "-")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("writeBinaryResponse(canceled HTTP response) error = %v, want %v", err, context.Canceled)
	}
	if stdout.written != int64(len("first chunk")) {
		t.Errorf("writeBinaryResponse(canceled HTTP response) wrote %d bytes, want %d", stdout.written, len("first chunk"))
	}
}

func TestGeneratedBinaryEndpointsStreamChunkedResponses(t *testing.T) {
	tests := []struct {
		name    string
		group   string
		command cli.Command
		args    []string
		path    string
	}{
		{
			name:    "files",
			group:   "files",
			command: filesContent,
			args:    []string{"--file-id", "file_123"},
			path:    "/files/file_123/content",
		},
		{
			name:    "videos",
			group:   "videos",
			command: videosDownloadContent,
			args:    []string{"--video-id", "video_123"},
			path:    "/videos/video_123/content",
		},
		{
			name:    "audio speech",
			group:   "audio:speech",
			command: audioSpeechCreate,
			args:    []string{"--input", "synthetic text", "--model", "tts-1", "--voice", "alloy"},
			path:    "/audio/speech",
		},
		{
			name:    "container files",
			group:   "containers:files:content",
			command: containersFilesContentRetrieve,
			args:    []string{"--container-id", "container_123", "--file-id", "file_123"},
			path:    "/containers/container_123/files/file_123/content",
		},
		{
			name:    "skills",
			group:   "skills:content",
			command: skillsContentRetrieve,
			args:    []string{"--skill-id", "skill_123"},
			path:    "/skills/skill_123/content",
		},
		{
			name:    "skill versions",
			group:   "skills:versions:content",
			command: skillsVersionsContentRetrieve,
			args:    []string{"--skill-id", "skill_123", "--version", "version_1"},
			path:    "/skills/skill_123/versions/version_1/content",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const chunks = 32
			chunk := bytes.Repeat([]byte("synthetic-download-"), 512)
			requests := make(chan *http.Request, 1)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requests <- request
				response.Header().Set("Content-Type", "application/octet-stream")
				for index := 0; index < chunks; index++ {
					if _, err := response.Write(chunk); err != nil {
						return
					}
					response.(http.Flusher).Flush()
				}
			}))
			t.Cleanup(server.Close)

			outfile := filepath.Join(t.TempDir(), "download.bin")
			download := test.command
			command := &cli.Command{
				Name: "openai",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "debug"},
					&cli.StringFlag{Name: "base-url"},
					&cli.StringFlag{Name: "api-key"},
				},
				Commands: []*cli.Command{{Name: test.group, Commands: []*cli.Command{&download}}},
			}
			args := []string{
				"openai", "--base-url", server.URL + "/", "--api-key", "synthetic-test-key",
				test.group, download.Name,
			}
			args = append(args, test.args...)
			args = append(args, "--output", outfile)
			if err := command.Run(t.Context(), args); err != nil {
				t.Fatalf("generated %s download returned error: %v", test.name, err)
			}

			request := <-requests
			if request.URL.Path != test.path {
				t.Errorf("generated %s download path = %q, want %q", test.name, request.URL.Path, test.path)
			}
			if got := request.Header.Get("Authorization"); got != "Bearer synthetic-test-key" {
				t.Errorf("generated %s download Authorization = %q, want synthetic bearer token", test.name, got)
			}
			info, err := os.Stat(outfile)
			if err != nil {
				t.Fatalf("os.Stat(%q) returned error: %v", outfile, err)
			}
			if want := int64(chunks * len(chunk)); info.Size() != want {
				t.Errorf("generated %s download size = %d, want %d", test.name, info.Size(), want)
			}
		})
	}
}

func TestWriteAutomaticBinaryResponsePreservesDetectionAndFilenames(t *testing.T) {
	tests := []struct {
		name            string
		content         []byte
		contentFilename string
		wantStdout      bool
		wantFilename    string
		wantExtension   string
	}{
		{name: "empty text", content: nil, wantStdout: true},
		{name: "plain text", content: []byte("plain text\n"), wantStdout: true},
		{name: "JSON text", content: []byte(`{"message":"hello"}`), wantStdout: true},
		{
			name:       "valid UTF-8 crossing the sniff boundary",
			content:    []byte(strings.Repeat("a", 513) + "€" + " remainder"),
			wantStdout: true,
		},
		{
			name:       "valid UTF-8 with a control byte after the sniff boundary",
			content:    append(bytes.Repeat([]byte("a"), 1024), 0),
			wantStdout: true,
		},
		{
			name:       "invalid UTF-8 after the bounded sniff boundary streams as text",
			content:    append(bytes.Repeat([]byte("a"), 4096), 0xff),
			wantStdout: true,
		},
		{
			name:          "invalid UTF-8 in the sampled prefix",
			content:       []byte("invalid \xff content"),
			wantExtension: guessExtension([]byte("invalid \xff content")),
		},
		{
			name:          "PDF type detection",
			content:       []byte("%PDF-1.7\nexample document"),
			wantExtension: ".pdf",
		},
		{
			name:            "content-disposition filename",
			content:         append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("a"), 1024)...),
			contentFilename: "picture.png",
			wantFilename:    "picture.png",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			header := make(http.Header)
			if test.contentFilename != "" {
				header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", test.contentFilename))
			}
			response := &http.Response{
				Body:   io.NopCloser(bytes.NewReader(test.content)),
				Header: header,
			}
			var stdout bytes.Buffer
			message, err := writeAutomaticBinaryResponse(response, &stdout)
			if err != nil {
				t.Fatalf("writeAutomaticBinaryResponse(%q) returned error: %v", test.name, err)
			}
			if test.wantStdout {
				if message != "" {
					t.Errorf("writeAutomaticBinaryResponse(%q) message = %q, want empty", test.name, message)
				}
				if !bytes.Equal(stdout.Bytes(), test.content) {
					t.Errorf("writeAutomaticBinaryResponse(%q) stdout = %q, want %q", test.name, stdout.Bytes(), test.content)
				}
				return
			}

			if stdout.Len() != 0 {
				t.Errorf("writeAutomaticBinaryResponse(%q) wrote %q to stdout, want no stdout", test.name, stdout.Bytes())
			}
			filename := strings.TrimPrefix(message, "Wrote output to: ")
			if filename == message {
				t.Fatalf("writeAutomaticBinaryResponse(%q) message = %q, want file confirmation", test.name, message)
			}
			if test.wantFilename != "" && filename != test.wantFilename {
				t.Errorf("writeAutomaticBinaryResponse(%q) filename = %q, want %q", test.name, filename, test.wantFilename)
			}
			if test.wantExtension != "" && filepath.Ext(filename) != test.wantExtension {
				t.Errorf("writeAutomaticBinaryResponse(%q) extension = %q, want %q", test.name, filepath.Ext(filename), test.wantExtension)
			}
			content, readErr := os.ReadFile(filename)
			if readErr != nil {
				t.Fatalf("os.ReadFile(%q) returned error: %v", filename, readErr)
			}
			if !bytes.Equal(content, test.content) {
				t.Errorf("writeAutomaticBinaryResponse(%q) file content = %q, want %q", test.name, content, test.content)
			}
		})
	}
}

func TestWriteAutomaticBinaryResponsePreservesFilenameCollisions(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("existing.pdf", []byte("original"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(existing.pdf) returned error: %v", err)
	}
	response := &http.Response{
		Body: io.NopCloser(strings.NewReader("%PDF-1.7\nreplacement")),
		Header: http.Header{
			"Content-Disposition": []string{`attachment; filename="existing.pdf"`},
		},
	}
	message, err := writeAutomaticBinaryResponse(response, io.Discard)
	if err != nil {
		t.Fatalf("writeAutomaticBinaryResponse(existing filename) returned error: %v", err)
	}
	filename := strings.TrimPrefix(message, "Wrote output to: ")
	if filename == "existing.pdf" || !strings.HasPrefix(filepath.Base(filename), "existing-") || filepath.Ext(filename) != ".pdf" {
		t.Errorf("writeAutomaticBinaryResponse(existing filename) filename = %q, want existing-*.pdf", filename)
	}
	content, err := os.ReadFile("existing.pdf")
	if err != nil {
		t.Fatalf("os.ReadFile(existing.pdf) returned error: %v", err)
	}
	if string(content) != "original" {
		t.Errorf("writeAutomaticBinaryResponse(existing filename) existing content = %q, want %q", content, "original")
	}
}

func TestWriteAutomaticBinaryResponsePreservesReplacedDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents renaming an open destination handle")
	}
	t.Chdir(t.TempDir())
	content := append([]byte{0xff}, bytes.Repeat([]byte("a"), 8192)...)
	body := &inspectingDownloadReader{
		Reader: bytes.NewReader(content),
		inspect: func() error {
			if err := os.Rename("download.bin", "original.bin"); err != nil {
				return err
			}
			return os.WriteFile("download.bin", []byte("unrelated replacement"), 0o600)
		},
	}
	response := &http.Response{
		Body:   io.NopCloser(body),
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}

	if _, err := writeAutomaticBinaryResponse(response, io.Discard); !errors.Is(err, os.ErrInvalid) {
		t.Errorf("writeAutomaticBinaryResponse(replaced destination) error = %v, want %v", err, os.ErrInvalid)
	}
	for filename, want := range map[string][]byte{
		"original.bin": content,
		"download.bin": []byte("unrelated replacement"),
	} {
		got, err := os.ReadFile(filename)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("os.ReadFile(%q) = %q, %v, want %q, nil", filename, got, err, want)
		}
	}
}

func TestWriteAutomaticBinaryResponseStreamsTextWithoutSpooling(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	setUnavailableTempDir(t)
	const responseSize = 8 << 20
	body := &boundedDownloadReader{remaining: responseSize}
	stdout := &countingDownloadWriter{}
	_, err := writeAutomaticBinaryResponse(&http.Response{Body: body, Header: http.Header{}}, stdout)
	if err != nil {
		t.Fatalf("writeAutomaticBinaryResponse(large text) returned error: %v", err)
	}
	if stdout.written != responseSize {
		t.Errorf("writeAutomaticBinaryResponse(large text) wrote %d bytes, want %d", stdout.written, responseSize)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) returned error: %v", directory, err)
	}
	if len(entries) != 0 {
		t.Errorf("writeAutomaticBinaryResponse(large text) created %d temporary files, want none", len(entries))
	}
}

func TestWriteAutomaticBinaryResponseStopsUnboundedTextOnWriterError(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	setUnavailableTempDir(t)
	wantErr := errors.New("synthetic text output failure")
	body := &boundedDownloadReader{remaining: 1 << 40}
	stdout := &countingDownloadWriter{limit: 64 << 10, err: wantErr}

	_, err := writeAutomaticBinaryResponse(&http.Response{Body: body, Header: http.Header{}}, stdout)
	if !errors.Is(err, wantErr) {
		t.Errorf("writeAutomaticBinaryResponse(unbounded text) error = %v, want %v", err, wantErr)
	}
	if stdout.written > 96<<10 {
		t.Errorf("writeAutomaticBinaryResponse(unbounded text) wrote %d bytes, want at most %d", stdout.written, 96<<10)
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatalf("os.ReadDir(%q) returned error: %v", directory, readErr)
	}
	if len(entries) != 0 {
		t.Errorf("writeAutomaticBinaryResponse(unbounded text) created %d temporary files, want none", len(entries))
	}
}

func TestWriteAutomaticBinaryResponseStreamsCancellationWithoutTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	setUnavailableTempDir(t)
	body := io.NopCloser(&errorAfterDownloadReader{
		Reader: strings.NewReader("partial response"),
		err:    context.Canceled,
	})
	stdout := &countingDownloadWriter{}
	_, err := writeAutomaticBinaryResponse(&http.Response{Body: body}, stdout)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("writeAutomaticBinaryResponse(canceled response) error = %v, want %v", err, context.Canceled)
	}
	if stdout.written != 0 {
		t.Errorf("writeAutomaticBinaryResponse(canceled response) wrote %d bytes, want none", stdout.written)
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatalf("os.ReadDir(%q) returned error: %v", directory, readErr)
	}
	if len(entries) != 0 {
		t.Errorf("writeAutomaticBinaryResponse(canceled response) left %d temporary files, want none", len(entries))
	}
}

func TestWriteAutomaticBinaryResponsePropagatesOutputErrors(t *testing.T) {
	t.Run("short stdout write", func(t *testing.T) {
		response := &http.Response{Body: io.NopCloser(strings.NewReader("text content"))}
		_, err := writeAutomaticBinaryResponse(response, shortDownloadWriter{})
		if !errors.Is(err, io.ErrShortWrite) {
			t.Errorf("writeAutomaticBinaryResponse(short stdout write) error = %v, want %v", err, io.ErrShortWrite)
		}
	})

	t.Run("unavailable system temporary storage", func(t *testing.T) {
		t.Chdir(t.TempDir())
		setUnavailableTempDir(t)
		response := &http.Response{Body: io.NopCloser(strings.NewReader("text content"))}
		var stdout bytes.Buffer
		_, err := writeAutomaticBinaryResponse(response, &stdout)
		if err != nil {
			t.Errorf("writeAutomaticBinaryResponse(unavailable system temporary storage) error = %v, want nil", err)
		}
		if stdout.String() != "text content" {
			t.Errorf("writeAutomaticBinaryResponse(unavailable system temporary storage) stdout = %q, want %q", stdout.String(), "text content")
		}
	})
}

type boundedDownloadReader struct {
	remaining int64
	closed    bool
}

func (reader *boundedDownloadReader) Read(data []byte) (int, error) {
	if len(data) > 64<<10 {
		return 0, fmt.Errorf("download requested an unbounded %d-byte read buffer", len(data))
	}
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(data)) > reader.remaining {
		data = data[:reader.remaining]
	}
	for index := range data {
		data[index] = 'a'
	}
	reader.remaining -= int64(len(data))
	return len(data), nil
}

func (reader *boundedDownloadReader) Close() error {
	reader.closed = true
	return nil
}

type closeTrackingDownloadFile struct {
	content    bytes.Buffer
	closeErr   error
	writeErr   error
	writeLimit int
	closeCalls int
}

func (file *closeTrackingDownloadFile) Write(data []byte) (int, error) {
	if file.writeErr != nil {
		if len(data) > file.writeLimit {
			data = data[:file.writeLimit]
		}
		n, err := file.content.Write(data)
		if err != nil {
			return n, err
		}
		return n, file.writeErr
	}
	return file.content.Write(data)
}

func (file *closeTrackingDownloadFile) Close() error {
	file.closeCalls++
	return file.closeErr
}

type countingDownloadWriter struct {
	written int64
	limit   int64
	err     error
}

func (writer *countingDownloadWriter) Write(data []byte) (int, error) {
	if writer.limit > 0 && writer.written >= writer.limit {
		if writer.err != nil {
			return 0, writer.err
		}
		return 0, errors.New("download writer reached its limit")
	}
	writer.written += int64(len(data))
	return len(data), nil
}

type errorAfterDownloadReader struct {
	io.Reader
	err error
}

func (reader *errorAfterDownloadReader) Read(data []byte) (int, error) {
	n, err := reader.Reader.Read(data)
	if errors.Is(err, io.EOF) {
		return n, reader.err
	}
	return n, err
}

type shortDownloadWriter struct{}

func (shortDownloadWriter) Write(data []byte) (int, error) {
	return len(data) - 1, nil
}

type cancelingDownloadWriter struct {
	written int64
	cancel  context.CancelFunc
}

type inspectingDownloadReader struct {
	io.Reader
	read      bool
	inspected bool
	inspect   func() error
}

func (reader *inspectingDownloadReader) Read(data []byte) (int, error) {
	if reader.read && !reader.inspected {
		if err := reader.inspect(); err != nil {
			return 0, err
		}
		reader.inspected = true
	}
	reader.read = true
	return reader.Reader.Read(data)
}

func (writer *cancelingDownloadWriter) Write(data []byte) (int, error) {
	writer.written += int64(len(data))
	writer.cancel()
	return len(data), nil
}
