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

func TestWriteBinaryResponsePromotesNewFileWithoutCopy(t *testing.T) {
	directory := t.TempDir()
	outfile := filepath.Join(directory, "download.bin")
	var staged os.FileInfo
	body := &inspectingDownloadReader{
		Reader: strings.NewReader("complete destination-local download"),
		inspect: func() error {
			entries, err := os.ReadDir(directory)
			if err != nil {
				return err
			}
			if len(entries) != 1 {
				return fmt.Errorf("destination stage count = %d, want exactly one private stage", len(entries))
			}
			staged, err = entries[0].Info()
			return err
		},
	}
	response := &http.Response{Body: io.NopCloser(body)}

	if _, err := writeBinaryResponse(response, io.Discard, outfile); err != nil {
		t.Fatalf("writeBinaryResponse(new explicit destination %q) = %v, want nil", outfile, err)
	}
	if !body.inspected {
		t.Error("writeBinaryResponse(new explicit destination) did not observe its private destination-local stage")
	}
	committed, err := os.Stat(outfile)
	if err != nil {
		t.Fatalf("os.Stat(%q) = %v, want committed destination", outfile, err)
	}
	if staged == nil || !os.SameFile(staged, committed) {
		t.Error("writeBinaryResponse(new explicit destination) copied its complete stage instead of promoting the same inode")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) = %v, want nil", directory, err)
	}
	if len(entries) != 1 || entries[0].Name() != "download.bin" {
		t.Errorf("writeBinaryResponse(new explicit destination) left entries %v, want only download.bin", entries)
	}
}

func TestWriteBinaryResponsePreservesConcurrentNewDestination(t *testing.T) {
	directory := t.TempDir()
	outfile := filepath.Join(directory, "download.bin")
	body := &inspectingDownloadReader{
		Reader: strings.NewReader("response content"),
		inspect: func() error {
			return os.WriteFile(outfile, []byte("concurrent owner"), 0o600)
		},
	}
	response := &http.Response{Body: io.NopCloser(body)}

	if _, err := writeBinaryResponse(response, io.Discard, outfile); !errors.Is(err, os.ErrExist) {
		t.Errorf("writeBinaryResponse(concurrently created destination %q) error = %v, want %v", outfile, err, os.ErrExist)
	}
	content, err := os.ReadFile(outfile)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want preserved concurrent destination", outfile, err)
	}
	if string(content) != "concurrent owner" {
		t.Errorf("writeBinaryResponse(concurrently created destination %q) content = %q, want %q", outfile, content, "concurrent owner")
	}
}

func TestWriteBinaryResponseRejectsMissingDirectoryBeforeReading(t *testing.T) {
	outfile := filepath.Join(t.TempDir(), "missing", "download.bin")
	body := &inspectingDownloadReader{
		Reader: strings.NewReader("response must not be consumed"),
		inspect: func() error {
			return nil
		},
	}
	response := &http.Response{Body: io.NopCloser(body)}

	if _, err := writeBinaryResponse(response, io.Discard, outfile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("writeBinaryResponse(missing parent %q) error = %v, want %v", outfile, err, os.ErrNotExist)
	}
	if body.inspected {
		t.Error("writeBinaryResponse(missing parent) consumed response bytes before rejecting the impossible destination")
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

func TestWriteBinaryResponseCreatesDanglingSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	runs := filepath.Join(directory, "runs")
	if err := os.Mkdir(runs, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) = %v, want nil", runs, err)
	}
	outfile := filepath.Join(directory, "latest.bin")
	if err := os.Symlink(filepath.Join("runs", "new.bin"), outfile); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating Windows symlinks requires an available privilege: %v", err)
		}
		t.Fatalf("os.Symlink(%q) = %v, want nil", outfile, err)
	}

	response := &http.Response{Body: io.NopCloser(strings.NewReader("new target content"))}
	if _, err := writeBinaryResponse(response, io.Discard, outfile); err != nil {
		t.Fatalf("writeBinaryResponse(dangling symlink %q) = %v, want nil", outfile, err)
	}
	link, err := os.Lstat(outfile)
	if err != nil {
		t.Fatalf("os.Lstat(%q) = %v, want preserved symlink", outfile, err)
	}
	if link.Mode()&os.ModeSymlink == 0 {
		t.Errorf("writeBinaryResponse(%q) replaced its dangling symlink, want link preserved", outfile)
	}
	target := filepath.Join(runs, "new.bin")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want newly created target", target, err)
	}
	if string(content) != "new target content" {
		t.Errorf("writeBinaryResponse(%q) target content = %q, want %q", outfile, content, "new target content")
	}
	assertDownloadPermissions(t, target, 0o600)
}

