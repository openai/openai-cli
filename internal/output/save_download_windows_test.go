//go:build windows

package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	filepermstest "github.com/openai/openai-cli/internal/testutil/fileperms"
)

func TestWindowsDownloadPrivatePublication(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "hardlink", true: "exclusive copy"}[fallback], func(t *testing.T) {
			directory := t.TempDir()
			stage, err := newDownloadStage(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer requireNoDownloadStages(t, directory)
			defer func() {
				if err := stage.cleanup(); err != nil {
					t.Error(err)
				}
			}()
			filepermstest.AssertPrivate(t, stage.file)
			payload := []byte{0, 255, 128, 's', '\n'}
			if _, err := stage.file.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := stage.closeWriter(); err != nil {
				t.Fatal(err)
			}
			link := stage.root.Link
			if fallback {
				link = func(string, string) error { return errors.New("synthetic unsupported hardlink") }
			}
			saved, incomplete, err := publishNewDownload(context.Background(), stage, "output.bin", link)
			if err != nil || incomplete || saved == nil {
				t.Fatalf("publication=%v incomplete=%t error=%v", saved, incomplete, err)
			}
			file, err := os.Open(filepath.Join(directory, "output.bin"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			filepermstest.AssertPrivate(t, file)
			data, err := io.ReadAll(file)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("published bytes=%x error=%v", data, err)
			}
		})
	}
}
