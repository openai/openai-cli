package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestWriteBinaryResponseCommitsExplicitFilesTransactionally(t *testing.T) {
	failures := []struct {
		name string
		err  error
	}{
		{name: "cancellation", err: context.Canceled},
		{name: "truncated response", err: io.ErrUnexpectedEOF},
		{name: "destination filesystem exhausted", err: syscall.ENOSPC},
	}

	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			for _, existing := range []bool{false, true} {
				t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
					directory := t.TempDir()
					outfile := filepath.Join(directory, "download.bin")
					if existing {
						if err := os.WriteFile(outfile, []byte("original content"), 0o640); err != nil {
							t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
						}
					}

					body := io.NopCloser(&errorAfterDownloadReader{
						Reader: strings.NewReader("partial sensitive response"),
						err:    failure.err,
					})
					_, err := writeBinaryResponse(&http.Response{Body: body}, io.Discard, outfile)
					if !errors.Is(err, failure.err) {
						t.Errorf("writeBinaryResponse(%q) error = %v, want %v", outfile, err, failure.err)
					}

					content, readErr := os.ReadFile(outfile)
					if existing {
						if readErr != nil {
							t.Fatalf("os.ReadFile(%q) = %v, want original content", outfile, readErr)
						}
						if string(content) != "original content" {
							t.Errorf("writeBinaryResponse(%q) existing content = %q, want %q", outfile, content, "original content")
						}
					} else if !errors.Is(readErr, os.ErrNotExist) {
						t.Errorf("os.ReadFile(%q) error = %v, want destination to remain absent", outfile, readErr)
					}

					entries, readDirErr := os.ReadDir(directory)
					if readDirErr != nil {
						t.Fatalf("os.ReadDir(%q) = %v, want nil", directory, readDirErr)
					}
					wantEntries := 0
					if existing {
						wantEntries = 1
					}
					if len(entries) != wantEntries {
						t.Errorf("writeBinaryResponse(%q) left %d files, want %d", outfile, len(entries), wantEntries)
					}
				})
			}
		})
	}
}

func TestWriteBinaryResponsePreservesExplicitSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.bin")
	if err := os.WriteFile(target, []byte("original"), 0o640); err != nil {
		t.Fatalf("os.WriteFile(%q) = %v, want nil", target, err)
	}
	outfile := filepath.Join(directory, "download.bin")
	if err := os.Symlink(target, outfile); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating Windows symlinks requires an available privilege: %v", err)
		}
		t.Fatalf("os.Symlink(%q, %q) = %v, want nil", target, outfile, err)
	}

	response := &http.Response{Body: io.NopCloser(strings.NewReader("replacement"))}
	if _, err := writeBinaryResponse(response, io.Discard, outfile); err != nil {
		t.Fatalf("writeBinaryResponse(%q) = %v, want nil", outfile, err)
	}
	info, err := os.Lstat(outfile)
	if err != nil {
		t.Fatalf("os.Lstat(%q) = %v, want nil", outfile, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("writeBinaryResponse(%q) replaced its symlink, want existing symlink preserved", outfile)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want nil", target, err)
	}
	if string(content) != "replacement" {
		t.Errorf("writeBinaryResponse(%q) target content = %q, want %q", outfile, content, "replacement")
	}
}

func TestWriteBinaryResponsePreservesReadOnlyDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows read-only file replacement is enforced by the operating system")
	}
	directory := t.TempDir()
	outfile := filepath.Join(directory, "download.bin")
	if err := os.WriteFile(outfile, []byte("original content"), 0o400); err != nil {
		t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
	}
	if err := os.Chmod(outfile, 0o400); err != nil {
		t.Fatalf("os.Chmod(%q) = %v, want nil", outfile, err)
	}

	response := &http.Response{Body: io.NopCloser(strings.NewReader("replacement"))}
	_, err := writeBinaryResponse(response, io.Discard, outfile)
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("writeBinaryResponse(%q) error = %v, want permission denied", outfile, err)
	}
	content, err := os.ReadFile(outfile)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want original content", outfile, err)
	}
	if string(content) != "original content" {
		t.Errorf("writeBinaryResponse(%q) content = %q, want %q", outfile, content, "original content")
	}
}