func TestWriteBinaryResponseFollowsDanglingSymlinkChain(t *testing.T) {
	directory := t.TempDir()
	runs := filepath.Join(directory, "runs")
	if err := os.Mkdir(runs, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) = %v, want nil", runs, err)
	}
	links := filepath.Join(directory, "links")
	if err := os.Mkdir(links, 0o700); err != nil {
		t.Fatalf("os.Mkdir(%q) = %v, want nil", links, err)
	}
	current := filepath.Join(links, "current.bin")
	if err := os.Symlink(filepath.Join("..", "runs", "new.bin"), current); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating Windows symlinks requires an available privilege: %v", err)
		}
		t.Fatalf("os.Symlink(../runs/new.bin, %q) = %v, want nil", current, err)
	}
	latest := filepath.Join(directory, "latest.bin")
	if err := os.Symlink(filepath.Join("links", "current.bin"), latest); err != nil {
		t.Fatalf("os.Symlink(links/current.bin, %q) = %v, want nil", latest, err)
	}

	response := &http.Response{Body: io.NopCloser(strings.NewReader("new target content"))}
	if _, err := writeBinaryResponse(response, io.Discard, latest); err != nil {
		t.Fatalf("writeBinaryResponse(dangling symlink chain %q) = %v, want nil", latest, err)
	}
	for _, link := range []string{latest, current} {
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("os.Lstat(%q) = %v, want preserved symlink", link, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("writeBinaryResponse(%q) replaced symlink %q, want the full chain preserved", latest, link)
		}
	}
	target := filepath.Join(runs, "new.bin")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want newly created terminal target", target, err)
	}
	if string(content) != "new target content" {
		t.Errorf("writeBinaryResponse(%q) terminal target = %q, want %q", latest, content, "new target content")
	}
	assertDownloadPermissions(t, target, 0o600)
}

func TestWriteBinaryResponseUpdatesExistingFileInReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not model POSIX directory write permissions")
	}
	directory := t.TempDir()
	outfile := filepath.Join(directory, "download.bin")
	if err := os.WriteFile(outfile, []byte("original content"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatalf("os.Chmod(%q) = %v, want nil", directory, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Errorf("os.Chmod(%q, 0700) = %v, want nil", directory, err)
		}
	})

	response := &http.Response{Body: io.NopCloser(strings.NewReader("replacement"))}
	if _, err := writeBinaryResponse(response, io.Discard, outfile); err != nil {
		t.Fatalf("writeBinaryResponse(existing file in read-only directory) = %v, want nil", err)
	}
	content, err := os.ReadFile(outfile)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want nil", outfile, err)
	}
	if string(content) != "replacement" {
		t.Errorf("writeBinaryResponse(%q) content = %q, want %q", outfile, content, "replacement")
	}
}

func TestWriteBinaryResponsePreservesExistingInodeAndHardLinks(t *testing.T) {
	directory := t.TempDir()
	outfile := filepath.Join(directory, "download.bin")
	if err := os.WriteFile(outfile, []byte("original content"), 0o640); err != nil {
		t.Fatalf("os.WriteFile(%q) = %v, want nil", outfile, err)
	}
	original, err := os.Stat(outfile)
	if err != nil {
		t.Fatalf("os.Stat(%q) = %v, want nil", outfile, err)
	}
	hardLink := filepath.Join(directory, "linked.bin")
	if err := os.Link(outfile, hardLink); err != nil {
		t.Skipf("the destination filesystem does not support hard links: %v", err)
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
		t.Errorf("writeBinaryResponse(%q) replaced the original inode, want in-place update", outfile)
	}
	content, err := os.ReadFile(hardLink)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want updated hard-link content", hardLink, err)
	}
	if string(content) != "replacement" {
		t.Errorf("writeBinaryResponse(%q) hard-link content = %q, want %q", outfile, content, "replacement")
	}
	assertDownloadPermissions(t, outfile, 0o640)
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

func TestWriteAutomaticBinaryResponsePrintsTextFromReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not model POSIX directory write permissions")
	}
	directory := t.TempDir()
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	t.Setenv("TMP", temporary)
	t.Setenv("TEMP", temporary)
	t.Chdir(directory)
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatalf("os.Chmod(%q) = %v, want nil", directory, err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Errorf("os.Chmod(%q, 0700) = %v, want nil", directory, err)
		}
	})

	response := &http.Response{Body: io.NopCloser(strings.NewReader("plain text\n")), Header: http.Header{}}
	var stdout bytes.Buffer
	message, err := writeAutomaticBinaryResponse(response, &stdout)
	if err != nil {
		t.Fatalf("writeAutomaticBinaryResponse(text in read-only directory) = %v, want nil", err)
	}
	if message != "" {
		t.Errorf("writeAutomaticBinaryResponse(text) message = %q, want empty", message)
	}
	if stdout.String() != "plain text\n" {
		t.Errorf("writeAutomaticBinaryResponse(text) stdout = %q, want %q", stdout.String(), "plain text\n")
	}
	entries, err := os.ReadDir(temporary)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) = %v, want nil", temporary, err)
	}
	if len(entries) != 0 {
		t.Errorf("writeAutomaticBinaryResponse(text) leaked %d temporary files, want none", len(entries))
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
		t.Error("writeAutomaticBinaryResponse did not observe its private destination file while streaming")
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
	if body.destinationInfo == nil || !os.SameFile(body.destinationInfo, info) {
		t.Error("writeAutomaticBinaryResponse replaced its exclusively opened destination file")
	}
}

