package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

type failedDownloadBody struct {
	io.Reader
	closeErr error
	closed   int
}

func (b *failedDownloadBody) Close() error { b.closed++; return b.closeErr }

func requireNoDownloadStages(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".openai-download-") {
			t.Errorf("incomplete staging file remains: %s", entry.Name())
		}
	}
}

type failedDownloadCompletion struct {
	closeTrackingDownloadFile
	truncateErr   error
	truncateCalls int
	shortWrite    bool
}

func (f *failedDownloadCompletion) Truncate(int64) error {
	f.truncateCalls++
	return f.truncateErr
}
func (f *failedDownloadCompletion) Write(data []byte) (int, error) {
	if f.shortWrite {
		return len(data) - 1, nil
	}
	return f.closeTrackingDownloadFile.Write(data)
}

func TestManagedDownloadCompletionFailures(t *testing.T) {
	for _, kind := range []string{"truncate", "write", "short write", "close", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			failure := errors.New("synthetic completion failure")
			file := &failedDownloadCompletion{}
			ctx := context.Background()
			switch kind {
			case "truncate":
				file.truncateErr = failure
			case "write":
				file.writeErr, file.writeLimit = failure, 3
			case "short write":
				file.shortWrite, failure = true, io.ErrShortWrite
			case "close":
				file.closeErr = failure
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				failure = context.Canceled
			}
			sourceClose := errors.New("synthetic staging close failure")
			source := &failedDownloadBody{Reader: strings.NewReader("complete download"), closeErr: sourceClose}
			err := completeExistingDownload(ctx, file, source)
			if !errors.Is(err, failure) || !errors.Is(err, sourceClose) {
				t.Errorf("completion lost failure causes: %v", err)
			}
			if source.closed != 1 || file.closeCalls != 1 {
				t.Errorf("source closes=%d, destination closes=%d; want one each", source.closed, file.closeCalls)
			}
			if kind == "canceled" && file.truncateCalls != 0 {
				t.Error("canceled completion truncated the existing file")
			}
		})
	}
}

func TestManagedDownloadPublicationWithoutHardlinks(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "exclusive fallback", true: "concurrent destination"}[concurrent], func(t *testing.T) {
			directory := t.TempDir()
			stage, err := newDownloadStage(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stage.cleanup(); err != nil {
					t.Error(err)
				}
			}()
			payload := []byte{0, 255, 128, 10}
			if _, err := stage.file.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := stage.closeWriter(); err != nil {
				t.Fatal(err)
			}
			link := func(string, string) error {
				if concurrent {
					if err := os.WriteFile(filepath.Join(directory, "output.bin"), []byte("unrelated"), 0o600); err != nil {
						return err
					}
				}
				return errors.New("synthetic filesystem without hardlink support")
			}
			saved, incomplete, err := publishNewDownload(context.Background(), stage, "output.bin", link)
			if concurrent {
				if err == nil || saved != nil || incomplete {
					t.Errorf("concurrent publication = %v, %v, %v", saved, incomplete, err)
				}
				payload = []byte("unrelated")
			} else if err != nil || saved == nil || incomplete {
				t.Errorf("fallback publication = %v, %v, %v", saved, incomplete, err)
			}
			actual, readErr := os.ReadFile(filepath.Join(directory, "output.bin"))
			if readErr != nil || !bytes.Equal(actual, payload) {
				t.Errorf("published bytes = %q, %v", actual, readErr)
			}
		})
	}
}