func TestWriteBinaryResponseStagesExistingFilesPrivately(t *testing.T) {
	directory := t.TempDir()
	outfile := filepath.Join(directory, "download.bin")
	if err := os.WriteFile(outfile, []byte("original content"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
	}
	if err := os.Chmod(outfile, 0o644); err != nil {
		t.Fatalf("os.Chmod(%q) = %v, want nil", outfile, err)
	}
	body := &inspectingDownloadReader{
		Reader: strings.NewReader("replacement"),
		inspect: func() error {
			content, err := os.ReadFile(outfile)
			if err != nil {
				return err
			}
			if string(content) != "original content" {
				return fmt.Errorf("existing content = %q, want original content", content)
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				return err
			}
			if len(entries) != 2 {
				return fmt.Errorf("staged directory contains %d entries, want original plus one staged file", len(entries))
			}
			for _, entry := range entries {
				if entry.Name() == "download.bin" {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
					return fmt.Errorf("staging permissions = %#o, want 0600", info.Mode().Perm())
				}
			}
			return nil
		},
	}
	response := &http.Response{Body: io.NopCloser(body)}
	if _, err := writeBinaryResponse(response, io.Discard, outfile); err != nil {
		t.Fatalf("writeBinaryResponse(%q) = %v, want nil", outfile, err)
	}
	if !body.inspected {
		t.Error("writeBinaryResponse did not inspect its destination-local private staging file")
	}
	content, err := os.ReadFile(outfile)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want nil", outfile, err)
	}
	if string(content) != "replacement" {
		t.Errorf("writeBinaryResponse(%q) content = %q, want %q", outfile, content, "replacement")
	}
	info, err := os.Stat(outfile)
	if err != nil {
		t.Fatalf("os.Stat(%q) = %v, want nil", outfile, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("writeBinaryResponse(%q) permissions = %#o, want 0644", outfile, info.Mode().Perm())
	}
}

func TestWriteAutomaticBinaryResponseStagesOnDestinationFilesystem(t *testing.T) {
	for _, content := range []struct {
		name string
		body []byte
	}{
		{name: "binary", body: []byte("%PDF-1.7\nsynthetic document")},
		{name: "text", body: []byte("plain text\n")},
	} {
		t.Run(content.name, func(t *testing.T) {
			directory := t.TempDir()
			t.Chdir(directory)
			setUnavailableTempDir(t)

			response := &http.Response{Body: io.NopCloser(bytes.NewReader(content.body)), Header: http.Header{}}
			var stdout bytes.Buffer
			message, err := writeAutomaticBinaryResponse(response, &stdout)
			if err != nil {
				t.Fatalf("writeAutomaticBinaryResponse(%q, unavailable TMPDIR) = %v, want nil", content.name, err)
			}
			if content.name == "text" {
				if !bytes.Equal(stdout.Bytes(), content.body) {
					t.Errorf("writeAutomaticBinaryResponse(text) stdout = %q, want %q", stdout.Bytes(), content.body)
				}
				return
			}
			filename := strings.TrimPrefix(message, "Wrote output to: ")
			data, readErr := os.ReadFile(filename)
			if readErr != nil {
				t.Fatalf("os.ReadFile(%q) = %v, want nil", filename, readErr)
			}
			if !bytes.Equal(data, content.body) {
				t.Errorf("writeAutomaticBinaryResponse(binary) content = %q, want %q", data, content.body)
			}
		})
	}
}

func TestWriteAutomaticBinaryResponseUsesSinglePrivateDestinationFile(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	t.Setenv("TMP", temporary)
	t.Setenv("TEMP", temporary)

	body := &binaryDestinationDownloadReader{
		observingDownloadReader: observingDownloadReader{
			boundedDownloadReader: boundedDownloadReader{remaining: 2 << 20},
			spoolDir:              directory,
		},
	}
	response := &http.Response{
		Body:   body,
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}
	message, err := writeAutomaticBinaryResponse(response, io.Discard)
	if err != nil {
		t.Fatalf("writeAutomaticBinaryResponse(destination-local staging) = %v, want nil", err)
	}
	if message != "Wrote output to: download.bin" {
		t.Errorf("writeAutomaticBinaryResponse(binary response) message = %q, want %q", message, "Wrote output to: download.bin")
	}
	if !body.observedPrivateSpool {
		t.Error("writeAutomaticBinaryResponse did not stage its private file in the destination directory")
	}
	temporaryEntries, err := os.ReadDir(temporary)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) = %v, want nil", temporary, err)
	}
	if len(temporaryEntries) != 0 {
		t.Errorf("writeAutomaticBinaryResponse created %d files in TMPDIR, want none", len(temporaryEntries))
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) = %v, want nil", directory, err)
	}
	if len(entries) != 1 || entries[0].Name() != "download.bin" {
		t.Errorf("writeAutomaticBinaryResponse left entries %v, want only the committed download", entries)
	}
	info, err := os.Stat("download.bin")
	if err != nil {
		t.Fatalf("os.Stat(download.bin) = %v, want nil", err)
	}
	if info.Size() != 2<<20 {
		t.Errorf("writeAutomaticBinaryResponse(binary response) size = %d, want %d", info.Size(), 2<<20)
	}
	if body.stagedInfo == nil || !os.SameFile(body.stagedInfo, info) {
		t.Error("writeAutomaticBinaryResponse copied its staging file instead of promoting the same file")
	}
}

type inspectingDownloadReader struct {
	io.Reader
	inspect   func() error
	inspected bool
}

func (reader *inspectingDownloadReader) Read(data []byte) (int, error) {
	if !reader.inspected {
		if err := reader.inspect(); err != nil {
			return 0, err
		}
		reader.inspected = true
	}
	return reader.Reader.Read(data)
}

type binaryDestinationDownloadReader struct {
	observingDownloadReader
	emittedBinaryPrefix bool
	stagedInfo          os.FileInfo
}

func (reader *binaryDestinationDownloadReader) Read(data []byte) (int, error) {
	n, err := reader.observingDownloadReader.Read(data)
	if reader.stagedInfo == nil {
		entries, readErr := os.ReadDir(reader.spoolDir)
		if readErr != nil {
			return n, readErr
		}
		if len(entries) != 1 {
			return n, fmt.Errorf("destination staging count = %d, want one", len(entries))
		}
		reader.stagedInfo, readErr = entries[0].Info()
		if readErr != nil {
			return n, readErr
		}
	}
	if n > 0 && !reader.emittedBinaryPrefix {
		data[0] = 0xff
		reader.emittedBinaryPrefix = true
	}
	return n, err
}