func TestWriteAutomaticBinaryResponseWritesThroughReservedFile(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	t.Setenv("TMP", temporary)
	t.Setenv("TEMP", temporary)

	content := append([]byte{0xff}, bytes.Repeat([]byte("a"), 4096)...)
	var reserved os.FileInfo
	body := &inspectAfterPrefixDownloadReader{
		Reader: bytes.NewReader(content),
		inspect: func() error {
			entries, err := os.ReadDir(directory)
			if err != nil {
				return err
			}
			if len(entries) != 1 || entries[0].Name() != "download.bin" {
				return fmt.Errorf("destination entries = %v, want only the exclusively reserved download.bin", entries)
			}
			reserved, err = entries[0].Info()
			return err
		},
	}
	response := &http.Response{
		Body:   io.NopCloser(body),
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}

	message, err := writeAutomaticBinaryResponse(response, io.Discard)
	if err != nil {
		t.Fatalf("writeAutomaticBinaryResponse(exclusive binary destination) = %v, want nil", err)
	}
	if message != "Wrote output to: download.bin" {
		t.Errorf("writeAutomaticBinaryResponse(exclusive binary destination) message = %q, want %q", message, "Wrote output to: download.bin")
	}
	if !body.inspected {
		t.Error("writeAutomaticBinaryResponse did not retain its exclusive destination while streaming")
	}
	info, err := os.Stat("download.bin")
	if err != nil {
		t.Fatalf("os.Stat(download.bin) = %v, want nil", err)
	}
	if reserved == nil || !os.SameFile(reserved, info) {
		t.Error("writeAutomaticBinaryResponse replaced its reserved file instead of writing through the original handle")
	}
	written, err := os.ReadFile("download.bin")
	if err != nil {
		t.Fatalf("os.ReadFile(download.bin) = %v, want nil", err)
	}
	if !bytes.Equal(written, content) {
		t.Errorf("writeAutomaticBinaryResponse(exclusive binary destination) wrote %d bytes, want %d", len(written), len(content))
	}
	temporaryEntries, err := os.ReadDir(temporary)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) = %v, want nil", temporary, err)
	}
	if len(temporaryEntries) != 0 {
		t.Errorf("writeAutomaticBinaryResponse(binary) created %d temporary files, want none", len(temporaryEntries))
	}
}

func TestWriteAutomaticBinaryResponseDoesNotOverwriteReplacedReservation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents removing an open destination handle")
	}
	t.Chdir(t.TempDir())
	body := &inspectAfterPrefixDownloadReader{
		Reader: bytes.NewReader(append([]byte{0xff}, bytes.Repeat([]byte("a"), 4096)...)),
		inspect: func() error {
			if err := os.Remove("download.bin"); err != nil {
				return err
			}
			return os.WriteFile("download.bin", []byte("unrelated replacement"), 0o600)
		},
	}
	response := &http.Response{
		Body:   io.NopCloser(body),
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}

	if _, err := writeAutomaticBinaryResponse(response, io.Discard); err != nil {
		t.Fatalf("writeAutomaticBinaryResponse(replaced reservation) = %v, want nil", err)
	}
	if !body.inspected {
		t.Error("writeAutomaticBinaryResponse did not exercise replacement of its open exclusive reservation")
	}
	content, err := os.ReadFile("download.bin")
	if err != nil {
		t.Fatalf("os.ReadFile(download.bin) = %v, want preserved replacement", err)
	}
	if string(content) != "unrelated replacement" {
		t.Errorf("writeAutomaticBinaryResponse(replaced reservation) overwrote %q, want %q", content, "unrelated replacement")
	}
}

func TestWriteAutomaticBinaryResponseRemovesInterruptedReservedFile(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	content := append([]byte{0xff}, bytes.Repeat([]byte("a"), 4096)...)
	response := &http.Response{
		Body: io.NopCloser(&errorAfterDownloadReader{
			Reader: bytes.NewReader(content),
			err:    context.Canceled,
		}),
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}

	if _, err := writeAutomaticBinaryResponse(response, io.Discard); !errors.Is(err, context.Canceled) {
		t.Errorf("writeAutomaticBinaryResponse(interrupted exclusive destination) error = %v, want %v", err, context.Canceled)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) = %v, want nil", directory, err)
	}
	if len(entries) != 0 {
		t.Errorf("writeAutomaticBinaryResponse(interrupted exclusive destination) left entries %v, want none", entries)
	}
}

func TestWriteAutomaticBinaryResponsePreservesReplacedReservationOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents removing an open destination handle")
	}
	t.Chdir(t.TempDir())
	body := &inspectAfterPrefixDownloadReader{
		Reader: &errorAfterDownloadReader{
			Reader: bytes.NewReader(append([]byte{0xff}, bytes.Repeat([]byte("a"), 4096)...)),
			err:    context.Canceled,
		},
		inspect: func() error {
			if err := os.Remove("download.bin"); err != nil {
				return err
			}
			return os.WriteFile("download.bin", []byte("unrelated replacement"), 0o600)
		},
	}
	response := &http.Response{
		Body:   io.NopCloser(body),
		Header: http.Header{"Content-Disposition": []string{`attachment; filename="download.bin"`}},
	}

	if _, err := writeAutomaticBinaryResponse(response, io.Discard); !errors.Is(err, context.Canceled) {
		t.Errorf("writeAutomaticBinaryResponse(replaced reservation canceled) error = %v, want %v", err, context.Canceled)
	}
	if !body.inspected {
		t.Error("writeAutomaticBinaryResponse(replaced reservation canceled) did not replace its original reserved inode")
	}
	content, err := os.ReadFile("download.bin")
	if err != nil {
		t.Fatalf("os.ReadFile(download.bin) = %v, want unrelated replacement to remain", err)
	}
	if string(content) != "unrelated replacement" {
		t.Errorf("writeAutomaticBinaryResponse(replaced reservation canceled) replacement = %q, want %q", content, "unrelated replacement")
	}
}

func TestWriteAutomaticBinaryResponsePreservesReplacedPrivateStageOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents removing an open staging handle")
	}
	directory := t.TempDir()
	t.Chdir(directory)
	var replaced string
	body := &inspectAfterPrefixDownloadReader{
		Reader: &errorAfterDownloadReader{
			Reader: bytes.NewReader(bytes.Repeat([]byte("a"), 8192)),
			err:    context.Canceled,
		},
		inspect: func() error {
			entries, err := os.ReadDir(directory)
			if err != nil {
				return err
			}
			if len(entries) != 1 {
				return fmt.Errorf("private stage count = %d, want one", len(entries))
			}
			replaced = filepath.Join(directory, entries[0].Name())
			if err := os.Remove(replaced); err != nil {
				return err
			}
			return os.WriteFile(replaced, []byte("unrelated replacement"), 0o600)
		},
	}
	response := &http.Response{Body: io.NopCloser(body), Header: http.Header{}}

	if _, err := writeAutomaticBinaryResponse(response, io.Discard); !errors.Is(err, context.Canceled) {
		t.Errorf("writeAutomaticBinaryResponse(replaced private stage canceled) error = %v, want %v", err, context.Canceled)
	}
	if !body.inspected {
		t.Fatal("writeAutomaticBinaryResponse(replaced private stage canceled) never replaced its stage")
	}
	content, err := os.ReadFile(replaced)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) = %v, want unrelated stage replacement to remain", replaced, err)
	}
	if string(content) != "unrelated replacement" {
		t.Errorf("writeAutomaticBinaryResponse(replaced private stage canceled) content = %q, want %q", content, "unrelated replacement")
	}
}

type inspectAfterPrefixDownloadReader struct {
	io.Reader
	read      bool
	inspected bool
	inspect   func() error
}

func (reader *inspectAfterPrefixDownloadReader) Read(data []byte) (int, error) {
	if reader.read && !reader.inspected {
		if err := reader.inspect(); err != nil {
			return 0, err
		}
		reader.inspected = true
	}
	reader.read = true
	return reader.Reader.Read(data)
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
	destinationInfo     os.FileInfo
}

func (reader *binaryDestinationDownloadReader) Read(data []byte) (int, error) {
	n, err := reader.observingDownloadReader.Read(data)
	if n > 0 && !reader.emittedBinaryPrefix {
		data[0] = 0xff
		reader.emittedBinaryPrefix = true
	}
	if reader.destinationInfo == nil {
		entries, readErr := os.ReadDir(reader.spoolDir)
		if readErr != nil {
			return n, readErr
		}
		if len(entries) == 0 {
			return n, err
		}
		if len(entries) != 1 {
			return n, fmt.Errorf("destination file count = %d, want one", len(entries))
		}
		reader.destinationInfo, readErr = entries[0].Info()
		if readErr != nil {
			return n, readErr
		}
	}
	return n, err
}
